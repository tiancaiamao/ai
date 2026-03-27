---
name: orchestrate
description: Multi-agent orchestration methodology. Use chain/parallel/loop patterns to coordinate multiple subagents for complex tasks. Current: loop mode for tasks.md execution.
allowed-tools: [bash, read, write, edit, grep]
---

# Orchestrate - Multi-Agent Orchestration

**Orchestrate** is a methodology for coordinating multiple specialized agents to complete complex tasks. Like how `speckit` provides a spec-driven development workflow, `orchestrate` provides patterns for multi-agent collaboration.

## What Problem Does It Solve?

When working on complex tasks, you often need:
- **Exploration** → Research existing code/docs
- **Planning** → Design the approach
- **Implementation** → Write the code
- **Verification** → Check if it works

Doing all of this in a single agent context leads to:
- Context bloat (accumulated garbage)
- No isolation between phases
- Hard to recover from failures

**Orchestrate solves this** by using isolated subagents for each phase.

## Core Principles

1. **Isolated Context** - Each subagent has a fresh context window
2. **Clear Roles** - Each subagent has a focused persona
3. **Composable Patterns** - Chain, parallel, loop modes can be combined
4. **Main Agent Supervision** - The main agent coordinates, subagents execute

## Supported Patterns (Phase 1)

### Loop Mode (Current Default)

Execute tasks with a worker → checker loop:

```
for each task:
    while not approved:
        worker subagent executes
        task-checker subagent verifies
        if approved: move to next task
        if needs fixes: loop back to worker
```

**Use case**: Executing `tasks.md` from speckit

**Usage**:
```bash
~/.ai/skills/orchestrate/bin/orchestrate.sh
```

**How it works**:
1. Reads `tasks.md`
2. For each pending task:
   - Spawns a worker subagent to implement
   - Spawns a task-checker subagent to verify
   - Loops up to 3 times if fixes are needed
   - Marks task as done/failed

## Parallel Execution Pattern

### Wait for All (Most Common)

Use when you have 2-8 independent subtasks:

```bash
# Start all subagents in parallel
SESSION1=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/out1.txt 10m @worker.md "Create user model")
SESSION2=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/out2.txt 10m @worker.md "Create auth middleware")
SESSION3=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/out3.txt 10m @worker.md "Create auth routes")

# Do independent work while they run
# Example: Review related code, prepare integration tests

# Wait for all (using session names)
~/.ai/skills/tmux/bin/tmux_wait.sh "$(echo $SESSION1 | cut -d: -f1)" /tmp/out1.txt 600
~/.ai/skills/tmux/bin/tmux_wait.sh "$(echo $SESSION2 | cut -d: -f1)" /tmp/out2.txt 600
~/.ai/skills/tmux/bin/tmux_wait.sh "$(echo $SESSION3 | cut -d: -f1)" /tmp/out3.txt 600

# Collect all results
RESULT1=$(cat /tmp/out1.txt)
RESULT2=$(cat /tmp/out2.txt)
RESULT3=$(cat /tmp/out3.txt)

# Verify integration
go test ./...
```

### Wait for Fastest (For Exploration/Research)

```bash
# Start multiple subagents exploring different options
SESSION1=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/opt1.txt 5m @researcher.md "Research option A")
SESSION2=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/opt2.txt 5m @researcher.md "Research option B")
SESSION3=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/opt3.txt 5m @researcher.md "Research option C")

# Wait for first to complete
FIRST=$(~/.ai/skills/subagent/bin/wait_any_subagent.sh \
  subagent-1 subagent-2 subagent-3)

# Get result from the first
case $FIRST in
  subagent-1) BEST=$(cat /tmp/opt1.txt) ;;
  subagent-2) BEST=$(cat /tmp/opt2.txt) ;;
  subagent-3) BEST=$(cat /tmp/opt3.txt) ;;
esac

# Kill others (optional)
# tmux kill-session -t ...
```

## When to Use Parallel Subagents

**MUST USE** when ALL conditions are met:
- Task can be split into 2-8 independent subtasks
- Each subtask has its own output file/destination
- No dependencies between subtasks
- Convergence verification is automatable (tests, scripts)

**Example: Implement User Authentication**
```
Subtask 1: Create user model (→ models/user.go)
Subtask 2: Create auth middleware (→ middleware/auth.go)
Subtask 3: Create auth routes (→ routes/auth.go)
Subtask 4: Write auth tests (→ auth_test.go)

Convergence: Run integration tests to verify all parts work together
```

