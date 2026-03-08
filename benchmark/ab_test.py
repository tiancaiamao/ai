#!/usr/bin/env python3
"""
A/B Testing Framework for AI Coding Agents

Usage:
    python ab_test.py --agent my-agent                    # Run single agent
    python ab_test.py --agent my-agent --model glm5       # Run with specific model
    python ab_test.py --compare my-agent claude-code      # Compare two agents
    python ab_test.py --benchmark                         # Run all configured agents
    python ab_test.py --report                            # Generate comparison report
"""

import argparse
import json
import os
import subprocess
import sys
import time
from datetime import datetime
from pathlib import Path
from typing import Any

import yaml


class Colors:
    """Terminal colors"""
    GREEN = '\033[92m'
    RED = '\033[91m'
    YELLOW = '\033[93m'
    BLUE = '\033[94m'
    BOLD = '\033[1m'
    END = '\033[0m'


class ABTestFramework:
    def __init__(self, benchmark_dir: Path):
        self.benchmark_dir = benchmark_dir
        self.tasks_dir = benchmark_dir / "tasks"
        self.results_dir = benchmark_dir / "results"
        self.config_file = benchmark_dir / "agents.yaml"

        self.results_dir.mkdir(exist_ok=True)

        # Load configuration
        self.config = self._load_config()

    def _load_config(self) -> dict:
        """Load agent and model configurations"""
        if self.config_file.exists():
            with open(self.config_file) as f:
                return yaml.safe_load(f)
        return {"agents": {}, "models": {}, "config": {}}

    def _get_tasks(self) -> list[str]:
        """Get list of available tasks"""
        tasks = []
        for task_dir in sorted(self.tasks_dir.iterdir()):
            if task_dir.is_dir() and (task_dir / "task.md").exists():
                tasks.append(task_dir.name)
        return tasks

    def _get_task_prompt(self, task_id: str) -> str:
        """Generate prompt for a task"""
        task_dir = self.tasks_dir / task_id
        task_file = task_dir / "task.md"
        setup_dir = task_dir / "setup"

        with open(task_file) as f:
            task_desc = f.read()

        prompt = f"""You are given a coding task. Read the task description and fix/implement the code.

Task ID: {task_id}
Working Directory: {setup_dir}

Task Description:
{task_desc}

Instructions:
1. Read the files in {setup_dir}
2. Fix the bugs or implement the required functionality
3. Make sure the code compiles
4. Do NOT modify any verification scripts (verify.sh)

Please start by reading the task files and understanding what needs to be done."""

        return prompt

    def _run_agent(self, agent_name: str, prompt: str, model: str = None) -> dict:
        """Run an agent with the given prompt"""
        agent_config = self.config.get("agents", {}).get(agent_name)

        if not agent_config:
            return {"success": False, "error": f"Agent '{agent_name}' not found in config"}

        command = agent_config["command"]

        # Replace placeholders
        command = command.replace("{prompt}", prompt.replace('"', '\\"'))

        # Add model if specified
        if model:
            # For my-agent, model is configured via config file
            # This is agent-specific
            pass

        start_time = time.time()

        try:
            result = subprocess.run(
                command,
                shell=True,
                capture_output=True,
                text=True,
                timeout=self.config.get("config", {}).get("timeout", 300)
            )

            return {
                "success": result.returncode == 0,
                "output": result.stdout,
                "error": result.stderr,
                "duration": time.time() - start_time
            }
        except subprocess.TimeoutExpired:
            return {"success": False, "error": "Timeout", "duration": time.time() - start_time}
        except Exception as e:
            return {"success": False, "error": str(e), "duration": time.time() - start_time}

    def _verify_task(self, task_id: str) -> dict:
        """Verify a task"""
        task_dir = self.tasks_dir / task_id
        verify_script = task_dir / "verify.sh"

        if not verify_script.exists():
            return {"passed": False, "error": "No verify.sh found"}

        try:
            result = subprocess.run(
                ["bash", str(verify_script)],
                capture_output=True,
                text=True,
                cwd=str(task_dir),
                timeout=60
            )

            return {
                "passed": result.returncode == 0,
                "output": result.stdout,
                "error": result.stderr
            }
        except Exception as e:
            return {"passed": False, "error": str(e)}

    def _reset_task(self, task_id: str):
        """Reset task to initial state from init directory"""
        task_dir = self.tasks_dir / task_id
        setup_dir = task_dir / "setup"
        init_dir = task_dir / "init"

        # If init directory exists, copy files to setup
        if init_dir.exists():
            import shutil
            # Remove all files in setup (except directories)
            for item in setup_dir.iterdir():
                if item.is_file():
                    item.unlink()
                elif item.is_dir():
                    shutil.rmtree(item)

            # Copy init files to setup
            for item in init_dir.iterdir():
                if item.is_file():
                    shutil.copy2(item, setup_dir / item.name)
                elif item.is_dir():
                    shutil.copytree(item, setup_dir / item.name)
        else:
            # Fallback: just remove generated files
            patterns = ["*.test", "basic", "asm", "assembler", "counter"]
            for pattern in patterns:
                for f in setup_dir.glob(pattern):
                    if f.is_file():
                        f.unlink()

    def run_single_agent(self, agent_name: str, model: str = None, task_ids: list[str] = None):
        """Run a single agent on all tasks"""
        tasks = task_ids or self._get_tasks()
        results = {
            "agent": agent_name,
            "model": model,
            "timestamp": datetime.now().isoformat(),
            "tasks": {}
        }

        print(f"\n{Colors.BOLD}Running Agent: {agent_name}{Colors.END}")
        if model:
            print(f"Model: {model}")
        print(f"Tasks: {len(tasks)}")
        print("=" * 50)

        passed = 0
        failed = 0

        for task_id in tasks:
            print(f"\n[{task_id}] Running...", end=" ", flush=True)

            # Reset task
            self._reset_task(task_id)

            # Get prompt and run agent
            prompt = self._get_task_prompt(task_id)
            agent_result = self._run_agent(agent_name, prompt, model)

            # Verify
            verify_result = self._verify_task(task_id)

            # Record result
            results["tasks"][task_id] = {
                "agent_success": agent_result.get("success", False),
                "verify_passed": verify_result["passed"],
                "duration": agent_result.get("duration", 0),
                "output": verify_result.get("output", "")[:500]
            }

            if verify_result["passed"]:
                print(f"{Colors.GREEN}PASSED{Colors.END} ({agent_result.get('duration', 0):.1f}s)")
                passed += 1
            else:
                print(f"{Colors.RED}FAILED{Colors.END}")
                failed += 1

        results["summary"] = {
            "total": len(tasks),
            "passed": passed,
            "failed": failed,
            "pass_rate": passed / len(tasks) * 100 if tasks else 0
        }

        # Save results
        result_file = self.results_dir / f"{agent_name}_{model or 'default'}_{datetime.now().strftime('%Y%m%d_%H%M%S')}.json"
        with open(result_file, "w") as f:
            json.dump(results, f, indent=2)

        print(f"\n{Colors.BOLD}Summary:{Colors.END}")
        print(f"  Passed: {Colors.GREEN}{passed}{Colors.END}")
        print(f"  Failed: {Colors.RED}{failed}{Colors.END}")
        print(f"  Rate:   {results['summary']['pass_rate']:.1f}%")
        print(f"\nResults saved to: {result_file}")

        return results

    def compare_agents(self, agent_names: list[str], task_ids: list[str] = None):
        """Compare multiple agents"""
        all_results = {}

        for agent_name in agent_names:
            print(f"\n{'='*60}")
            print(f"Testing Agent: {agent_name}")
            print(f"{'='*60}")

            results = self.run_single_agent(agent_name, task_ids=task_ids)
            all_results[agent_name] = results

        # Print comparison table
        self._print_comparison(all_results)

        # Save comparison
        comparison_file = self.results_dir / f"comparison_{datetime.now().strftime('%Y%m%d_%H%M%S')}.json"
        with open(comparison_file, "w") as f:
            json.dump(all_results, f, indent=2)

        return all_results

    def _print_comparison(self, all_results: dict):
        """Print comparison table"""
        tasks = self._get_tasks()
        agents = list(all_results.keys())

        print(f"\n{Colors.BOLD}Comparison Results:{Colors.END}")
        print("=" * 80)

        # Header
        header = f"{'Task':<25}"
        for agent in agents:
            header += f" {agent:>15}"
        print(header)
        print("-" * 80)

        # Task rows
        for task_id in tasks:
            row = f"{task_id:<25}"
            for agent in agents:
                task_result = all_results[agent]["tasks"].get(task_id, {})
                if task_result.get("verify_passed"):
                    row += f" {Colors.GREEN}✓ PASS{Colors.END}        "
                else:
                    row += f" {Colors.RED}✗ FAIL{Colors.END}        "
            print(row)

        # Summary row
        print("-" * 80)
        summary_row = f"{'PASS RATE':<25}"
        for agent in agents:
            rate = all_results[agent]["summary"]["pass_rate"]
            color = Colors.GREEN if rate >= 70 else (Colors.YELLOW if rate >= 50 else Colors.RED)
            summary_row += f" {color}{rate:>13.1f}%{Colors.END}  "
        print(summary_row)

    def generate_report(self):
        """Generate report from all result files"""
        result_files = list(self.results_dir.glob("*.json"))

        if not result_files:
            print("No result files found. Run some tests first.")
            return

        print(f"\n{Colors.BOLD}Historical Results:{Colors.END}")
        print("=" * 80)

        # Load and aggregate results
        all_results = []
        for rf in sorted(result_files):
            with open(rf) as f:
                data = json.load(f)
                data["file"] = rf.name
                all_results.append(data)

        # Print table
        print(f"{'File':<40} {'Agent':<15} {'Rate':>8} {'Date':>12}")
        print("-" * 80)

        for r in all_results:
            summary = r.get("summary", {})
            file_name = r.get("file", "")[:38]
            agent = r.get("agent", "unknown")[:13]
            rate = summary.get("pass_rate", 0)
            timestamp = r.get("timestamp", "")[:10]

            color = Colors.GREEN if rate >= 70 else (Colors.YELLOW if rate >= 50 else Colors.RED)
            print(f"{file_name:<40} {agent:<15} {color}{rate:>6.1f}%{Colors.END} {timestamp:>12}")

    def list_available(self):
        """List available agents and models"""
        print(f"\n{Colors.BOLD}Available Agents:{Colors.END}")
        for name, cfg in self.config.get("agents", {}).items():
            print(f"  - {name}: {cfg.get('name', name)}")

        print(f"\n{Colors.BOLD}Available Models:{Colors.END}")
        for name, cfg in self.config.get("models", {}).items():
            print(f"  - {name}: {cfg.get('name', name)}")

        print(f"\n{Colors.BOLD}Available Tasks:{Colors.END}")
        for task in self._get_tasks():
            print(f"  - {task}")


