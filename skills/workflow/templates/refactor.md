---
id: refactor
name: Refactor
description: Restructure code without changing behavior
phases: [assess, plan, implement, verify]
complexity: medium
estimated_tasks: 3-6
skills:
  assess: explore
  plan: plan
  implement: implement
  verify: explore
---

# Refactor Workflow

## Overview

Restructure code while preserving behavior. The key risk is accidental
behavior change — tests are your safety net.

## Phase Sequence

```
assess (explore) → plan → implement → verify (explore)
```

### Phase 1: Assess

**Skill:** `explore`

Understand what exists:
1. Map the current structure
2. Identify what needs to change and why
3. Identify what must NOT change (public API, behavior)
4. Check existing test coverage — if low, add characterization tests first

**Output:** `.workflow/artifacts/refactors/[name]/assessment.md`

**Gate:** Clear understanding of scope and constraints.

### Phase 2: Plan

**Skill:** `plan`

Plan the restructuring. Key rule: each task should be small enough that
if something breaks, it's easy to identify which change caused it.

**Output:** PLAN.yml + PLAN.md

**Gate:** Plan approved.

### Phase 3: Implement

**Skill:** `implement`

Execute the refactoring. Run tests after EACH task — if tests break,
the last change is the culprit.

**Output:** Git commits

### Phase 4: Verify

**Skill:** `explore`

After all changes:
1. Run full test suite
2. Verify public API unchanged
3. Check for accidental behavior changes
4. Review final code structure matches intent

**Output:** verification-report.md

## Refactor-Specific Rules

- **Preserve behavior** — refactoring must not change what the code does
- **Small steps** — each commit should be independently revertable
- **Tests green always** — if tests go red, stop and fix before continuing
- **Characterization tests first** — if coverage is low, add tests BEFORE refactoring

## Commit Convention

```
refactor(scope): [what changed and why]
```