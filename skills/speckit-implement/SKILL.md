---
name: speckit-implement
description: INTERNAL - Used by speckit. Do not invoke directly. Execute tasks from tasks.md checklist.
license: MIT
---

# Task Implementation

⚠️ **This is an internal phase of speckit. Use `speckit` command instead of invoking this directly.**

You are implementing tasks from a checklist. This is the execution phase of the spec-driven development workflow.

## Prerequisites

Ensure you have:
- A task file (`tasks.md`) with checklist items
- Implementation plan (`plan.md`) for technical guidance
- Feature specification (`spec.md`) for requirements context

## Workflow

### Step 1: Checklist Validation

Before starting, verify:
1. All pending tasks have valid IDs (T001, T002...)
2. Each task has a checkbox (`[ ]` or `[X]`)
3. Dependencies are respected (don't skip ahead)

### Step 2: Project Setup Verification

Ensure development environment is ready:
- [ ] Dependencies installed
- [ ] Build passes
- [ ] Tests pass (baseline)

### Step 3: Task Execution

For each task:

1. **Identify Next Task**: Find first unchecked task
2. **Understand Context**: Review related spec and plan sections
3. **Implement**: Use test-driven-development skill to write code
4. **Verify**: Run tests, check behavior
5. **Mark Complete**: Update `[ ]` to `[X]` in tasks.md

### Step 4: Progress Tracking

After each task:
```
✓ T001 Create project structure
  - Created cmd/, internal/, pkg/ directories
  - Initialized go.mod
```

## Implementation Guidelines

### Code Quality

- Follow existing project patterns
- Write self-documenting code
- Add comments for complex logic
- Keep functions focused and small

### Testing

- Use test-driven-development skill
- Run tests after each task
- Ensure no regression in existing tests

### Commits

Make atomic commits per task:

```bash
git commit -m "T001: Create project structure

- Add cmd/app/main.go entry point
- Initialize go.mod"
```

## Error Handling

If you encounter blockers:

1. **Document the issue**: Add note in tasks.md
2. **Skip if safe**: Move to next independent task
3. **Ask for help**: If blocking, pause and request clarification

## Completion

When all tasks are complete:

1. Run full test suite
2. Verify all acceptance criteria from spec
3. Update documentation
4. Create summary of changes

---

**User's Task File:**

$ARGUMENTS