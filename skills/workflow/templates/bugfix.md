---
id: bugfix
name: Bug Fix
description: Fix bugs with root-cause analysis
phases: [triage, plan, implement]
complexity: low
estimated_tasks: 2-4
skills:
  triage: explore
  plan: plan
  implement: implement
---

# Bug Fix Workflow

## Overview

Bugfix focuses on **root cause analysis** before jumping to solutions.
Skips brainstorm — the problem is already known.

## Phase Sequence

```
triage (explore) → plan → implement
```

### Phase 1: Triage

**Skill:** `explore`

Reproduce the bug and identify root cause:
1. Gather context (error message, when it started, what changed)
2. Create minimal reproduction
3. Trace the code path to root cause
4. Document findings

**Output:** `.workflow/artifacts/bugfixes/[name]/triage.md`

```markdown
# Bug Triage: [title]

## Reproduction
Steps: 1. 2. 3.
Expected: [behavior]
Actual: [behavior]

## Root Cause
[Why it happens, code path, line references]

## Impact
[Who is affected, how often, severity]
```

**Gate:** Root cause identified.

### Phase 2: Plan

**Skill:** `plan`

Create a minimal plan to fix the root cause. This is usually 1-3 tasks.
Keep the plan focused — fix the bug, don't refactor around it.

**Output:** PLAN.yml + PLAN.md

**Gate:** Plan approved.

### Phase 3: Implement

**Skill:** `implement`

Execute the fix. Even for bugfixes, use sub-agents + two-stage review
to prevent introducing new bugs.

After implementation:
1. Verify the original reproduction steps now work
2. Run full test suite
3. Commit

**Output:** Git commit

## Bugfix-Specific Rules

- **Minimal scope** — fix the bug, don't refactor the surrounding code
- **Test first** — write a failing test that reproduces the bug, then fix
- **No scope creep** — if you find related issues, file them separately

## Commit Convention

```
fix(scope): resolve [brief description]
```