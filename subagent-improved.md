---
name: subagent
description: Spawn isolated subagent processes for delegated tasks. Use for parallel execution, focused tasks, or breaking down complex problems.
tools: [bash]
---

# Subagent Tool

Spawn a subagent to handle delegated tasks. The subagent runs in an **isolated process** using headless mode with a clean session and **focused system prompt**.

## When to Use

- **Parallel execution** of independent tasks
- **Complex problems** that need focused attention
- **Breaking down** large tasks into sub-tasks
- **Tasks requiring isolation** from main conversation context

## Command-Line Options

| Option | Description | Default |
|--------|-------------|---------|
| `--mode headless` | Run in headless mode (required) | - |
| `--no-session` | Don't persist session to disk (required) | false |
| `--subagent` | Use focused subagent system prompt (recommended) | false |
| `--max-turns N` | Maximum conversation turns | 0 (unlimited) |
| `--tools T1,T2` | Comma-separated tool whitelist | all tools |

## Max-Turns Guidelines

Choose `--max-turns` based on task complexity:

| Task Complexity | Recommended Max-Turns | Examples |
|-----------------|----------------------|----------|
| **Simple** (1-2 tool calls) | 5 | Read file, grep pattern |
| **Medium** (3-6 tool calls) | 8-10 | Analyze multiple files, simple refactoring |
| **Complex** (7-12 tool calls) | 15-20 | Feature implementation, test writing |
| **Very Complex** (unbounded) | 0 (unlimited) | Full feature development |
| **Exploratory** | 20+ | Codebase analysis, architecture review |

**Rule of thumb:** Estimate tool calls needed, add 2-3 buffer turns for LLM reasoning.

## Output Format

Single JSON line:

```json
{
  "text": "Task result or explanation...",
  "usage": {
    "input_tokens": 7611,
    "output_tokens": 1115,
    "total_tokens": 8726
  },
  "exit_code": 0
}
```

### Exit Codes

| Code | Meaning | Action |
|------|---------|--------|
| 0 | Success | Use result |
| 1 | Task failure / error | Check `text` field for details |
| 130 | SIGINT (Ctrl+C) | Task was interrupted |

## Usage Patterns

### Single Task

```bash
# Simple read-only task
ai --mode headless --no-session --subagent --max-turns 5 \
  "List all Go files in pkg/"

# Output: JSON with "text" field containing the result
```

### Extract Result with jq

```bash
# Get just the text output
RESULT=$(ai --mode headless --no-session --subagent --max-turns 5 "List Go files" | jq -r '.text')

# Get both result and exit code
OUTPUT=$(ai --mode headless --no-session --subagent --max-turns 5 "Analyze code")
EXIT_CODE=$(echo "$OUTPUT" | jq -r '.exit_code')
TEXT=$(echo "$OUTPUT" | jq -r '.text')

if [ "$EXIT_CODE" -eq 0 ]; then
  echo "Success: $TEXT"
else
  echo "Failed: $TEXT" >&2
fi
```

### Parallel Tasks

Execute multiple independent tasks concurrently:

```bash
# Method 1: Background jobs with wait
ai --mode headless --no-session --subagent "Task 1" > /tmp/task1.out &
PID1=$!
ai --mode headless --no-session --subagent "Task 2" > /tmp/task2.out &
PID2=$!
ai --mode headless --no-session --subagent "Task 3" > /tmp/task3.out &
PID3=$!

wait $PID1 $PID2 $PID3

# Collect results
RESULT1=$(jq -r '.text' < /tmp/task1.out)
RESULT2=$(jq -r '.text' < /tmp/task2.out)
RESULT3=$(jq -r '.text' < /tmp/task3.out)
```

```bash
# Method 2: Use parallel (if available)
parallel -j 3 \
  'ai --mode headless --no-session --subagent --max-turns 10 "{}" > /tmp/{}.out' \
  ::: "Task 1" "Task 2" "Task 3"
```

### With Tool Restrictions

```bash
# Explorer profile: read-only analysis
ai --mode headless --no-session --subagent --tools read,grep --max-turns 8 \
  "Find all API endpoints in src/api/"

# Builder profile: implementation with tests
ai --mode headless --no-session --subagent --tools read,write,edit,bash --max-turns 15 \
  "Implement user registration with unit tests"

# Tester profile: run and diagnose tests
ai --mode headless --no-session --subagent --tools read,bash,grep --max-turns 10 \
  "Run go test ./pkg/auth and diagnose failures"
```