def main():
    parser = argparse.ArgumentParser(description="A/B Testing Framework for AI Coding Agents")
    parser.add_argument("--agent", "-a", help="Agent to test")
    parser.add_argument("--model", "-m", help="Model to use (if agent supports it)")
    parser.add_argument("--task", "-t", help="Specific task to run (can be repeated)", action="append")
    parser.add_argument("--compare", "-c", nargs="+", help="Compare multiple agents")
    parser.add_argument("--benchmark", "-b", action="store_true", help="Run all configured agents")
    parser.add_argument("--report", "-r", action="store_true", help="Generate report from all results")
    parser.add_argument("--list", "-l", action="store_true", help="List available agents, models, and tasks")

    args = parser.parse_args()

    benchmark_dir = Path(__file__).parent
    framework = ABTestFramework(benchmark_dir)

    if args.list:
        framework.list_available()
    elif args.report:
        framework.generate_report()
    elif args.compare:
        framework.compare_agents(args.compare, args.task)
    elif args.agent:
        framework.run_single_agent(args.agent, args.model, args.task)
    elif args.benchmark:
        # Run all configured agents
        agents = list(framework.config.get("agents", {}).keys())
        if agents:
            framework.compare_agents(agents)
        else:
            print("No agents configured in agents.yaml")
    else:
        parser.print_help()


if __name__ == "__main__":
    main()
