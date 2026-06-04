#!/usr/bin/env python3
"""
Trajectory Normalizer

Converts benchmark trajectory format to OpenAI messages format,
making it compatible with the agent debugger.

Input format (from benchmark results JSON):
{
  "task_id": "agent_001_forced_exploration",
  "trajectory": [
    {
      "tool": "bash",
      "args": {"command": "ls -la"},
      "output": "total 64...",
      "duration": 0.0,
      "thinking": "Let me understand..."
    }
  ]
}

Output format (OpenAI messages):
{
  "trace_id": "agent_001_forced_exploration-rollout-0",
  "task_id": "agent_001_forced_exploration",
  "rollout_index": 0,
  "passed": false,
  "verifier_output": "ERROR: ...",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."},
    {"role": "assistant", "content": [...], "tool_calls": [...]},
    {"role": "tool", "content": "...", "name": "bash"}
  ]
}
"""

import json
import os
import sys
from datetime import datetime
from pathlib import Path
from typing import Any, Dict, List, Optional


def normalize_trajectory(
    raw_trajectory: List[Dict[str, Any]],
    task_id: str,
    rollout_index: int,
    passed: bool,
    verifier_output: str = "",
    system_prompt: str = "You are a coding agent...",
    user_prompt: str = ""
) -> Dict[str, Any]:
    """
    Normalize a single trajectory to OpenAI messages format.

    Args:
        raw_trajectory: List of trajectory events from benchmark
        task_id: Task identifier
        rollout_index: Rollout index (0-based)
        passed: Whether this rollout passed
        verifier_output: Verifier output (test failure message)
        system_prompt: System prompt used by agent
        user_prompt: Original task description (if available)

    Returns:
        Normalized trace dict with OpenAI messages format
    """
    messages: List[Dict[str, Any]] = []

    # 1. System message (synthesized)
    messages.append({
        "role": "system",
        "content": system_prompt
    })

    # 2. User message (task description)
    if user_prompt:
        messages.append({
            "role": "user",
            "content": user_prompt,
            "metadata": {
                "kind": "user",
                "timestamp": 0
            }
        })

        # 3. Process trajectory events
    tool_call_counter = 0

    for i, event in enumerate(raw_trajectory):
        tool = event.get("tool", "")
        # Support both string (legacy) and dict (from agent_output) args
        args_raw = event.get("args", event.get("args_summary", "{}") or "{}")
        if isinstance(args_raw, dict):
            args = args_raw
        else:
            try:
                args = json.loads(args_raw)
            except (json.JSONDecodeError, TypeError):
                args = {}
        # Check both "output" (from agent_output) and "result_summary" (legacy)
        output = event.get("output", event.get("result_summary", ""))
        duration = event.get("duration", 0.0)
        thinking = event.get("thinking", "")

        # Extract assistant content (thinking + maybe text)
        assistant_content: List[Dict[str, str]] = []

        if thinking:
            assistant_content.append({
                "type": "thinking",
                "text": thinking
            })

        # Add tool call
        if tool:
            tool_call_counter += 1
            tool_call_id = f"call_{tool_call_counter}"

            tool_calls = [{
                "id": tool_call_id,
                "type": "function",
                "function": {
                    "name": tool,
                    "arguments": json.dumps(args, ensure_ascii=False)
                }
            }]

            messages.append({
                "role": "assistant",
                "content": assistant_content if assistant_content else [{}],
                "tool_calls": tool_calls,
                "metadata": {
                    "kind": "assistant",
                    "timestamp": i,
                    "duration": duration
                }
            })

            # Add tool result with smart truncation
            content_str = str(output)
            # Smart truncation: preserve key information
            if len(content_str) > 500:
                content_lower = content_str.lower()
                # Preserve error messages, file paths, code comments
                if any(e in content_lower for e in ['error', 'exception', 'failed', 'traceback', 'abort']):
                    pass  # Keep full error output
                elif any(c in content_str for c in ['BUG:', 'FIXME:', 'WARNING:', 'TODO:']):
                    # Keep the line with the comment + context
                    lines = content_str.split('\n')
                    for i, line in enumerate(lines):
                        if any(c in line for c in ['BUG:', 'FIXME:', 'WARNING:', 'TODO:']):
                            start = max(0, i-1)
                            end = min(len(lines), i+3)
                            content_str = '\n'.join(lines[start:end])
                            break
                    if len(content_str) > 500:
                        content_str = content_str[:500]
                else:
                    # Truncate to 500 characters for normal output
                    content_str = content_str[:500]
            
            messages.append({
                "role": "tool",
                "tool_call_id": tool_call_id,
                "content": content_str,
                "name": tool,
                "metadata": {
                    "kind": "tool_result",
                    "timestamp": i,
                    "duration": duration
                }
            })

    return {
        "trace_id": f"{task_id}-rollout-{rollout_index}",
        "task_id": task_id,
        "rollout_index": rollout_index,
        "passed": passed,
        "verifier_output": verifier_output,
        "messages": messages
    }


