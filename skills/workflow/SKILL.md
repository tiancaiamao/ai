---
name: workflow
description: |
  Multi-phase workflow system. Agent uses /workflow commands to execute structured
  development flows (feature/bugfix/refactor/spike) with spec/plan phases.
  State persisted to disk, phases executed via ag agents.
---

# Workflow - Multi-Agent Orchestration

## Core Philosophy

> **State on disk, progress visible, commits after each phase.**

Workflow is **execution engine** that turns plans into shipped code. It combines:
- **Workflow Templates**: Structured development flows (feature, bugfix, refactor, spike)
- **Agent-Driven Execution**: Agent autonomously progresses through phases
- **State Management**: Deterministic state updates via `workflow-ctl` tool
- **ag Backend**: Agent orchestration primitives for phase execution

## Architecture

```
/workflow start feature "X"
    ↓
workflow-ctl start → write STATE.json
    ↓
Agent reads STATE.json → current phase = spec
    ↓
Agent reads templates/feature.md → Phase 1 instructions
    ↓
Agent executes Phase 1: Spec
    ↓
Agent calls workflow-ctl advance → update STATE.json {currentPhase: plan}
    ↓
Agent reads Phase 2: Plan
    ↓
Agent executes Phase 2: Plan (generates tasks.md)
    ↓
Agent calls ag task create + fan-out.sh for parallel execution
    ↓
Agent calls workflow-ctl advance → update STATE.json
    ↓
...continue until all phases done
```

**workflow-ctl Tool:**
- **Location:** `skills/workflow/bin/workflow-ctl`
- **Build:** `cd skills/workflow && go build -o bin/workflow-ctl workflow-ctl.go`
- **Commands:**
  - `start <template> <description>` — Start new workflow
  - `advance [--phase <name>]` — Advance to next phase (or jump to specific phase)
  - `status` — Show current workflow state
  - `pause` / `resume` — Pause/resume workflow

**ag CLI:**
- **Location:** `skills/ag/` (separate skill)
- **Used for:** Agent spawn, task queue (for dynamic parallel tasks)

> **Note:** workflow-ctl provides **deterministic state updates**. Agent controls execution flow
> but state is persisted by the tool, ensuring reliability and restart capability.

## Commands

| Command | Description |
|---------|-------------|
| `/workflow start <template> [description]` | Start a new workflow |
| `/workflow status` | Show workflow state |
| `/workflow advance [--phase <name>]` | Advance to next phase (for agent use) |
| `/workflow pause` / `/workflow resume` | Pause/resume workflow |

**Agent Usage:**
```bash
# Agent-driven execution
workflow-ctl start feature "add user auth"

# Agent reads Phase 1: Spec from template, executes it
workflow-ctl advance  # Move to next phase

# Agent reads Phase 2: Plan, generates tasks.md
ag task create "implement API"
ag task create "write tests"
# ... execute tasks via fan-out pattern
workflow-ctl advance  # Move to next phase
```

## Quick Start

```bash
# Setup binaries
cd skills/ag && go build -o ag .
cd skills/workflow && go build -o bin/workflow-ctl workflow-ctl.go

# Start a workflow
workflow-ctl start feature "add user authentication"

# Check status
workflow-ctl status

# Agent-driven execution (agent calls this after each phase):
workflow-ctl advance
```

## Templates

| Template | Complexity | Use Case |
|----------|------------|----------|
| `feature` | Medium | New feature development |
| `bugfix` | Low | Bug fix with root-cause analysis |
| `refactor` | Medium | Code restructuring |
| `spike` | Low | Research/exploration |
| `hotfix` | Minimal | Emergency production fix |
| `security` | Medium | Security audit/fix |

## Feature Development: SPEC + PLAN

For feature work, feature template includes **spec** and **plan** phases.

### Phase 1: SPEC (What)

Create `SPEC.md` defining **what** we're building:

```markdown
# Feature: [Name]

## Summary
[1-2 sentence description]

## Motivation
[Why are we doing this?]

## User Stories
- As a [user], I want [goal] so that [benefit]

## Requirements
- [ ] [requirement 1]
- [ ] [requirement 2]

## Out of Scope
- [ ] [explicitly not doing]

## Success Criteria
- [ ] [criterion 1]
- [ ] [criterion 2]
```

**Review criteria:**
- [ ] Clear user value?
- [ ] Scope bounded?
- [ ] Testable requirements?

### Phase 2: PLAN (How)

Read SPEC.md, explore codebase, create `PLAN.md` defining **how**:

