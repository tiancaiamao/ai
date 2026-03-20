---
name: subagent
description: Spawn isolated subagent processes for delegated tasks. Use for parallel execution, focused tasks, or breaking down complex problems.
tools: [bash]
---

# Subagent Skill

Spawn a subagent to handle delegated tasks. The subagent runs in an **isolated tmux session** using headless mode with a **focused system prompt**.

## ⚠️ CRITICAL RULES

```
🔴 RULE 1: NEVER use sleep, wait, or polling loops
   → Use tmux_wait.sh - it detects completion immediately
   → sleep 30 wastes 27s if subagent finishes in 3s

🔴 RULE 2: ALWAYS use tmux_wait.sh to wait for completion
   → Correct: tmux_wait.sh "$SESSION" 600
   → Wrong: sleep 30 && cat output.txt
   → Wrong: while [ ! -f done ]; do sleep 1; done

🔴 RULE 3: ALWAYS use --timeout to prevent runaway subagents
   → Recommended: 5-10 minutes for most tasks

🔴 RULE 4: Use --system-prompt @file for focused persona
   → Load appropriate role from skill references

🔴 RULE 5: NEVER use --max-turns unless explicitly needed
   → Let subagents complete naturally
```

## Correct Command Template

```bash
# STEP 1: Start subagent in tmux (auto-captures session ID)
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/subagent-output.txt \
  10m \
  /Users/genius/.ai/skills/orchestrate/references/explorer.md \
  "Your task description here")

# Extract session name (format: "session-name:session-id")
SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)

echo "Started subagent: $SESSION_NAME"

# STEP 2: Wait for completion
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 600

# STEP 3: Collect results
cat /tmp/subagent-output.txt
```

## Interrupting Subagents

**With tmux, you have better control than interrupt files:**

```bash
# Method 1: Send Ctrl+C (graceful interrupt)
tmux send-keys -t subagent-1234567890 C-c

# Method 2: Kill session (force interrupt)
tmux kill-session -t subagent-1234567890

# Method 3: Check what's running first
tmux attach -t subagent-1234567890
# Then: Ctrl+C to interrupt
```

**Why this is better than interrupt files:**
- ✅ Immediate effect (no polling delay)
- ✅ Standard Unix signals
- ✅ Can attach and inspect before killing
- ✅ No race conditions

## Subagent Management

### List Running Subagents

```bash
# List all subagent tmux sessions
tmux ls | grep subagent

# Check if specific session exists
tmux ls | grep "subagent-123"
```

### View Subagent Output in Real-time

```bash
# Attach to session (Ctrl-b d to detach)
tmux attach -t subagent-1234567890

# Capture current output
tmux capture-pane -t subagent-1234567890 -p

# Capture last N lines
tmux capture-pane -t subagent-1234567890 -p -S -50
```

### Kill a Runaway Subagent

```bash
# Find the session
tmux ls | grep subagent

# Kill it
tmux kill-session -t subagent-1234567890
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

Sessions are saved to:
```
~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl
```

Useful for debugging subagent behavior after execution.

## Timeout Guidelines

| Task Type | Recommended Timeout |
|-----------|-------------------|
| Quick search/analysis | 5 minutes |
| Code review | 10 minutes |
| Multi-file refactoring | 15 minutes |
| Complex investigation | 15-30 minutes |

## Common Pitfalls

| Problem | Cause | Solution |
|---------|-------|----------|
| Subagent hangs | No timeout set | Add `--timeout 10m` |
| Process stuck | Unexpected error | `tmux kill-session -t <name>` |
| Can't find session | Wrong name | `tmux ls \| grep subagent` |
| Lost output | Output went to void | Redirect to file: `> /tmp/out.txt 2>&1` |
| **Wasted time waiting** | **Using sleep instead of tmux_wait.sh** | **Always use tmux_wait.sh** |
| **Session ID capture fails** | **Long task description** | **Write to file, pass file path** |
| **Resource leaks** | **Failed start, no cleanup** | **start_subagent_tmux.sh auto-cleans** |

## Resource Management

**Automatic Cleanup**: `start_subagent_tmux.sh` automatically cleans up tmux sessions on failure:

```bash
# If Session ID capture fails, the script:
# 1. Outputs error message with full scrollback
# 2. Kills the tmux session it created
# 3. Exits with code 1

# This prevents resource leaks when startup fails
```

**Manual Cleanup** (if needed):

```bash
# List all subagent sessions
tmux ls | grep subagent

# Kill specific session
tmux kill-session -t subagent-1234567890

# Kill all subagent sessions (nuclear option)
tmux ls | grep subagent | cut -d: -f1 | xargs -I {} tmux kill-session -t {}
```

**Checking for Leaks**:

```bash
# Check for orphan tmux sessions
tmux ls | grep subagent

# Check for orphan processes
ps aux | grep "ai --mode headless" | grep -v grep

# Check for stale output files
ls -lt /tmp/*-output.txt | head -10
```

## Best Practices

- ✅ Always set `--timeout` to prevent runaway
- ✅ Use persona files via `--system-prompt @file`
- ✅ Run independent tasks in parallel with separate sessions
- ✅ Keep sessions for debugging
- ✅ Use `tmux kill-session` to terminate stuck subagents
- ✅ Use `tmux capture-pane` to check progress
- ✅ **For long tasks (>200 chars), write to file and pass file path**
- ❌ Don't use `--max-turns` (use timeout instead)
- ❌ Don't nest subagents
- ❌ **Don't pass long text directly in command line**

## Handling Long Task Descriptions

**Problem**: Passing long task descriptions (>200 characters) directly can cause:
- Command line too long errors
- Shell quoting issues (single/double quotes in text)
- tmux send-keys failures
- start_subagent_tmux.sh unable to capture Session ID

**Solution**: Write task to file, pass file path

```bash
# ❌ BAD: Long task description directly
SESSION=$(start_subagent_tmux.sh \
  /tmp/output.txt 10m @reviewer.md \
  "Review PR #62. This is a very long description with multiple paragraphs, special characters like it's, \"quotes\", $variables, etc...")  # FAILS!

# ✅ GOOD: Write to file first
cat > /tmp/task.txt << 'EOF'
Review PR #62 (tmux-unified-task-management).

Key Changes:
- Removed interrupt file mechanism
- Fixed 4 critical bugs found by dogfooding

Focus on: correctness, performance, security
Write result to /tmp/review-62.json
EOF

SESSION=$(start_subagent_tmux.sh \
  /tmp/output.txt 10m @reviewer.md \
  "Read task from /tmp/task.txt and follow instructions")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
tmux_wait.sh "$SESSION_NAME" 600
```

**Benefits**:
- ✅ No command line length limits
- ✅ No shell quoting issues
- ✅ Reliable Session ID capture
- ✅ Easier to debug (can view /tmp/task.txt)

## Output File Convention

When the main agent needs structured output from subagent:

```bash
# Main agent specifies output file in the prompt
SESSION=$(start_subagent_tmux.sh \
  /tmp/review-output.txt \
  10m \
  @reviewer.md \
  "Review this code. Write result to /tmp/review-result.json")

tmux_wait.sh "$(echo $SESSION | cut -d: -f1)" 600

# Read the structured result
cat /tmp/review-result.json
```