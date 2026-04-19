# Migration: subagent → ag

## Status: subagent Skill Deprecated

**Date:** 2024-04-13
**Action:** `subagent` skill has been deprecated and replaced by `ag` CLI

## Why This Change?

The `subagent` skill provided basic agent spawning via `start_subagent_tmux.sh`, but had significant limitations:

1. **No unified interface** - Manual lifecycle management with tmux commands
2. **No status tracking** - Had to manually check tmux sessions
3. **No communication layer** - File-based messaging was ad-hoc
4. **No task management** - No built-in queue or claim system
5. **Hard to compose** - Difficult to build multi-agent workflows

The `ag` CLI was created to address these limitations:
- **Agent lifecycle**: spawn, wait, kill, rm, status (built-in)
- **Communication**: channels, send/recv (structured)
- **Task management**: create, claim, done/fail (atomic)
- **Pattern scripts**: pair, parallel, pipeline, fan-out (reusable)

## Quick Migration

### Old (subagent)

```bash
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/output.txt \
  10m \
  @planner.md \
  "Task description")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" /tmp/output.txt 600
OUTPUT=$(cat /tmp/output.txt)
```

### New (ag)

```bash
ag spawn --id planner --system @planner.md --input "Task description" --timeout 10m
ag wait planner --timeout 600
OUTPUT=$(ag output planner)
ag rm planner
```

## Detailed Migration Guide

### 1. Basic Agent Spawning

| Old (subagent) | New (ag) |
|----------------|-----------|
| `start_subagent_tmux.sh <output> <timeout> <system> <task>` | `ag spawn --id <id> --system <system> --input <task> --timeout <timeout>` |
| Manual `cut` to get session name | `ag ls` shows all sessions |
| Manual `tmux_wait.sh` | `ag wait <id>` |
| Manual `cat output.txt` | `ag output <id>` |
| Manual cleanup | `ag rm <id>` |

### 2. Error Handling

**Old way:**
```bash
SESSION=$(start_subagent_tmux.sh ...)

if [ $? -ne 0 ]; then
  echo "Failed to start"
  exit 1
fi

# Manual wait with timeout
if ! tmux_wait.sh "$SESSION_NAME" output.txt 600; then
  echo "Timeout or error"
  exit 1
fi
```

**New way:**
```bash
# spawn handles errors
ag spawn --id agent --system prompt.md --input "task" --timeout 10m

# wait returns status code (0=success, 1=timeout/error)
if ! ag wait agent --timeout 600; then
  # Check why it failed
  ag status agent  # shows 'failed', 'timeout', etc.
  exit 1
fi
```

### 3. Message Passing

**Old way:**
```bash
# Agent 1 writes to file
start_subagent_tmux.sh /tmp/agent1-out.txt 10m @writer.md "write to /tmp/shared.txt"

# Agent 2 reads from file
start_subagent_tmux.sh /tmp/agent2-out.txt 10m @reader.md "read from /tmp/shared.txt"
```

**New way:**
```bash
# Create channel
ag channel create shared

# Agent 1 sends to channel
ag send shared --file message.txt

# Agent 2 receives from channel
ag recv shared --wait --timeout 60
```

### 4. Parallel Execution

**Old way:**
```bash
# Spawn agents in background
start_subagent_tmux.sh /tmp/agent1-out.txt 10m @agent1.md "task 1" &
SESSION1=$!

start_subagent_tmux.sh /tmp/agent2-out.txt 10m @agent2.md "task 2" &
SESSION2=$!

# Wait for both
wait $SESSION1
wait $SESSION2

# Collect outputs
OUTPUT1=$(cat /tmp/agent1-out.txt)
OUTPUT2=$(cat /tmp/agent2-out.txt)
```

**New way:**
```bash
# Use pattern script
ag patterns/parallel.sh 2 agent.md "task" /tmp/results

# Or manual
ag spawn --id agent1 --system @agent1.md --input "task 1" --timeout 10m
ag spawn --id agent2 --system @agent2.md --input "task 2" --timeout 10m
ag wait agent1 --timeout 600
ag wait agent2 --timeout 600
OUTPUT1=$(ag output agent1)
OUTPUT2=$(ag output agent2)
```

### 5. Worker-Judge Loop (pair pattern)

**Old way:**
```bash
# Manual loop
for round in {1..3}; do
  # Spawn worker
  start_subagent_tmux.sh /tmp/worker-out.txt 10m @worker.md "task"
  tmux_wait.sh "worker" /tmp/worker-out.txt 600

  # Spawn judge
  start_subagent_tmux.sh /tmp/judge-out.txt 10m @judge.md "review /tmp/worker-out.txt"
  tmux_wait.sh "judge" /tmp/judge-out.txt 600

  VERDICT=$(cat /tmp/judge-out.txt | grep APPROVED)
  if [ -n "$VERDICT" ]; then
    break
  fi

  # Feed back to worker
  TASK="fix issues: $(cat /tmp/judge-out.txt)"
done
```

**New way:**
```bash
# Use pair.sh pattern
ag patterns/pair.sh @worker.md @judge.md task.md 3
```

## Pattern Mappings

| Old Pattern | New Pattern (ag) |
|-------------|------------------|
| Manual spawn/wait → manual spawn/wait | `ag spawn` + `ag wait` |
| File-based messaging | `ag send` / `ag recv` / channels |
| Manual parallel spawns | `ag patterns/parallel.sh` |
| Manual worker-judge loop | `ag patterns/pair.sh` |
| Sequential processing | `ag patterns/pipeline.sh` |
| Task queue + workers | `ag patterns/fan-out.sh` |
| Manual status checks | `ag status` / `ag ls` |

## Breaking Changes

1. **Command name**: `subagent` → `ag`
2. **Agent ID required**: `ag spawn` requires `--id <name>` (auto-generated in some patterns)
3. **Path changes**: `~/.ai/skills/subagent/` → `~/.ai/skills/ag/`
4. **Script location**: `start_subagent_tmux.sh` now at `~/.ai/skills/ag/internal/tmux/start_agent.sh`

## Files Changed

- **Deprecated**: `~/.ai/skills/subagent/` → `~/.ai/skills/subagent-deprecated-20240413/`
- **Moved**: `~/.ai/skills/subagent/bin/start_subagent_tmux.sh` → `~/.ai/skills/ag/internal/tmux/start_agent.sh`
- **Updated**: `~/.ai/skills/ag/SKILL.md` (added migration guide)
- **Updated**: `~/.ai/project/ai/skills/ag/internal/agent/agent.go` (pointed to new script path)

## Rollback Plan

If issues arise with `ag`, you can temporarily restore `subagent`:

```bash
# Restore old skill
mv ~/.ai/skills/subagent-deprecated-20240413 ~/.ai/skills/subagent

# Update scripts to use old path
# (or use symlinks)
```

However, we strongly encourage fixing any issues with `ag` rather than rolling back.

## Support

For questions or issues:
1. Check `ag/SKILL.md` for detailed documentation
2. Check `ag/patterns/` for usage examples
3. Report issues in the project repository