```markdown
# Plan: [Feature]

## Technical Context
[Existing patterns, relevant files]

## Data Model
[Any new types or changes]

## API Design
[If applicable]

## Implementation Steps

### STEP-1: [Name]
**File:** `src/xxx.go`
**What:** [Brief description]
**Test:** [How to verify]

### STEP-2: [Name]
...

## Risks
- [risk] → [mitigation]

## Verification
[How to test] feature works]
```

**Review criteria:**
- [ ] All requirements addressed?
- [ ] Dependencies clear?
- [ ] Testable steps?

---

### Dynamic Task Execution (e.g., Implement Phase)

For phases that involve parallel subtasks, agent can leverage `ag task`:

```bash
# Agent generates tasks.md with task list
# Agent creates tasks:
ag task create "implement API endpoint"
ag task create "write unit tests"
ag task create "update documentation"

# Agent spawns workers that claim tasks (fan-out pattern)
# Or uses ag/patterns/fan-out.sh for parallel execution

# After all tasks done, agent calls:
workflow-ctl advance
```

## Workflow State

State is persisted to `.workflow/STATE.json`:

```json
{
  "template": "bugfix",
  "templateName": "Bug Fix",
  "description": "fix login timeout",
  "phases": [
    { "name": "triage", "index": 0, "status": "completed" },
    { "name": "fix", "index": 1, "status": "active" },
    { "name": "verify", "index": 2, "status": "pending" },
    { "name": "ship", "index": 3, "status": "pending" }
  ],
  "currentPhase": 1,
  "status": "in_progress",
  "artifactDir": ".workflow/artifacts/bugfixes/bugfix"
}
```

**Status values:**
- `in_progress` — Workflow running
- `paused` — Workflow paused
- `completed` — All phases done

**Phase status values:**
- `pending` — Not started
- `active` — Currently executing
- `completed` — Finished successfully
- `failed` — Failed (can retry)

## Phase Execution Flow

Agent-driven execution:

1. Agent reads `.workflow/STATE.json` → current phase
2. Agent reads `templates/<template>.md` → phase instructions
3. Agent executes the phase (autonomously decides how)
4. Agent calls `workflow-ctl advance` to update state
5. Repeat until all phases completed

**For phases with parallel tasks:**
1. Agent generates task list
2. Agent calls `ag task create` for each task
3. Agent spawns workers via `fan-out.sh` pattern
4. Workers claim and execute tasks via `ag task claim`
5. Agent calls `workflow-ctl advance` after all tasks done

## State Files

| File | Purpose |
|------|---------|
| `.workflow/STATE.json` | Current workflow state (managed by workflow-ctl) |
| `.workflow/artifacts/` | Phase outputs (SPEC.md, PLAN.md, tasks.md, etc.) |

## Artifact Directory

Artifacts are stored in `.workflow/artifacts/<category>/<template>/`:

```
.workflow/
├── STATE.json
└── artifacts/
    └── features/
        └── feature/
            ├── SPEC.md
            ├── PLAN.md
            ├── tasks.md
            └── implement-output.md
```

## Error Handling

| Error | Recovery |
|-------|----------|
| Phase fails | Agent retries or fixes; state not advanced |
| workflow-ctl fails | State file corruption; restore from backup |
| ag task fails | Agent marks task failed; retry or skip |

## Best Practices

1. **Agent autonomy** — Agent controls phase execution, not scripts
2. **State via tool** — Always use `workflow-ctl advance` (don't manually edit STATE.json)
3. **Parallel tasks** — Use `ag task` + `fan-out.sh` for concurrent work
4. **Check status** — Use `workflow-ctl status` before new work
5. **Commit after phases** — Keep clean git history

## Example: Full Feature Flow

```
/workflow start feature "add user auth"
  ↓
workflow-ctl writes STATE.json {currentPhase: spec}
  ↓
Agent reads STATE.json → Phase 1: Spec
Agent executes Spec phase (creates SPEC.md)
Agent calls workflow-ctl advance
  ↓
STATE.json {currentPhase: plan}
  ↓
Agent executes Plan phase (creates PLAN.md)
Agent calls workflow-ctl advance
  ↓
STATE.json {currentPhase: implement}
  ↓
Agent generates tasks.md
Agent creates tasks: ag task create "implement auth", "write tests"
Agent spawns fan-out workers to execute tasks
Agent calls workflow-ctl advance
  ↓
STATE.json {currentPhase: test}
  ↓
Agent executes tests
Agent calls workflow-ctl advance
  ↓
STATE.json {status: completed}
```

## Anti-Patterns

❌ **Don't start without template** — Use `/workflow start feature` not ad-hoc
❌ **Don't manually edit STATE.json** — Always use `workflow-ctl advance`
❌ **Don't skip phases** — Each phase has a purpose
❌ **Don't ignore failures** — Fix issues before advancing
❌ **Don't run without ag** — Ensure ag binary is built