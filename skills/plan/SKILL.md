---
name: plan
description: Break a SPEC.md into an actionable implementation plan with tasks, dependencies, and groups. Uses worker-judge loop (planner + reviewer) for quality.
---

# Plan

Transform a specification into a structured implementation plan.

## When to Use

- After spec is approved (from the `spec` skill)
- When user says "write a plan" or "plan this"
- As Phase 3 of a feature workflow

## Input

- `SPEC.md` (required)
- `CONTEXT.md` (exploration results, if exists — do NOT re-explore)

## The Planning Process

### Step 1: Read Inputs

Read SPEC.md. If CONTEXT.md exists from a prior exploration phase, read it.
Do NOT spawn new explorers — reuse existing context.

### Step 2: Generate PLAN.yml

Use the planner persona (`prompts/planner.md`) to produce a structured plan.

**PLAN.yml structure:**

```yaml
version: "1.0"
metadata:
  spec_file: "SPEC.md"

tasks:
  - id: "T001"
    title: "Task title"
    description: "What to do (actionable, specific)"
    priority: high|medium|low
    estimated_hours: 2
    dependencies: []
    file: "path/to/target.go"
    done: false

groups:
  - name: "group-name"
    title: "Group Title"
    tasks: ["T001", "T002"]
    commit_message: "feat(scope): description"

group_order: ["group-name"]
risks:
  - area: "Area"
    risk: "What could go wrong"
    mitigation: "How to prevent it"
```

### Step 3: Validate

Run the validation pipeline:

```bash
plan-lint PLAN.yml
```

If lint fails, fix YAML issues and re-run until clean.

### Step 4: Review via Worker-Judge Loop

Use `pair.sh` with planner + reviewer personas (max 3 rounds):

```bash
~/.ai/skills/ag/patterns/pair.sh \
  "$(cat ~/.ai/skills/plan/prompts/planner.md)" \
  "$(cat ~/.ai/skills/plan/prompts/reviewer.md)" \
  SPEC.md \
  3
```

Reviewer checks:
- All SPEC requirements covered by tasks
- Dependencies are correct (no cycles, no missing IDs)
- Group order respects dependencies
- Test tasks included

### Step 5: Render

```bash
plan-render PLAN.yml > PLAN.md
```

### Step 6: Present to User

Show PLAN.md summary. Get approval before proceeding.

## Task Granularity Rules

| Size | Hours | Action |
|------|-------|--------|
| Too big | > 6h | Break down further |
| Just right | 2-4h | Keep as is |
| Too small | < 1h | Combine with related work |

## Grouping Principles

Group tasks by **user story / business value**, not by technical layer.
Each group should produce a working increment.

❌ Bad: "models group" → "services group" → "API group"
✅ Good: "registration flow" → "email verification" → "activation"

## Scope Adaptation

| Scope | Tasks | Strategy |
|-------|-------|----------|
| Small | 1-2 | Agent executes directly |
| Medium | 3-6 | Group by story, serial or light parallel |
| Large | 7+ | Full fan-out with parallel workers per group |

## Output

- `PLAN.yml` — machine-parseable plan
- `PLAN.md` — human-readable rendered plan

## Skill Composition

```
spec → plan (this skill) → implement
           or
direct requirements → plan → implement
```

## Tools

- `bin/plan-lint` — validate PLAN.yml
- `bin/plan-render` — render PLAN.yml → PLAN.md
- `prompts/planner.md` — planner persona
- `prompts/reviewer.md` — plan reviewer persona