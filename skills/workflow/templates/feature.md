---
id: feature
name: Feature Development
description: Develop a new feature from brainstorm to ship
phases: [brainstorm, spec, plan, implement]
complexity: medium
estimated_tasks: 4-8
skills:
  brainstorm: brainstorm
  spec: spec
  plan: plan
  implement: implement
---

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

**Output:** `.workflow/artifacts/features/[name]/design.md`

On approval, run:
```bash
workflow-ctl advance
```

### Phase 2: Spec

**Skill:** `spec`

Write a structured specification from the brainstorm design. Include
prioritized user stories with independent test criteria.

**Gate:** User explicitly approves the spec.

**Output:** `.workflow/artifacts/features/[name]/SPEC.md`

On approval, run:
```bash
workflow-ctl advance
```

### Phase 3: Plan

**Skill:** `plan`

Break SPEC.md into PLAN.yml with tasks, dependencies, and groups.
Uses planner + reviewer worker-judge loop for quality.

**Gate:** User approves the plan.

**Output:**
- `.workflow/artifacts/features/[name]/PLAN.yml`
- `.workflow/artifacts/features/[name]/PLAN.md`

On approval, run:
```bash
workflow-ctl advance
```

### Phase 4: Implement

**Skill:** `implement`

Execute PLAN.md using subagent-driven development:
- Fresh sub-agent per task
- Two-stage review: spec compliance → code quality
- Commit after each group
- Run tests after all groups complete

**Output:** Git commits + impl-report.md

On completion, run:
```bash
workflow-ctl advance  # marks workflow as completed
```

## Template-Specific Rules

### Scope Adaptation

Not every feature needs full fan-out. After plan phase produces the task
breakdown, adapt execution:

| Scope | Tasks | Implement Strategy |
|-------|-------|--------------------|
| Small | 1-2 | Agent executes directly, single commit |
| Medium | 3-6 | Sub-agents per group, serial or light parallel |
| Large | 7+ | Full fan-out with parallel workers per group |

### Skipping Brainstorm

If the user already has a clear, detailed spec:
- User can say "skip brainstorm" → go directly to spec phase
- workflow-ctl advance can be called immediately to skip to spec

### Commit Conventions

```
feat(scope): add [feature]
fix(scope): resolve [issue]
test(scope): add tests for [feature]
docs: update [documentation]
```

## Error Recovery

| Phase | Error | Action |
|-------|-------|--------|
| Brainstorm | User cancels | Terminate workflow |
| Spec | Major scope change | Return to brainstorm |
| Plan | plan-lint fails | Fix YAML, re-lint |
| Plan | Reviewer rejects | Regenerate plan |
| Implement | Sub-agent times out | Retry once, report if persistent |
| Implement | Review fails 2 rounds | Report to user, don't auto-proceed |
| Implement | Tests fail 2 fix cycles | Report to user |