def extract_user_prompt_from_agent_output(agent_output: str) -> str:
    """
    Extract user prompt text from agent_output JSONL stream.

    Looks for message_start events where message.role == "user" and
    extracts the text content.

    Args:
        agent_output: JSONL-formatted agent event stream

    Returns:
        Extracted user prompt text, or empty string if not found
    """
    for line in agent_output.split('\n'):
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except (json.JSONDecodeError, TypeError):
            continue
        if event.get("type") == "message_start":
            msg = event.get("message", {})
            if msg.get("role") == "user":
                content_parts = msg.get("content", [])
                for part in content_parts:
                    if part.get("type") == "text":
                        return part.get("text", "")
    return ""


def parse_trajectory_from_agent_output(agent_output: str) -> List[Dict[str, Any]]:
    """
    Parse trajectory events from agent_output JSONL stream.

    Matches tool_execution_start + tool_execution_end pairs by toolCallId,
    and collects thinking text from thinking_delta events.

    Args:
        agent_output: JSONL-formatted agent event stream

    Returns:
        List of trajectory dicts with keys: tool, args, output, thinking
    """
    # First pass: collect all events indexed by type
    tool_starts: Dict[str, Dict] = {}  # toolCallId -> event
    tool_ends: Dict[str, Dict] = {}    # toolCallId -> event
    thinking_chunks: List[str] = []
    # Track current thinking group (between tool calls)
    thinking_groups: List[str] = []  # one per tool call group
    current_thinking: List[str] = []
    seen_tool_start = False

    for line in agent_output.split('\n'):
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except (json.JSONDecodeError, TypeError):
            continue

        event_type = event.get("type", "")

        if event_type == "tool_execution_start":
            if not seen_tool_start and current_thinking:
                thinking_groups.append("".join(current_thinking))
            seen_tool_start = True
            tool_call_id = event.get("toolCallId", "")
            tool_starts[tool_call_id] = event

        elif event_type == "tool_execution_end":
            tool_call_id = event.get("toolCallId", "")
            tool_ends[tool_call_id] = event
            # Reset thinking accumulator for next group
            current_thinking = []
            seen_tool_start = False

        elif event_type == "message_update":
            ame = event.get("assistantMessageEvent", {})
            if ame.get("type") == "thinking_delta":
                current_thinking.append(ame.get("delta", ""))

    # Any remaining thinking after last tool
    if current_thinking:
        thinking_groups.append("".join(current_thinking))

    # Second pass: build trajectory by matching start/end pairs
    # Process in order of appearance by iterating tool_starts keys
    # We need to preserve order, so re-scan for ordering
    ordered_ids: List[str] = []
    for line in agent_output.split('\n'):
        line = line.strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except (json.JSONDecodeError, TypeError):
            continue
        if event.get("type") == "tool_execution_start":
            tid = event.get("toolCallId", "")
            if tid and tid not in ordered_ids:
                ordered_ids.append(tid)

    trajectory: List[Dict[str, Any]] = []
    for idx, tool_call_id in enumerate(ordered_ids):
        start = tool_starts.get(tool_call_id, {})
        end = tool_ends.get(tool_call_id, {})

        tool_name = start.get("toolName", "")
        args = start.get("args", {})

        # Extract output text from tool_execution_end result
        output = ""
        result = end.get("result", {})
        content_parts = result.get("content", [])
        if content_parts:
            # Concatenate all text parts
            texts = []
            for part in content_parts:
                if part.get("type") == "text":
                    texts.append(part.get("text", ""))
            output = "\n".join(texts)

        # Get thinking text for this tool call
        thinking = thinking_groups[idx] if idx < len(thinking_groups) else ""

        trajectory.append({
            "tool": tool_name,
            "args": args,
            "output": output,
            "thinking": thinking
        })

    return trajectory


def extract_task_prompt(result_data: Dict[str, Any], task_id: str) -> str:
    """
    Extract the original task prompt from result data.

    Tries to find the task description from the first user message
    or from task metadata.
    """
    # Try to get from task results
    for task in result_data.get("results", []):
        if task.get("task_id") == task_id:
            # Look for trajectory with user message
            trajectory = task.get("trajectory", [])
            if trajectory:
                # The first event is often the task setup
                # Try to extract from output or args
                first_event = trajectory[0]
                if "task" in first_event.get("args", {}):
                    return str(first_event["args"]["task"])

                # Fallback: try to get from output
                output = first_event.get("output", "")
                if output and len(output) < 1000:  # Reasonable length
                    return output

    return ""


