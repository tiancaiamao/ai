# Feature Development Workflow

## Overview

Feature workflow composes four skills in sequence. The agent orchestrates
handoffs between skills and manages state via `workflow-ctl`.

## Phase Sequence

```
brainstorm → spec → plan → implement
```

### Phase 1: Brainstorm

**Skill:** `brainstorm`

Conduct dependency inversion interview. Understand what the user wants.
Produce a validated design.

**Gate:** User explicitly approves the design.

**Output:** `design.md` in artifact directory

### Phase 2: Spec

**Skill:** `spec`

Write a structured specification from the brainstorm design. Include
prioritized user stories with independent test criteria.

**Gate:** User explicitly approves the spec.

**Output:** `SPEC.md` in artifact directory

### Phase 3: Plan

**Skill:** `plan`

Break SPEC.md into PLAN.yml with tasks, dependencies, and groups.
Uses planner + reviewer worker-judge loop for quality.

**Gate:** User approves the plan.

**Output:** `PLAN.yml` + `PLAN.md` in artifact directory

### Phase 4: Implement

**Skill:** `implement`

Execute PLAN.md using subagent-driven development:
- Fresh sub-agent per task
- Two-stage review: spec compliance → code quality
- Commit after each group
- Run tests after all groups complete

**Output:** Git commits + `impl-report.md`

## Scope Adaptation

After plan phase produces the task breakdown, adapt execution:

| Scope | Tasks | Strategy |
|-------|-------|----------|
| Small | 1-2 | Agent executes directly, single commit |
| Medium | 3-6 | Sub-agents per group, serial or light parallel |
| Large | 7+ | Full fan-out with parallel workers |

`workflow-ctl plan-lint` outputs a strategy recommendation automatically.

## Skipping Brainstorm

If the user already has a clear, detailed spec:
- User can say "skip brainstorm" → run `workflow-ctl advance` then `workflow-ctl advance` to go directly to spec phase
- Or use `workflow-ctl start bugfix "..."` which skips brainstorm

## Error Recovery

| Phase | Error | Action |
|-------|-------|--------|
| Brainstorm | User cancels | Terminate workflow |
| Spec | Major scope change | `workflow-ctl back` to brainstorm |
| Plan | plan-lint fails | Fix YAML, re-lint |
| Plan | Reviewer rejects | Regenerate plan |
| Implement | Sub-agent times out | Retry once, report if persistent |
| Implement | Review fails 2 rounds | Report to user, don't auto-proceed |
| Implement | Tests fail 2 fix cycles | Report to user |

## Commit Conventions

```
feat(scope): add [feature]
fix(scope): resolve [issue]
test(scope): add tests for [feature]
docs: update [documentation]
```