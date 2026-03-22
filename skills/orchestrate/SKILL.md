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

## Planned Patterns (Phase 2)

### Chain Mode

Sequential execution with output passing:

```
task → scout → {context} → planner → {plan} → worker → {code}
```

**Use case**: Explore → Research → Implement pipeline

### Parallel Mode

Concurrent execution with result merging:

```
      ┌→ worker 1 ─┐
task →├→ worker 2 ─┼→ merge results
      └→ worker 3 ─┘
```

**Use case**: Researching multiple options in parallel

### Debate Mode

Multiple perspectives synthesis:

```
task → researcher A (观点 A)
     → researcher B (观点 B)
     → synthesizer (综合)
```

**Use case**: Evaluating different approaches

## Internal Personas

Orchestrate uses internal personas (not exposed as skills):

| Persona | Role | Location |
|---------|------|----------|
| **worker** | Executes implementation tasks | `orchestrate/references/worker.md` |
| **task-checker** | Verifies task completion | `orchestrate/references/task-checker.md` |

These are **internal** - use them via orchestrate, don't call directly.

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
| **2** | Chain/parallel modes | 🚧 Planned |
| **3** | Integration with speckit phases | 📋 Future |

## Key Benefits

- ✅ **Clean main agent context** - All execution in subagents
- ✅ **Crash recovery** - Each subagent session is isolated
- ✅ **Parallel processing** - Can run multiple subagents at once (Phase 2)
- ✅ **Specialized roles** - Each subagent has focused expertise
- ✅ **Composable** - Patterns can be combined

## Example Output

```
=== Orchestration ===
Tasks file: tasks.md

--- Iteration 1 ---

=== Task: Implement user signup (ID: T01) ===

--- Cycle 1/3 ---
→ Worker executing...
✓ Task completed: T01
→ Task-checker verifying...
⚠ Changes requested, cycle 1/3

--- Cycle 2/3 ---
Feedback from previous cycle:
1. Add password validation (min 8 chars)
2. Show error messages when form submits

→ Worker executing...
✓ Task completed: T01
→ Task-checker verifying...
✓ Task approved: T01

=== Task: Add database models (ID: T02) ===
...
```

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
