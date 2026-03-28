---
name: orchestrate
description: Orchestrate subagent tasks with dependency analysis and safe parallelism. Use when breaking down complex tasks into subtasks for delegation, especially for multi-step implementations, parallel exploration, or structured workflows.
allowed-tools: [bash]
---

# Orchestrate Skill

Delegate tasks to subagents with dependency analysis and safe parallel execution.

## Core Philosophy

- **Context isolation**: Each task runs in a fresh subagent to avoid context pollution
- **Serial by default**: Delegate tasks serially unless parallelism is provably safe
- **Two-stage review**: Spec compliance first, then code quality
- **Persistent sessions**: All sessions are persisted for debugging

## When to Use

- Multi-step implementation plans (use with workflow skill)
- Parallel exploration of multiple topics/codebases
- Breaking complex tasks into manageable subtasks
- When you need subagent delegation for any complex workflow

## When NOT to Use

- Simple tasks that don't need delegation
- Tasks that modify the same files (serial required)
- Anything requiring human review mid-execution

---

# Usage Patterns

## Pattern 1: Delegate (Serial Tasks)

For sequential tasks with dependencies:

```bash
# Create task list
cat > /tmp/tasks.txt << 'EOF'
Task 1: Create User model
Task 2: Add password hashing
Task 3: Create login endpoint
EOF

# Execute tasks serially with subagent
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/delegate-output.txt \
  15m \
  @~/.ai/skills/orchestrate/references/delegate-task.md \
  "$(cat /tmp/tasks.txt)")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 900

# Check result
cat /tmp/delegate-output.txt
```

## Pattern 2: Parallel Explore (Safe Parallelism)

For independent exploration tasks:

```bash
# Start multiple exploration sessions in parallel
SESSION1=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/explore1.txt 10m @explore.md "Explore pattern X")
SESSION2=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/explore2.txt 10m @explore.md "Explore pattern Y")

SESSION1_NAME=$(echo "$SESSION1" | cut -d: -f1)
SESSION2_NAME=$(echo "$SESSION2" | cut -d: -f1)

# Do main work while subagents run
MAIN_RESULT=$(do_something)

# Wait for both
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION1_NAME" 600
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION2_NAME" 600

# Merge results
cat /tmp/explore1.txt /tmp/explore2.txt
```

## Pattern 3: Two-Stage Review

For implementation quality assurance:

```bash
# Stage 1: Implementation
SESSION=$(start_subagent_tmux.sh /tmp/impl.txt 15m @impl.md "Implement task X")
tmux_wait.sh "$(echo $SESSION | cut -d: -f1)" 900

# Stage 2: Spec Compliance Review
SESSION=$(start_subagent_tmux.sh /tmp/spec-review.txt 10m @spec-review.md "Verify implementation matches spec")
tmux_wait.sh "$(echo $SESSION | cut -d: -f1)" 600

# Stage 3: Code Quality Review (only if spec passes)
SESSION=$(start_subagent_tmux.sh /tmp/quality-review.txt 10m @quality-review.md "Review code quality")
tmux_wait.sh "$(echo $SESSION | cut -d: -f1)" 600
```

---

# Reference Files

| File | Purpose |
|------|---------|
| `references/delegate-task.md` | Template for delegate task prompts |
| `references/spec-review.md` | Template for spec compliance review |
| `references/quality-review.md` | Template for code quality review |
| `references/explorer.md` | Template for parallel exploration |

---

# Dependency Analysis

## When Parallelism is SAFE

✅ **Read-only operations** (explore, search, analysis)
✅ **Different directories** (no file conflicts)
✅ **Different files in same directory** (if no shared imports)
✅ **Independent API calls** (different endpoints)

## When Parallelism is UNSAFE

❌ **Same files modified** (merge conflicts)
❌ **Shared configurations** (package.json, requirements.txt)
❌ **Same directory with imports** (dependency issues)
❌ **Database migrations** (schema conflicts)

## Decision Tree

```
Can tasks run in parallel?
├── Are tasks READ-ONLY?
│   └── YES → Parallel is safe
├── Do tasks modify DIFFERENT files?
│   └── YES → Likely safe, but check for shared dependencies
├── Do tasks modify SAME files?
│   └── NO → Must be serial
└── Unsure?
    └── Be conservative: use serial delegation
```

---

# Task Design Guidelines

## Good Task (2-5 minutes)

```
"Create User model with email and password_hash fields"
```

## Bad Task (too big)

```
"Implement user authentication system"
```

Break big tasks into smaller ones:
- Create User model
- Add password hashing
- Create login endpoint
- Create registration endpoint
- Add JWT token generation

---

# Session Management

## Session Location

Sessions are saved to: `~/.ai/sessions/--<cwd>--/<session-id>/`

## Debugging Subagent Sessions

```bash
# List all sessions
ls -ltd ~/.ai/sessions/--*--/*/ | head -10

# View session messages
cat ~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl

# Check session status
cat ~/.ai/sessions/--<cwd>--/<session-id>/status.json | jq .status

# Attach to tmux session (if still running)
tmux attach -t <session-name>
```

## Cleanup

```bash
# Kill stuck subagent
tmux kill-session -t <session-name>

# List running subagents
tmux ls | grep subagent
```

---

# Integration with Other Skills

## With workflow Skill

The workflow skill creates implementation plans. This skill executes them:

```bash
# 1. Plan phase (workflow skill)
/workflow create "Implement feature X"

# 2. Implement phase (orchestrate skill)
/orchestrate execute /tmp/plan.md
```

## With explore Skill

Use orchestrate for parallel exploration:

```bash
# Instead of sequential:
/explore topic-a
/explore topic-b

# Use parallel:
/orchestrate explore topic-a topic-b
```

## With review Skill

Two-stage review integrates with review skill:
- Stage 1: Spec compliance (orchestrate)
- Stage 2: Code quality (review skill)

---

# Quick Reference

```bash
# Serial delegation (delegate mode)
~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/output.txt 15m @delegate-task.md "task description"

# Parallel exploration
SESSION1=$(...topic A...)
SESSION2=$(...topic B...)
# Do main work
tmux_wait.sh $SESSION1 600
tmux_wait.sh $SESSION2 600

# Safe concurrency for read-only
ConcurrencyManager: max 5 concurrent for exploration
```
