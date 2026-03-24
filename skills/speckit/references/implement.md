# Task Implementation

## Purpose

Execute tasks from `tasks.md` checklist systematically. This is the execution phase of the spec-driven development workflow.

## Prerequisites

- A task file (`tasks.md`) with checklist items
- Implementation plan (`plan.md`) for technical guidance
- Feature specification (`spec.md`) for requirements context

## Execution Options

You have two ways to execute tasks:

### Option 1: Orchestrate Loop (Recommended)

Use orchestrate loop mode for automated execution with worker → task-checker verification.

**When to use**:
- ✅ Well-defined tasks with clear acceptance criteria
- ✅ Want to save time on routine implementation
- ✅ Tasks are straightforward and independent
- ✅ You've approved the task list and want to proceed automatically

**When NOT to use**:
- ❌ Tasks requiring careful oversight or learning
- ❌ Debugging tricky issues that need your direct attention
- ❌ First-time work in unfamiliar codebase areas

**Usage**:
```bash
# Full automatic execution
~/.ai/skills/orchestrate/bin/orchestrate.sh

# Or use the auto-execute skill
/skill:auto-execute

# Execute one task at a time
~/.ai/skills/orchestrate/bin/orchestrate.sh next
```

**Progress Monitoring**:
Use `llm_context_update` to track execution progress:
```markdown
## Auto-Execution Progress
- Phase: executing
- Total: 5
- Done: 2
- In Progress: TASK003
```

**How it works**:
1. Reads `tasks.md`
2. For each task:
   - Spawns a worker subagent to implement
   - Spawns a task-checker subagent to verify
   - Loops up to 3 times if fixes are needed
   - Marks task as done/failed
3. Main agent context stays clean throughout
4. Progress updated via `llm_context_update`

**Benefits**:
- ✅ Automated execution - save time
- ✅ Worker subagent has isolated context - avoid context bloat
- ✅ Task-checker verifies completion - quality control
- ✅ Main agent supervises, doesn't execute - strategic oversight

### Option 2: Manual Execution

Execute tasks one by one with your supervision. This gives you full control and visibility.

**When to use**:
- Complex tasks requiring careful oversight
- Learning codebase
- Debugging tricky issues

**See**: [Manual Execution](#manual-execution) below

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
- ❌ Forgetting to mark tasks as complete
- ❌ Not documenting blockers or edge cases