---
name: subagent
description: Spawn isolated subagent processes for delegated tasks. Use for parallel execution, focused tasks, or breaking down complex problems.
tools: [bash]
---

# Subagent Skill

Spawn a subagent to handle delegated tasks. The subagent runs in an **isolated process** using headless mode with a **focused system prompt**.

## ⚠️ CRITICAL RULES

```
1. Sessions are ALWAYS saved (no --no-session option)
   → Sessions are needed for debugging subagent behavior

2. ALWAYS use --timeout to prevent runaway subagents
   → Recommended: 5-10 minutes for most tasks

3. Use --system-prompt @file for focused persona
   → Load appropriate role from skill references

4. NEVER use --max-turns unless explicitly needed
   → Let subagents complete naturally
```

## Correct Command Template

```bash
# CORRECT ✓ - Session enabled, timeout set, persona loaded
ai --mode headless \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/explorer.md \
  --timeout 10m \
  "Your task description here"

# WRONG ✗ - No timeout = runaway risk
ai --mode headless "complex task"

# WRONG ✗ - No persona = unfocused output
ai --mode headless --timeout 10m "analyze this"
```

## Subagent Management with Bash

Since there's no dedicated subagent tool, use bash commands:

### Find Running Subagents

```bash
# Find all ai headless processes
ps aux | grep "ai.*--mode headless"

# More precise: find by timeout flag
ps aux | grep "ai.*--mode headless" | grep -v grep
```

### Kill a Runaway Subagent

```bash
# Find the PID first
ps aux | grep "ai.*--mode headless"

# Kill by PID
kill <PID>

# Force kill if stuck
kill -9 <PID>
```

### Track Subagent Sessions

Sessions are saved to:
```
~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl
```

```bash
# List all sessions
ls -la ~/.ai/sessions/

# View a session
cat ~/.ai/sessions/--project--/abc123/messages.jsonl | jq .
```

## When to Use

- **Parallel execution** of independent tasks
- **Complex problems** that need focused attention
- **Breaking down** large tasks into sub-tasks
- **Tasks requiring isolation** from main conversation context

## Command-Line Options

| Option | Description | Default |
|--------|-------------|---------|
| `--mode headless` | Run in headless mode (required) | - |
| `--timeout D` | Total execution timeout (e.g., `10m`, `300s`) | 0 (unlimited) |
| `--system-prompt @FILE` | Load persona from file | default headless prompt |
| `--tools T1,T2` | Comma-separated tool whitelist | all tools |
| `--max-turns N` | Maximum turns (avoid, use timeout) | 0 (unlimited) |

## Session Persistence

**Sessions are always saved to:**
```
~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl
```

This allows you to:
- Debug subagent behavior after execution
- Review tool calls and reasoning
- Understand why a subagent failed

## Monitoring via status.json

**Each headless session creates a status.json file:**
```
~/.ai/sessions/--<cwd>--/<session-id>/status.json
```

**Status file structure:**
```json
{
  "session_id": "abc123",
  "pid": 54321,
  "status": "running",
  "current_turn": 3,
  "last_tool": "read",
  "last_activity": "2024-03-11T09:00:00Z",
  "started_at": "2024-03-11T08:55:00Z",
  "error": ""
}
```

**Status values:**
- `running` - Subagent is actively processing
- `completed` - Finished successfully
- `timeout` - Exceeded timeout limit
- `error` - Failed with an error

**Monitor subagent progress:**
```bash
# Start subagent in background
(ai --mode headless --timeout 10m "task" > /tmp/out.txt 2>&1) &
PID=$!

# Get session ID from output
SESSION_ID=$(grep "Session ID:" /tmp/out.txt | head -1 | awk '{print $3}')

# Monitor status
watch -n 2 "cat ~/.ai/sessions/*/$SESSION_ID/status.json 2>/dev/null | jq ."

# Or check specific fields
cat ~/.ai/sessions/*/$SESSION_ID/status.json | jq '.current_turn, .last_tool, .status'
```

**For aiclaw integration:**
- Read status.json periodically to display progress
- Check `status` field to know when complete
- Use `current_turn` and `last_tool` for progress indicators

## Output Format

```
=== Session Info ===
Session ID: abc123
Session file: ~/.ai/sessions/--project--/abc123/messages.jsonl

=== Turn 1 ===
Thinking: ...
Tool calls:
  • read: path=config.yaml
  • grep: pattern=timeout

=== Summary ===
Total turns: 5
Tokens: 1500 input, 800 output, 2300 total
Duration: 45.2s
```

## Examples

### Example 1: Code Analysis with Timeout

```bash
ai --mode headless \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/explorer.md \
  --timeout 5m \
  "Analyze the authentication flow in src/auth/"
```

### Example 2: Parallel Analysis

```bash
# Run 3 parallel subagents
(ai --mode headless \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/explorer.md \
  --timeout 10m \
  "Analyze project A architecture" > /tmp/a.txt) &

(ai --mode headless \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/explorer.md \
  --timeout 10m \
  "Analyze project B architecture" > /tmp/b.txt) &

(ai --mode headless \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/explorer.md \
  --timeout 10m \
  "Analyze project C architecture" > /tmp/c.txt) &

wait
cat /tmp/a.txt /tmp/b.txt /tmp/c.txt
```

### Example 3: With Tool Restrictions

```bash
# Read-only explorer (safe for analysis)
ai --mode headless \
  --tools read,grep \
  --timeout 5m \
  "Find all API endpoints in src/api/"
```

### Example 4: Check and Kill Stuck Subagent

```bash
# Check running subagents
$ ps aux | grep "ai.*--mode headless"
genius   54321  0.5  1.2  ... ai --mode headless ...
genius   54322  0.3  1.1  ... ai --mode headless ...

# Kill a stuck one
$ kill 54321

# Verify it's gone
$ ps aux | grep 54321
```

