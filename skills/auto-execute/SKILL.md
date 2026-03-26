---
name: auto-execute
description: Automatically execute tasks from tasks.md using orchestrate.sh with progress tracking. Use when you have a tasks.md file ready and want to execute all tasks automatically without manual intervention.
---

# Auto-Execute Tasks

This skill automatically executes tasks from `tasks.md` using the orchestrate system, with progress tracking via `task_tracking`.

## When to Use

- After `speckit` has created a `tasks.md` file
- When the user says "execute the tasks", "run the workflow", "auto-execute"
- After the user has approved the task list

## Prerequisites

- `tasks.md` must exist in the current directory
- Tasks should be reviewed and approved by the user
- `~/.ai/skills/orchestrate/bin/orchestrate.sh` must be available

## ⚠️ Pre-flight Checks (MANDATORY)

**Before starting auto-execution, verify:**

1. **Explore complete?** - For complex tasks, was explore phase done?
2. **Requirements clarified?** - For architecture changes, was design direction confirmed?
3. **Plan approved?** - User explicitly approved the plan?
4. **Tasks approved?** - User explicitly approved the task list?

**If any check fails, ask user:**
```
Before auto-execution, I need to confirm:
- [ ] Design direction is clear (especially for architecture changes)
- [ ] Plan was reviewed and approved
- [ ] Tasks list was reviewed and approved

Should I proceed, or do you want to review first?
```

**Architecture changes MUST have explicit confirmation:**
- Multiple packages/layers involved
- "Unify", "consolidate", "refactor", "integrate" keywords
- Changes to core modules

## Process

### 1. Check Prerequisites

First, verify the workflow can start:

```bash
# Check if tasks.md exists
ls -la tasks.md

# Check current status
~/.ai/skills/orchestrate/bin/orchestrate.sh status
```

### 2. Initialize Progress Tracking

Before starting execution, initialize task tracking:

```
Use task_tracking to record:
- Current phase: "auto_execute"
- Target: tasks.md
- Total task count
- Starting timestamp
```

Example:
```markdown
## Current Task
- Auto-executing tasks from tasks.md
- Status: Starting
- Total tasks: 5
- Next: Execute first pending task
```

### 3. Run Auto-Execution

Determine execution mode based on user request:

**Mode A: Full Auto (default)**
```bash
# Run all tasks automatically
~/.ai/skills/orchestrate/bin/orchestrate.sh
```

**Mode B: Stop After N Tasks**
```bash
# Execute N tasks, then stop for approval
for i in $(seq 1 $N); do
    ~/.ai/skills/orchestrate/bin/orchestrate.sh next
    # Report progress
    ~/.ai/skills/orchestrate/bin/orchestrate.sh status
    # Check if more tasks remain
    remaining=$(grep -c "^- \[ \]" tasks.md)
    if [ $remaining -eq 0 ]; then
        echo "All tasks completed!"
        break
    fi
done

# Stop and ask for approval
echo "Completed $N tasks. Remaining: $remaining"
echo "Continue with remaining tasks? (yes/no)"
```

**Mode C: One Task at a Time**
```bash
# Execute next task, then report back
~/.ai/skills/orchestrate/bin/orchestrate.sh next
```

### 4. Monitor Progress

After each task or periodically, check status:

```bash
# Check current progress
~/.ai/skills/orchestrate/bin/orchestrate.sh status
```

Update `task_tracking` with:
- Completed task count
- Current in-progress task
- Any errors or failures
- Next action

### 5. Handle Errors

If a task fails:

1. Check the task output in `/tmp/orchestrate-task-*.txt`
2. Review task-checker feedback in `/tmp/orchestrate-check-*.txt`
3. Update `task_tracking` with error details
4. Ask user if they want to:
   - Retry the task
   - Skip the task
   - Fix manually and continue

### 6. Completion

When all tasks are done:

- Mark the task as `[X]` in tasks.md (done by orchestrate)
- Update `task_tracking` with completion summary
- Provide summary of what was accomplished
- Report any issues or follow-up tasks needed

## Progress Tracking Template

Use this template in `task_tracking`:

```markdown
## Auto-Execution Progress

**Phase**: auto_execute
**Tasks File**: tasks.md
**Started**: 2025-01-15T10:00:00Z

### Progress
- Total: 5
- Done: 2
- Pending: 2
- Failed: 1
- In Progress: TASK003

### Current Task
- ID: TASK003
- Description: Implement user authentication
- Status: in_progress

### Issues
- TASK005: Database connection failed
  - Error: Connection timeout
  - Status: User reviewing

### Next Action
- Monitor TASK003 completion
- Address TASK005 issue when ready
```

## Task Status Convention

The orchestrate system uses these checkboxes in tasks.md:

| Checkbox | Status | Meaning |
|-----------|---------|---------|
| `[ ]` | pending | Not yet started |
| `[-]` | in_progress | Currently executing |
| `[X]` | done | Successfully completed |
| `[!]` | failed | Failed execution |

## Example Session Flow

```
User: "Execute the tasks"

Agent:
1. Read tasks.md
2. Check orchestrate status
3. Update task_tracking: "Starting auto-execution, 5 tasks total"
4. Run: ~/.ai/skills/orchestrate/bin/orchestrate.sh
5. Monitor progress periodically
6. Update task_tracking after each task
7. On completion: "All 5 tasks done successfully"

User: "Check progress"

Agent:
1. Run: ~/.ai/skills/orchestrate/bin/orchestrate.sh status
2. Parse output
3. Update task_tracking with current status
4. Report to user
```

## Success Criteria

Auto-execution is successful when:
- ✅ All tasks marked as `[X]` in tasks.md (orchestrate handles this automatically)
- ✅ No tasks marked as `[!]` (failed)
- ✅ Progress tracked via task_tracking
- ✅ User notified of completion
- ✅ Summary of accomplishments provided

## ⚠️ IMPORTANT: tasks.md is Source of Truth

The `tasks.md` file is the **source of truth** for task progress.

**Orchestrate automatically updates tasks.md:**
- When a task starts: `[ ]` → `[-]`
- When a task completes: `[-]` → `[X]`
- When a task fails: `[-]` → `[!]`

**You do NOT need to manually edit tasks.md** when using orchestrate.
The `update_tasks.sh` script handles checkbox updates.

## Error Recovery

| Situation | Action |
|-----------|--------|
| Task fails during execution | Check error logs, update task_tracking, ask user |
| Orchestrate script error | Verify dependencies, check permissions, retry |
| Worker timeout | Check task complexity, may need manual intervention |
| Task-checker rejects task | Worker will auto-retry (up to 3 cycles), then fail |

## Integration with speckit

This skill works as the final stage of the speckit workflow:

```
speckit (spec) → speckit (plan) → speckit (tasks) → auto-execute
```

The user approves each stage before proceeding to the next.