**Counter-Example: DON'T Parallelize**
```
Subtask 1: Create user model (→ models/user.go)
Subtask 2: Create user service (needs models/user.go) ❌ DEPENDENCY
Subtask 3: Create user routes (needs user service) ❌ DEPENDENCY

This should be serial, not parallel.
```

## Parallel Task Decomposition Pattern

When speckit generates tasks.md, check for parallelizable tasks:

```markdown
## Example tasks.md (Good for parallel)

- [ ] Create user model
- [ ] Create auth middleware
- [ ] Create auth routes
- [ ] Write auth tests
```

These are independent → Can run in parallel with subagents.

```markdown
## Example tasks.md (Must be serial)

- [ ] Create user model
- [ ] Create user service (depends on model)
- [ ] Create user routes (depends on service)
- [ ] Write integration tests (depends on all above)
```

These have dependencies → Must run serially.

## Internal Personas

Orchestrate uses internal personas (not exposed as skills):

| Persona | Role | Location |
|---------|------|----------|
| **worker** | Executes implementation tasks | `orchestrate/references/worker.md` |
| **task-checker** | Verifies task completion | `orchestrate/references/task-checker.md` |

These are **internal** - use them via orchestrate, don't call directly.

## Error Handling

### Subagent Failure

```bash
# If subagent fails, check:
# 1. /tmp/output.txt for error details
# 2. tmux attach -t <session> to see full context

# Retry with different approach:
# - Increase timeout if needed
# - Simplify task
# - Break into smaller subtasks
```

### Timeout Handling

```bash
# Default timeout is 10m, adjust for task complexity:
# - Quick search/analysis: 5m
# - Code review: 10m
# - Multi-file refactoring: 15m
# - Complex investigation: 15-30m

# If timeout occurs, task may still be running:
# - Check with tmux ls
# - Attach to see progress
# - Or kill and restart with longer timeout
```

## Planned Patterns (Phase 2)

### Chain Mode

Sequential execution with output passing:

```
task → scout → {context} → planner → {plan} → worker → {code}
```

**Use case**: Explore → Research → Implement pipeline

### Debate Mode

Multiple perspectives synthesis:

```
task → researcher A (观点 A)
     → researcher B (观点 B)
     → synthesizer (综合)
```

**Use case**: Evaluating different approaches

## vs Other Skills

| Skill | Focus | Subagents? |
|-------|-------|------------|
| **speckit** | Spec-driven workflow | No (main agent does all work) |
| **orchestrate** | Multi-agent coordination | Yes (core feature) |
| **explore** | Codebase exploration | Yes (uses subagent) |
| **review** | Code review | Yes (uses subagent) |

## Typical Workflow

```
1. /skill:explore      → explorer/*.md (optional, for context)
2. /skill:brainstorming → decisions.md (optional, for decisions)
3. /skill:speckit      → spec.md, plan.md, tasks.md
4. orchestrate.sh      → executes all tasks with worker→checker loop
```

## Commands

### Run orchestration
```bash
~/.ai/skills/orchestrate/bin/orchestrate.sh
```

### Check status
```bash
~/.ai/skills/orchestrate/bin/orchestrate.sh status
```

### Execute next task only
```bash
~/.ai/skills/orchestrate/bin/orchestrate.sh next
```

### Initialize workflow
```bash
~/.ai/skills/orchestrate/bin/orchestrate.sh init
```

## Roadmap

| Phase | Features | Status |
|-------|----------|--------|
| **1** | Loop mode for tasks.md | ✅ Current |
| **2** | Parallel execution with subagents | 🚧 In Progress |
| **3** | Chain/debate modes | 📋 Planned |
| **4** | Integration with speckit phases | 📋 Future |

## Key Benefits

- ✅ **Clean main agent context** - All execution in subagents
- ✅ **Crash recovery** - Each subagent session is isolated
- ✅ **Parallel processing** - Can run multiple subagents at once
- ✅ **Specialized roles** - Each subagent has focused expertise
- ✅ **Composable** - Patterns can be combined

## Troubleshooting

### Task stuck in loop
- Check `/tmp/orchestrate-check-*.txt` for feedback
- Max cycles is 3 - after that it stops for manual intervention

### Subagent fails to start
- Ensure `ai` binary is in PATH
- Check tmux is installed: `tmux -V`

### Task-checker not parsing
- Ensure task-checker.md outputs `TASK_CHECK_RESULT` JSON
- Check `/tmp/orchestrate-check-*.txt` for raw output