def normalize_all_trajectories(
    result_json_path: str,
    output_dir: str,
    only_failed: bool = True
) -> List[str]:
    """
    Normalize all trajectories from a benchmark result JSON file.

    Supports k-rollout results:
    - If 'per_task_rollout_details' is present (k>1), extracts all k rollouts
      per task with full trajectories.
    - Falls back to 'results' array for k=1 or legacy format.

    Args:
        result_json_path: Path to benchmark result JSON
        output_dir: Directory to save normalized traces
        only_failed: Only normalize failed tasks (default: True)

    Returns:
        List of output file paths
    """
    with open(result_json_path, 'r') as f:
        data = json.load(f)

    output_dir = Path(output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    output_files = []
    k = data.get("k", 1)
    per_task_details = data.get("per_task_rollout_details")

    if per_task_details and k > 1:
        # k-rollout format: extract all rollouts from per_task_rollout_details
        output_files = _normalize_from_rollout_details(
            data, per_task_details, output_dir, only_failed
        )
    else:
        # Legacy format (k=1): one trajectory per task from results[]
        output_files = _normalize_from_results(
            data, output_dir, only_failed
        )

    return output_files


def _normalize_from_rollout_details(
    data: Dict[str, Any],
    per_task_details: Dict[str, Any],
    output_dir: Path,
    only_failed: bool
) -> List[str]:
    """Normalize all k rollouts from per_task_rollout_details."""
    output_files = []

    for task_id, rollout_result in per_task_details.items():
        rollouts = rollout_result.get("rollouts", [])
        if not rollouts:
            continue

        for rollout_idx, rollout in enumerate(rollouts):
            passed = rollout.get("passed", False)

            # Skip passed rollouts if only_failed=True
            if only_failed and passed:
                continue

            trajectory = rollout.get("trajectory", [])
            if not trajectory:
                continue

            verifier_output = rollout.get("error", "")
            system_prompt = "You are a coding agent..."
            user_prompt = _extract_task_prompt_from_rollout(rollout, data, task_id)

            normalized = normalize_trajectory(
                raw_trajectory=trajectory,
                task_id=task_id,
                rollout_index=rollout_idx,
                passed=passed,
                verifier_output=verifier_output,
                system_prompt=system_prompt,
                user_prompt=user_prompt
            )

            # Save to file: {task_id}_rollout_{i}.normalized.json
            safe_task_id = task_id.replace('/', '_')
            output_file = output_dir / f"{safe_task_id}_rollout_{rollout_idx}.normalized.json"
            with open(output_file, 'w') as f:
                json.dump(normalized, f, indent=2, ensure_ascii=False)

            output_files.append(str(output_file))
            status = "PASS" if passed else "FAIL"
            print(f"Normalized: {task_id} rollout {rollout_idx} ({status}) -> {output_file}")

    return output_files


def _normalize_from_results(
    data: Dict[str, Any],
    output_dir: Path,
    only_failed: bool
) -> List[str]:
    """Normalize trajectories from the legacy results[] array (k=1)."""
    output_files = []

    for task in data.get("results", []):
        task_id = task.get("task_id")
        passed = task.get("passed", False)

        if only_failed and passed:
            continue

        # Prefer trajectory field if present (future compatibility)
        trajectory = task.get("trajectory", [])
        user_prompt = extract_task_prompt(data, task_id)

        # Fallback: parse trajectory from agent_output JSONL stream
        agent_output = task.get("agent_output", "")
        if not trajectory and agent_output:
            trajectory = parse_trajectory_from_agent_output(agent_output)
            # Also try to extract user_prompt from agent_output
            if not user_prompt:
                user_prompt = extract_user_prompt_from_agent_output(agent_output)

        if not trajectory:
            continue

        verifier_output = task.get("error", "")
        system_prompt = "You are a coding agent..."

        normalized = normalize_trajectory(
            raw_trajectory=trajectory,
            task_id=task_id,
            rollout_index=0,
            passed=passed,
            verifier_output=verifier_output,
            system_prompt=system_prompt,
            user_prompt=user_prompt
        )

        safe_trace_id = normalized['trace_id'].replace('/', '_')
        output_file = output_dir / f"{safe_trace_id}.normalized.json"
        with open(output_file, 'w') as f:
            json.dump(normalized, f, indent=2, ensure_ascii=False)

        output_files.append(str(output_file))
        print(f"Normalized: {task_id} -> {output_file}")

    return output_files


def _extract_task_prompt_from_rollout(
    rollout: Dict[str, Any],
    data: Dict[str, Any],
    task_id: str
) -> str:
    """
    Extract the original task prompt from a rollout's trajectory.

    Tries the rollout's own trajectory first, then falls back to the
    main results array.
    """
    trajectory = rollout.get("trajectory", [])
    if trajectory:
        first_event = trajectory[0]
        if "task" in first_event.get("args", {}):
            return str(first_event["args"]["task"])
        output = first_event.get("output", "")
        if output and len(output) < 1000:
            return output

    # Fall back to main results
    return extract_task_prompt(data, task_id)


def main():
    import argparse

    parser = argparse.ArgumentParser(
        description="Normalize benchmark trajectories to OpenAI messages format"
    )
    parser.add_argument(
        "--input",
        required=True,
        help="Path to benchmark result JSON file"
    )
    parser.add_argument(
        "--output-dir",
        required=True,
        help="Directory to save normalized traces"
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="Normalize all tasks (not just failed ones)"
    )

    args = parser.parse_args()

    output_files = normalize_all_trajectories(
        result_json_path=args.input,
        output_dir=args.output_dir,
        only_failed=not args.all
    )

    print(f"\nNormalized {len(output_files)} trajectories to {args.output_dir}")


if __name__ == "__main__":
    main()