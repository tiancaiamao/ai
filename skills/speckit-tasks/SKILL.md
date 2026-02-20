---
name: speckit-tasks
description: INTERNAL - Used by speckit. Do not invoke directly. Creates tasks.md from plan.md.
license: MIT
---

# Task Generation Phase

⚠️ **This is an internal phase of speckit. Use `speckit` command instead of invoking this directly.**

## Purpose

Create `tasks.md` - an actionable checklist based on `plan.md`.

## Prerequisites

- `spec.md` must exist
- `plan.md` must exist

## Output

Create `tasks.md` with:

```markdown
# Tasks: [Feature Name]

## Setup
- [ ] T001 [Setup task]
- [ ] T002 [Setup task]

## Foundation
- [ ] T003 [Foundation task]
- [ ] T004 [P] [Parallelizable foundation task]

## User Story: [US1 Name]
- [ ] T005 [US1 task]
- [ ] T006 [US1 task]

## Polish
- [ ] T007 [Polish task]
```

## Task Format

```markdown
- [ ] T### [P] Task description
```

- `T###`: Task ID (T001, T002, etc.)
- `[P]`: Optional, marks parallelizable tasks
- Description should be specific and actionable

## Process

1. **Read plan.md** for implementation details
2. **Break down** into small, testable tasks
3. **Order by dependency** (foundation first)
4. **Write tasks.md**

## Guidelines

- Each task should take 15-30 minutes
- Tasks should be independently testable
- Group related tasks under headings
- Mark tasks that can run in parallel

---

**User's Request:**

$ARGUMENTS