## Persona Profiles

Use with `--system-prompt @/Users/genius/.ai/skills/orchestrate/references/<persona>.md`:

| Persona | File | Purpose |
|---------|------|---------|
| Explorer | `explorer.md` | Code analysis, architecture review |
| Researcher | `researcher.md` | Investigation, comparison |
| Implementer | `implementer.md` | Feature implementation |
| Reviewer | `reviewer.md` | Code review, validation |

## Timeout Guidelines

| Task Complexity | Recommended Timeout |
|----------------|---------------------|
| Simple lookup | 2-5 minutes |
| Code analysis | 5-10 minutes |
| Feature implementation | 10-20 minutes |
| Complex investigation | 15-30 minutes |

## Debugging Subagents

If a subagent fails or behaves unexpectedly:

```bash
# 1. Check if it's still running
ps aux | grep "ai.*--mode headless"

# 2. Find the session from output
# Session ID: abc123

# 3. View the session
cat ~/.ai/sessions/--<project>--/abc123/messages.jsonl | jq .

# 4. If stuck, kill it
kill <PID>
```

## Debugging & Monitoring Subagents

### Problem: Bash Timeout vs Subagent Timeout

When calling subagent via bash tool, the bash tool has its own timeout (typically 30s). Even if subagent has `--timeout 10m`, bash will timeout first.

**Solution: Run subagent in background and collect results**

```bash
# 1. Start subagent in background, output to file
(ai --mode headless \
  --system-prompt @/Users/genius/.ai/skills/review/reviewer.md \
  --timeout 10m \
  "Review this code: $(cat /tmp/diff.txt)" > /tmp/subagent_output.txt 2>&1) &

SUBAGENT_PID=$!
echo "Subagent started with PID: $SUBAGENT_PID"
```

### Monitor Subagent Status

```bash
# 1. Check if process is still alive
ps -p $SUBAGENT_PID -o pid,ppid,%cpu,%mem,etime,stat,command

# 2. Watch output file in real-time
tail -f /tmp/subagent_output.txt

# 3. Monitor session file (find latest)
SESSION_FILE=$(ls -t ~/.ai/sessions/*/*/messages.jsonl 2>/dev/null | head -1)
if [ -n "$SESSION_FILE" ]; then
  tail -f "$SESSION_FILE" | jq -r 'select(.role=="assistant") | .content[]? | select(.type=="text") | .text'
fi
```

### Check Subagent Progress

```bash
# Count turns completed (from session file)
SESSION_FILE=$(ls -t ~/.ai/sessions/*/*/messages.jsonl 2>/dev/null | head -1)
echo "Turns: $(grep -c '"role":"assistant"' "$SESSION_FILE" 2>/dev/null || echo 0)"

# Check if subagent is making progress (file size changing)
watch -n 2 "ls -lh /tmp/subagent_output.txt"

# View last N lines of output
tail -20 /tmp/subagent_output.txt
```

### Collect Results

```bash
# Wait for completion and get exit code
wait $SUBAGENT_PID
EXIT_CODE=$?

if [ $EXIT_CODE -eq 0 ]; then
  echo "Subagent completed successfully"
  cat /tmp/subagent_output.txt
else
  echo "Subagent failed with exit code: $EXIT_CODE"
  # Check session for error details
  cat "$SESSION_FILE" | jq -r 'select(.role=="assistant") | .content[]? | select(.type=="text") | .text' | tail -50
fi
```

### Kill Stuck Subagent

```bash
# Check if still running
if ps -p $SUBAGENT_PID > /dev/null 2>&1; then
  echo "Subagent still running, killing..."
  kill $SUBAGENT_PID
  # Force kill if needed
  sleep 2
  kill -9 $SUBAGENT_PID 2>/dev/null
fi
```

### Full Debugging Script

```bash
#!/bin/bash
# debug_subagent.sh - Monitor and debug a running subagent

echo "=== Running Subagents ==="
ps aux | grep "ai.*--mode headless" | grep -v grep

echo -e "\n=== Latest Session Files ==="
ls -lt ~/.ai/sessions/*/*/messages.jsonl 2>/dev/null | head -5

echo -e "\n=== Latest Session Activity ==="
LATEST=$(ls -t ~/.ai/sessions/*/*/messages.jsonl 2>/dev/null | head -1)
if [ -n "$LATEST" ]; then
  echo "File: $LATEST"
  echo "Size: $(ls -lh "$LATEST" | awk '{print $5}')"
  echo "Messages: $(wc -l < "$LATEST")"
  echo -e "\nLast assistant message:"
  tac "$LATEST" | grep -m1 '"role":"assistant"' | jq -r '.content[]? | select(.type=="text") | .text' | head -20
fi
```

## Common Pitfalls

| Problem | Cause | Solution |
|---------|-------|----------|
| Subagent hangs | No timeout set | Add `--timeout 10m` |
| Process stuck | Unexpected error | Use `ps` + `kill` to terminate |
| Can't find session | Wrong directory | Check `Session file:` in output |
| Bash timeout | Bash tool has 30s limit | Run in background with `&` and collect to file |
| Lost output | Output went to void | Redirect to file: `> /tmp/out.txt 2>&1` |

## Best Practices

- ✅ Always set `--timeout` to prevent runaway
- ✅ Use persona files via `--system-prompt @file`
- ✅ Run independent tasks in parallel with `&` and `wait`
- ✅ Keep sessions for debugging
- ✅ Use `ps aux | grep` to track running subagents
- ✅ Use `kill` to terminate stuck subagents
- ❌ Don't use `--max-turns` (use timeout instead)
- ❌ Don't nest subagents