## Agent Profiles

For specialized tasks, use tool restrictions to create focused agent profiles:

| Profile | Tools | Purpose | Max-Turns |
|---------|-------|---------|-----------|
| **Explorer** | `read,grep` | Read-only analysis, code exploration | 5-10 |
| **Reviewer** | `read,grep` | Code review, validation | 8-12 |
| **Builder** | `read,write,edit,bash` | Implementation, file changes | 15-20 |
| **Tester** | `read,bash,grep` | Test execution, debugging | 10-15 |
| **General** | (all) | Full capability tasks | 20+ |

### Profile Examples

```bash
# Explorer: Fast code analysis
ai --mode headless --no-session --subagent --tools read,grep --max-turns 5 \
  "List all exported functions in pkg/rpc/"

# Reviewer: Security-focused review
ai --mode headless --no-session --subagent --tools read,grep --max-turns 8 \
  "Review auth.go for SQL injection vulnerabilities"

# Builder: Feature with testing
ai --mode headless --no-session --subagent --tools read,write,edit,bash --max-turns 15 \
  "Implement user registration with tests"

# Tester: Diagnose test failures
ai --mode headless --no-session --subagent --tools read,bash,grep --max-turns 10 \
  "Run go test ./auth and report all failures with details"
```

### Profile Selection Guide

| Task Type | Use Profile | Why |
|-----------|-------------|-----|
| Code analysis / architecture review | Explorer | No writes needed, fastest execution |
| Security / style review | Reviewer | Read-only prevents accidental changes |
| Feature implementation | Builder | Needs write access + bash for tests |
| Test triage / debugging | Tester | Needs to run tests but not modify code |
| Unknown / complex | General | Full tool access for flexibility |

## Real-World Workflows

### Workflow 1: Parallel Codebase Analysis

```bash
#!/bin/bash
# Analyze different parts of codebase in parallel

TASKS=(
  "Analyze pkg/rpc for API surface"
  "Review pkg/agent for architecture patterns"
  "Check cmd/ai for command handlers"
)

for i in "${!TASKS[@]}"; do
  TASK="${TASKS[$i]}"
  ai --mode headless --no-session --subagent --tools read,grep --max-turns 8 \
    "$TASK" > "/tmp/analysis_$i.out" &
done

wait

# Combine results
for i in "${!TASKS[@]}"; do
  echo "=== Task: ${TASKS[$i]} ==="
  jq -r '.text' < "/tmp/analysis_$i.out"
  echo
done
```

### Workflow 2: Parallel Test Runner

```bash
#!/bin/bash
# Run tests for different packages in parallel

PACKAGES=(
  "./pkg/agent"
  "./pkg/rpc"
  "./pkg/session"
)

FAILED=()

for pkg in "${PACKAGES[@]}"; do
  OUTPUT=$(ai --mode headless --no-session --subagent --tools read,bash,grep --max-turns 10 \
    "Run 'go test $pkg' and report all failures")
  EXIT_CODE=$(echo "$OUTPUT" | jq -r '.exit_code')

  if [ "$EXIT_CODE" -ne 0 ]; then
    FAILED+=("$pkg")
    echo "❌ $pkg failed" >&2
    jq -r '.text' <<< "$OUTPUT" >&2
  else
    echo "✅ $pkg passed"
  fi
done

if [ ${#FAILED[@]} -gt 0 ]; then
  echo ""
  echo "Failed packages: ${FAILED[*]}" >&2
  exit 1
fi
```

### Workflow 3: Code Review Pipeline

