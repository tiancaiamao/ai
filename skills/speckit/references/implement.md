# Task Implementation

## Purpose

Execute tasks from `tasks.md` checklist systematically. This is the execution phase of the spec-driven development workflow.

## Prerequisites

- A task file (`tasks.md`) with checklist items
- Implementation plan (`plan.md`) for technical guidance
- Feature specification (`spec.md`) for requirements context

## Execution Options

You have two ways to execute tasks:

### Option 1: Manual Execution (Default)

Execute tasks one by one with your supervision. This gives you full control and visibility.

**When to use**:
- Complex tasks requiring careful oversight
- Learning the codebase
- Debugging tricky issues

**See**: [Manual Execution](#manual-execution) below

### Option 2: Orchestrate Loop (Recommended)

Use the orchestrate loop mode for automated execution with worker → task-checker verification.

**When to use**:
- Well-defined tasks with clear acceptance criteria
- Want to save time
- Tasks are straightforward

**Usage**:
```bash
~/.ai/skills/orchestrate/bin/orchestrate.sh
```

**How it works**:
1. Reads `tasks.md`
2. For each task:
   - Spawns a worker subagent to implement
   - Spawns a task-checker subagent to verify
   - Loops up to 3 times if fixes are needed
   - Marks task as done/failed
3. Main agent context stays clean throughout

**Benefits**:
- ✅ Automated execution
- ✅ Worker subagent has isolated context
- ✅ Task-checker verifies completion
- ✅ Main agent supervises, doesn't execute

---

## Manual Execution

Use this for manual, supervised execution of tasks.

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
5. **Mark Complete**: ⚠️ **MANDATORY** — Update `[ ]` to `[X]` in tasks.md immediately after verification

### ⚠️ CRITICAL: Always Update tasks.md Progress

**After completing EACH task, you MUST:**

```
1. Verify the task works (tests pass, behavior correct)
2. IMMEDIATELY edit tasks.md
3. Change [ ] → [X] for that task
4. Save the file
```

**Do NOT:**
- ❌ Complete multiple tasks before updating tasks.md
- ❌ Rely on memory to batch-update later
- ❌ Skip this step because "it's tracked elsewhere"

**Why this matters:**
- tasks.md is the source of truth for progress
- User needs visibility into what's done
- Context loss between tasks can cause confusion

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

**Stop and ask** if:
- Blocked by unclear requirement
- Found unexpected issue
- Major decision needed

## Completion

When all tasks are complete:

1. Run full test suite
2. Verify all acceptance criteria from spec
3. Update documentation
4. Create summary of changes

## Common Mistakes

- ❌ Skipping validation and starting implementation directly
- ❌ Not running tests after each task
- ❌ Making changes out of dependency order
- ❌ Not verifying spec requirements during implementation
- ❌ **Forgetting to mark tasks as complete in tasks.md** — This is the #1 mistake!
- ❌ Not documenting blockers or edge cases