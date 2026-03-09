---
name: subagent
description: Spawn isolated subagent processes for delegated tasks. Use for parallel execution, focused tasks, or breaking down complex problems.
tools: [bash]
---

# Subagent Skill

Spawn a subagent to handle delegated tasks. The subagent runs in an **isolated process** using headless mode with a **focused system prompt**.

## ⚠️ CRITICAL RULES

```
1. NEVER use --no-session by default
   → Sessions are needed for debugging subagent behavior

2. ALWAYS use --subagent flag for focused prompt
   → Gives subagent appropriate role and behavior

3. Use --subagent-timeout to prevent runaway subagents
   → Recommended: 5-10 minutes for most tasks

4. NEVER use --max-turns unless explicitly needed
   → Let subagents complete naturally
```

## Correct Command Template

```bash
# CORRECT ✓ - Session enabled for debugging
ai --mode headless --subagent \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/explorer.md \
  --subagent-timeout 10m \
  "Your task description here"

# WRONG ✗ - No session = no debugging
ai --mode headless --no-session --subagent "task"

# WRONG ✗ - No timeout = runaway risk
ai --mode headless --subagent "complex task"
```

## Subagent Management with Bash

Since there's no dedicated subagent tool, use bash commands:

### Find Running Subagents

```bash
# Find all ai subagent processes
ps aux | grep "ai.*--subagent"

# More precise: find by session directory
ps aux | grep "ai.*--subagent" | grep -v grep
```

### Kill a Runaway Subagent

```bash
# Find the PID first
ps aux | grep "ai.*--subagent"

# Kill by PID
kill <PID>

# Force kill if stuck
kill -9 <PID>
```

### Track Subagent Sessions

Subagent sessions are saved to:
```
~/.ai/sessions/--<cwd>--/subagents/<session-id>/messages.jsonl
```

```bash
# List all subagent sessions
ls -la ~/.ai/sessions/--$(echo $PWD | sed 's/\//--/g')--/subagents/

# Or simply
ls -la ~/.ai/sessions/*/subagents/ 2>/dev/null

# View a subagent's session
cat ~/.ai/sessions/--project--/subagents/abc123/messages.jsonl | jq .
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
| `--subagent` | Use focused subagent system prompt | false |
| `--subagent-timeout D` | Total execution timeout (e.g., `10m`, `300s`) | 0 (unlimited) |
| `--system-prompt @FILE` | Load persona from file | default subagent prompt |
| `--tools T1,T2` | Comma-separated tool whitelist | all tools |
| `--no-session` | **Don't use** (needed for debugging) | false |
| `--max-turns N` | Maximum turns (avoid, use timeout) | 0 (unlimited) |

## Session Persistence

**Subagent sessions are saved to:**
```
~/.ai/sessions/--<cwd>--/subagents/<session-id>/messages.jsonl
```

This allows you to:
- Debug subagent behavior after execution
- Review tool calls and reasoning
- Understand why a subagent failed

## Output Format

```
=== Session Info ===
Session ID: abc123
Session file: ~/.ai/sessions/--project--/subagents/abc123/messages.jsonl
Subagent dir: ~/.ai/sessions/--project--/subagents

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
ai --mode headless --subagent \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/explorer.md \
  --subagent-timeout 5m \
  "Analyze the authentication flow in src/auth/"
```

### Example 2: Parallel Analysis

```bash
# Run 3 parallel subagents with sessions
(ai --mode headless --subagent \
  --subagent-timeout 10m \
  "Analyze project A architecture" > /tmp/a.txt) &

(ai --mode headless --subagent \
  --subagent-timeout 10m \
  "Analyze project B architecture" > /tmp/b.txt) &

(ai --mode headless --subagent \
  --subagent-timeout 10m \
  "Analyze project C architecture" > /tmp/c.txt) &

wait
cat /tmp/a.txt /tmp/b.txt /tmp/c.txt
```

### Example 3: With Tool Restrictions

```bash
# Read-only explorer (safe for analysis)
ai --mode headless --subagent \
  --tools read,grep \
  --subagent-timeout 5m \
  "Find all API endpoints in src/api/"
```

### Example 4: Check and Kill Stuck Subagent

```bash
# Check running subagents
$ ps aux | grep "ai.*--subagent"
genius   54321  0.5  1.2  ... ai --mode headless --subagent ...
genius   54322  0.3  1.1  ... ai --mode headless --subagent ...

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
ps aux | grep "ai.*--subagent"

# 2. Find the session from output
# Session ID: abc123

# 3. View the session
cat ~/.ai/sessions/--<project>--/subagents/abc123/messages.jsonl | jq .

# 4. If stuck, kill it
kill <PID>
```

## Common Pitfalls

| Problem | Cause | Solution |
|---------|-------|----------|
| No session file | Used `--no-session` | Remove `--no-session` flag |
| Subagent hangs | No timeout set | Add `--subagent-timeout 10m` |
| Process stuck | Unexpected error | Use `ps` + `kill` to terminate |
| Can't find session | Wrong directory | Check `Subagent dir:` in output |

## Best Practices

- ✅ Always use `--subagent` for focused output
- ✅ Set `--subagent-timeout` to prevent runaway
- ✅ Use persona files via `--system-prompt @file`
- ✅ Run independent tasks in parallel with `&` and `wait`
- ✅ Keep sessions for debugging
- ✅ Use `ps aux | grep` to track running subagents
- ✅ Use `kill` to terminate stuck subagents
- ❌ Don't use `--no-session` (lose debugging info)
- ❌ Don't use `--max-turns` (use timeout instead)
- ❌ Don't nest subagents