```bash
#!/bin/bash
# Multi-stage code review pipeline

FILE="$1"

# Stage 1: Style check
STYLE_OUTPUT=$(ai --mode headless --no-session --subagent --tools read,grep --max-turns 5 \
  "Check $FILE for Go style issues and report violations")
STYLE_CODE=$(echo "$STYLE_OUTPUT" | jq -r '.exit_code')

# Stage 2: Security check
SEC_OUTPUT=$(ai --mode headless --no-session --subagent --tools read,grep --max-turns 8 \
  "Review $FILE for security vulnerabilities")
SEC_CODE=$(echo "$SEC_OUTPUT" | jq -r '.exit_code')

# Stage 3: Architecture check (parallel with security)
ARCH_OUTPUT=$(ai --mode headless --no-session --subagent --tools read,grep --max-turns 8 \
  "Check $FILE for architectural issues and coupling")
ARCH_CODE=$(echo "$ARCH_OUTPUT" | jq -r '.exit_code')

# Report
echo "=== Style Check ==="
[ "$STYLE_CODE" -eq 0 ] && echo "✅ Pass" || (jq -r '.text' <<< "$STYLE_OUTPUT" && echo "❌ Fail")

echo ""
echo "=== Security Check ==="
[ "$SEC_CODE" -eq 0 ] && echo "✅ Pass" || (jq -r '.text' <<< "$SEC_OUTPUT" && echo "❌ Fail")

echo ""
echo "=== Architecture Check ==="
[ "$ARCH_CODE" -eq 0 ] && echo "✅ Pass" || (jq -r '.text' <<< "$ARCH_OUTPUT" && echo "❌ Fail")

# Overall exit code
[ "$STYLE_CODE" -eq 0 ] && [ "$SEC_CODE" -eq 0 ] && [ "$ARCH_CODE" -eq 0 ] || exit 1
```

## Debugging Tips

### Enable Verbose Output

```bash
# Run with debug output to see what's happening
ai --mode headless --no-session --subagent --debug "Task description"
```

### Check JSON Output

```bash
# Pretty-print the output for inspection
ai --mode headless --no-session --subagent "Task" | jq '.'
```

### Test a Simple Task First

```bash
# Verify subagent is working with a simple task
ai --mode headless --no-session --subagent --max-turns 2 "Read /tmp/test.txt"
```

### Common Issues and Solutions

| Issue | Cause | Solution |
|-------|-------|----------|
| Exit code 1 with no output | Task too complex for max-turns | Increase `--max-turns` |
| Empty text field | LLM couldn't complete | Increase `--max-turns` or simplify task |
| Exit code 130 | Process interrupted | Check if task was killed externally |
| Non-JSON output | Tool error or crash | Check logs, try simpler task |
| Timeout | Task taking too long | Break into smaller subtasks, run in background |

### Timeout Handling

For tasks that may take a long time:

```bash
# Run with timeout
timeout 60s ai --mode headless --no-session --subagent "Long task" > /tmp/out.json
TIMEOUT_CODE=$?

if [ "$TIMEOUT_CODE" -eq 124 ]; then
  echo "Task timed out after 60 seconds" >&2
  exit 1
fi

# Check actual task exit code
TASK_CODE=$(jq -r '.exit_code' < /tmp/out.json)
```

### Background Long-Running Tasks

```bash
# Run in background with output file
ai --mode headless --no-session --subagent --max-turns 0 "Complex task" > /tmp/task.json &
BG_PID=$!

# Check progress later
jobs -l $BG_PID

# Wait for completion
wait $BG_PID
jq -r '.text' < /tmp/task.json
```

## Performance Tips

1. **Tool whitelist speeds up execution** - Restrict tools when possible
2. **Lower max-turns for simple tasks** - Reduces unnecessary LLM calls
3. **Parallel independent tasks** - Use `&` and `wait` for speed
4. **Reuse sessions for related tasks** - Don't use `--no-session` if context matters
5. **Cache results** - Save parsed output to files if reused

## Best Practices

- ✅ Always use `--no-session` to avoid polluting session files
- ✅ Always use `--subagent` for focused, concise output
- ✅ Set `--max-turns` based on task complexity
- ✅ Use `--tools` to restrict capabilities when possible
- ✅ Parse JSON output with `jq` for reliability
- ✅ Check exit_code before using results
- ✅ Run independent tasks in parallel
- ❌ Don't use for simple single-file reads (use `read` tool)
- ❌ Don't nest subagents
- ❌ Don't set `--max-turns` too low for complex tasks
- ❌ Don't ignore exit codes
- ❌ Don't mix unrelated tools in a single subagent call

## Important Notes

1. **No nesting**: Subagent cannot spawn another subagent
2. **Isolated context**: No access to parent conversation
3. **Process isolation**: Runs as separate OS process
4. **Focused prompt**: Specialized system prompt for concise, efficient execution
5. **Working directory**: Inherits from parent
6. **Clean slate**: Each subagent starts with fresh context unless session is reused
7. **No parent tools**: Subagent's tool access is independent of parent's available tools