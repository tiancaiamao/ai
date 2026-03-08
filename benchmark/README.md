# Agent Benchmark

A lightweight benchmark framework for testing and comparing AI coding agents.

## Features

- **No Docker required** - Runs locally
- **A/B testing** - Compare different agents and models
- **Regression detection** - Catch when changes break things
- **Easy to extend** - Add new tasks in minutes

## Quick Start

```bash
# List all tasks
make list

# Run verification only (no agent)
make run

# Run with your agent
make agent-task TASK=001_fix_off_by_one

# Save baseline
make baseline

# Compare with baseline
make compare
```

## A/B Testing

### Compare Different Agents

```bash
# List available agents
make ab-list

# Run single agent
make ab-test AGENT=my-agent

# Compare multiple agents
python3 ab_test.py --compare my-agent claude-code

# Run all configured agents
python3 ab_test.py --benchmark
```

### Compare Different Models

```bash
# Test with specific model
python3 ab_test.py --agent my-agent --model glm5
python3 ab_test.py --agent my-agent --model deepseek
```

### View Reports

```bash
# Generate report from all results
make ab-report

# Or
python3 ab_test.py --report
```

## Available Agents

| Agent | Description |
|-------|-------------|
| my-agent | Your custom agent (uses `ai` CLI) |
| claude-code | Claude Code CLI |
| codex | OpenAI Codex CLI |
| gemini | Gemini CLI |

## Available Models

| Model | Description |
|-------|-------------|
| glm5 | GLM-5 |
| minimax | MiniMax 2.5 |
| deepseek | DeepSeek V3 |
| gpt4 | GPT-4 |
| claude | Claude Sonnet |

## Tasks

| Task | Difficulty | Description |
|------|------------|-------------|
| 001_fix_off_by_one | Easy | Fix off-by-one error in loop |
| 002_add_error_handling | Easy | Add error handling to functions |
| 003_refactor_duplicated_code | Medium | Refactor duplicate code |
| 010_basic_interpreter | Hard | Implement BASIC interpreter |
| 011_unittest_http_parser | Medium | Write unit tests |
| 012_mos6502_assembler | Hard | Implement MOS6502 assembler |
| 013_fix_concurrent_bug | Easy | Fix race condition |

## Directory Structure

```
benchmark/
├── run.sh              # Main runner script
├── runner/
│   └── runner.go       # Go implementation
├── tasks/
│   ├── 001_fix_off_by_one/
│   │   ├── setup/      # Initial code
│   │   ├── task.md     # Task description
│   │   └── verify.sh   # Verification script
│   └── ...
└── results/
    ├── baseline.json   # Baseline results
    └── current.json    # Latest results
```

## Adding New Tasks

1. Create a new directory under `tasks/`:
   ```bash
   mkdir -p tasks/004_your_task/setup
   ```

2. Create `task.md` with the task description:
   ```markdown
   # Task: Your Task Name

   ## Description
   What the agent needs to do...

   ## Requirements
   - Requirement 1
   - Requirement 2
   ```

3. Create `setup/` with initial code files

4. Create `verify.sh` to check the result:
   ```bash
   #!/bin/bash
   cd "$(dirname "$0")/setup"

   # Your verification logic
   if [ condition ]; then
       echo "PASS: Description"
   else
       echo "FAIL: Description"
       exit 1
   fi
   ```

## Running with Your Agent

```bash
# Set your agent command
export AGENT_CMD="ai --mode rpc"

# Run benchmark with agent
./run.sh agent
```

## Output Format

Results are saved as JSON:

```json
{
  "timestamp": "2025-01-15T10:30:00Z",
  "agent_name": "my-agent",
  "git_commit": "abc123",
  "total_tasks": 3,
  "passed_tasks": 2,
  "failed_tasks": 1,
  "pass_rate": 66.67,
  "results": [...]
}
```

## Example Workflow

### A/B Testing

```bash
# 1. Run and save baseline
./run.sh run
./run.sh baseline

# 2. Make changes to your agent code
git checkout -b feature/new-capability
# ... make changes ...

# 3. Run again
./run.sh run

# 4. Compare
./run.sh compare
```

### CI Integration

```yaml
# .github/workflows/benchmark.yml
name: Benchmark

on: [push, pull_request]

jobs:
  benchmark:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Run benchmark
        run: |
          cd benchmark
          ./run.sh run

      - name: Compare with baseline
        run: |
          cd benchmark
          ./run.sh compare
```

## Task Types

### Bug Fix Tasks
- Fix off-by-one errors
- Handle edge cases
- Fix null pointer dereferences

### Feature Tasks
- Add new functions
- Implement interfaces
- Add validation

### Refactoring Tasks
- Reduce code duplication
- Improve naming
- Extract functions

### Performance Tasks
- Optimize algorithms
- Reduce allocations
- Improve concurrency

## Tips

1. **Keep tasks small** - Each task should take < 5 minutes
2. **Clear verification** - verify.sh should be deterministic
3. **Isolated setup** - Each task's setup/ should be independent
4. **Meaningful descriptions** - task.md should be clear and specific
