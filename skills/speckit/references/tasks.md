# Task Generation Phase

## Purpose

Create `tasks.md` - an actionable checklist based on `plan.md`.

## Prerequisites

- `spec.md` must exist
- `plan.md` must exist
- Plan must be approved by user

## Process

1. **Read plan.md** for implementation details
2. **Break down** into small, testable tasks
3. **Order by dependency** (foundation first)
4. **Write tasks.md** with the structure below
5. **🔷 PHASE GATE**: Present task summary, get explicit approval

**Do NOT auto-proceed to implementation without user confirmation.**

## Task Format

```markdown
- [ ] T001: Task title (concise, actionable)

  **Description**: What needs to be done.

  **Acceptance Criteria**:
  - [ ] Criterion 1 (measurable, verifiable)
  - [ ] Criterion 2
  - [ ] Criterion 3

  **Dependencies**: T001 (if any)
  **Estimated**: 30m (optional)
```

- `T###`: Task ID (T001, T002, T003, etc.)
- Title: Concise, actionable description
- Acceptance Criteria: Measurable, verifiable, testable
- Dependencies: Other task IDs this task depends on

**See**: [docs/tasks-format.md](../../../../docs/tasks-format.md) for detailed specification.

## tasks.md Template

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

## User Story: [US2 Name]
- [ ] T007 [US2 task]

## Polish
- [ ] T008 [Polish task]
```

## Phase Gate Template

```
---
📝 **tasks.md created!**

**Task breakdown:**
- Setup: X tasks
- Foundation: X tasks
- User Stories: X tasks
- Polish: X tasks

**Total: X tasks** (estimated Y parallel tracks)

**Critical path:** [Key dependencies]

Ready to start implementation? Any tasks to add/remove?
---
```

## Guidelines for Breaking Down Tasks

### Task Size
- Each task should take 15-30 minutes
- If a task is larger, break it down further
- If a task is too small (<5 min), combine with related tasks

### Task Independence
- Tasks should be independently testable
- Each task should produce verifiable output
- Avoid tasks that require multiple steps to validate

### Task Organization
- Group related tasks under headings
- Use Setup → Foundation → User Stories → Polish order
- Mark tasks that can run in parallel with `[P]`

### Task Descriptions
- Be specific: "Add POST /api/users endpoint" not "Add user API"
- Include context when needed: "Update User struct to include ProfileID (foreign key to profiles table)"
- Make it actionable: "Create tests for user registration" not "Consider testing user registration"

## Task ID Convention

- Use sequential IDs: T001, T002, T003, etc.
- Restart numbering when reorganizing
- Keep IDs within a single spec/planning session

## Common Mistakes

- ❌ Tasks too large or vague
- ❌ Missing dependencies between tasks
- ❌ Not marking parallelizable tasks
- ❌ Forgetting setup and polish tasks
- ❌ Tasks that can't be independently tested
- ❌ Skipping foundation tasks and jumping to user stories