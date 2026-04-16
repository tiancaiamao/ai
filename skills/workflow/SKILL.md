---
name: workflow
description: Orchestrate a full development flow by composing brainstorm → spec → plan → implement skills. State persisted to disk via workflow-ctl.
---

# Workflow — Skill Orchestrator

Workflow composes smaller skills into a complete development flow.
It manages state transitions and coordinates skill handoffs.

**Workflow does NOT implement logic itself.** It delegates to:

| Skill | Responsibility |
|-------|---------------|
| `brainstorm` | Explore requirements, produce validated design |
| `spec` | Write structured SPEC.md |
| `plan` | Break spec into PLAN.yml + PLAN.md |
| `implement` | Execute plan with sub-agents + two-stage review |

## Templates

Templates define which skills to compose and in what order:

| Template | Flow | Use Case |
|----------|------|----------|
| `feature` | brainstorm → spec → plan → implement | New feature |
| `bugfix` | explore → plan → implement | Bug fix |
| `spike` | brainstorm → (document) | Research/exploration |
| `refactor` | explore → plan → implement → explore | Code restructuring |
| `hotfix` | implement (fast path) | Emergency fix |
| `security` | explore → plan → implement → explore | Security audit |

Templates are thin: they only define the skill sequence and any
template-specific rules (e.g., bugfix skips brainstorm and goes
straight to exploration + plan).

See `templates/` for per-template definitions.

## Architecture

```
/workflow start feature "X"
    ↓
workflow-ctl start → write STATE.json
    ↓
Agent reads STATE.json → current phase
    ↓
Agent invokes the corresponding skill
    ↓
Skill completes → workflow-ctl advance
    ↓
Next phase → next skill
    ↓
All phases done → workflow-ctl complete
```

## Commands

```bash
# Start a workflow
workflow-ctl start <template> "[description]"

# After each skill completes
workflow-ctl advance

# Check status
workflow-ctl status

# Pause / resume
workflow-ctl pause
workflow-ctl resume
```

## State

Persisted to `.workflow/STATE.json`:

```json
{
  "template": "feature",
  "description": "add user auth",
  "phases": [
    { "name": "brainstorm", "status": "completed" },
    { "name": "spec", "status": "completed" },
    { "name": "plan", "status": "active" },
    { "name": "implement", "status": "pending" }
  ],
  "currentPhase": 2,
  "status": "in_progress",
  "artifactDir": ".workflow/artifacts/features/feature"
}
```

**Workflow status:** `in_progress` | `paused` | `completed`
**Phase status:** `pending` | `active` | `completed` | `failed`

## Artifact Directory

```
.workflow/
├── STATE.json
└── artifacts/
    └── features/
        └── feature/
            ├── design.md        ← brainstorm output
            ├── SPEC.md          ← spec output
            ├── PLAN.yml         ← plan output (structured)
            ├── PLAN.md          ← plan output (rendered)
            └── impl-report.md   ← implement output
```

## How Each Phase Works

### Phase: Brainstorm

Agent invokes the `brainstorm` skill:
- Conducts dependency inversion interview
- Explores codebase if needed (via `explore` skill)
- Produces validated design

**Output:** `design.md` in artifact dir

### Phase: Spec

Agent invokes the `spec` skill:
- Reads brainstorm design
- Writes structured SPEC.md with prioritized user stories
- Gets user approval

**Output:** `SPEC.md` in artifact dir

### Phase: Plan

Agent invokes the `plan` skill:
- Reads SPEC.md (and CONTEXT.md if exists)
- Uses planner persona + reviewer in worker-judge loop
- Produces PLAN.yml + PLAN.md

**Output:** `PLAN.yml` + `PLAN.md` in artifact dir

### Phase: Implement

Agent invokes the `implement` skill:
- Spawns sub-agents per task
- Two-stage review (spec compliance + code quality)
- Commits after each group
- Run tests after all groups complete

**Output:** git commits + `impl-report.md`

## Skipping Phases

Some templates skip phases:

- **bugfix** → skips brainstorm and spec; goes straight to explore → plan → implement
- **hotfix** → skips everything; just implement
- **spike** → brainstorm only, maybe document

The template defines what to skip. The agent follows the template.

## Anti-Patterns

❌ Don't start without template — always `/workflow start <template>`
❌ Don't manually edit STATE.json — always use `workflow-ctl`
❌ Don't skip skills — each has a purpose
❌ Don't ignore failures — fix before advancing
❌ Don't let workflow do the work — delegate to skills

## Tooling

- **workflow-ctl** (`bin/workflow-ctl`) — state management
- **plan-lint** (in `plan` skill) — validate PLAN.yml
- **plan-render** (in `plan` skill) — render PLAN.yml → PLAN.md
- **pair.sh** (in `ag` skill) — worker-judge loop pattern