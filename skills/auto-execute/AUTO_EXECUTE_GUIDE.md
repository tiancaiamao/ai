# Auto-Execute Skill Documentation

## Overview

The **auto-execute** skill provides automated task execution using subagent orchestration. It wraps the `orchestrate.sh` workflow for easy integration with your development workflow.

## When to Use

Use auto-execute when:

✅ You have a `tasks.md` file with well-defined tasks
✅ Tasks have clear acceptance criteria
✅ You want to save time on routine implementation
✅ Tasks are straightforward and independent

Do NOT use when:

❌ Tasks require careful oversight or learning
❌ Debugging tricky issues
❌ First-time work in unfamiliar codebase areas

## How It Works

The auto-execute skill orchestrates the following workflow:

```
tasks.md
  ↓
[Read next task]
  ↓
[Spawn worker subagent]
  - Task: Implement the task
  - Persona: worker.md
  ↓
[Spawn task-checker subagent]
  - Task: Verify completion
  - Persona: task-checker.md
  ↓
[Update task status]
  - Success → [X] done
  - Failed → [!] failed
  ↓
[Repeat until all tasks done]
```

## Usage

### Basic Usage

Execute all tasks in `tasks.md`:

```bash
/skill:auto-execute
```

### With Parameters

Execute with options:

```bash
/skill:auto-execute stop_after=2 dry_run=true
```

**Parameters**:
- `stop_after=N` - Stop after completing N tasks (default: execute all)
- `dry_run=true` - Show what would be done without executing (default: false)

### Progress Monitoring

The skill automatically updates progress using `llm_context_update`:

```markdown
## Auto-Execute Progress
- Phase: executing
- Total: 5
- Done: 2
- In Progress: TASK003
```

To check progress at any time:

```
/task progress
```

## Task Status Convention

The auto-execute workflow uses the following status markers:

| Status | Marker | Meaning |
|--------|--------|---------|
| Pending | `[ ]` | Not started |
| In Progress | `[-]` | Currently executing |
| Done | `[X]` | Completed successfully |
| Failed | `[!]` | Execution failed |

## Example Tasks File

```markdown
# Project Setup Tasks

## Foundation
- [ ] T001: Create project structure
- [ ] T002: Initialize go.mod

## Implementation
- [ ] T003: Implement core module
- [ ] T004: Add tests

## Documentation
- [ ] T005: Write README
```

## Worker Agent Persona

The worker subagent uses a focused persona:

**Role**: Task Implementation Specialist

**Responsibilities**:
- Read and understand the task requirements
- Implement the solution step by step
- Write tests (if applicable)
- Verify the implementation works

**Constraints**:
- Work within the task scope
- Don't make unnecessary changes
- Follow project conventions

## Task-Checker Agent Persona

The task-checker subagent verifies task completion:

**Role**: Quality Assurance Specialist

**Responsibilities**:
- Review the implementation
- Verify all acceptance criteria are met
- Run tests (if applicable)
- Report any issues found

**Decision**:
- If complete → approve → task marked as [X]
- If incomplete → reject → worker tries again (max 3 attempts)
- If failed → task marked as [!] → return to user

## Error Handling

### Task Fails

When a task fails:

1. The task-checker identifies the issue
2. Worker subagent retries (up to 3 times)
3. If still failing → task marked as [!]
4. Execution stops and returns to you with error details

### Example Failure Output

```
❌ TASK003: Implement core module
   Error: Tests failing - expected function not found
   Max retries (3) exceeded
   Returning to user for manual intervention
```

### User Intervention

When a task fails, you can:

1. **Review the issue**: Check the task-checker's findings
2. **Fix manually**: Address the problem yourself
3. **Update task**: Modify the task if requirements were unclear
4. **Resume**: Continue execution with remaining tasks

## Integration with Speckit Workflow

The auto-execute skill fits into the speckit workflow:

```
spec.md (Phase 1)
  ↓
plan.md (Phase 2)
  ↓
tasks.md (Phase 3)
  ↓
auto-execute (Phase 4) ← Use /skill:auto-execute here
  ↓
completion summary
```

## Advanced Usage

### Stop After N Tasks

Execute a few tasks, then review progress:

```bash
/skill:auto-execute stop_after=3
```

After completing 3 tasks:
- Stops execution
- Shows progress summary
- You can review and continue if needed

### Dry Run Mode

Preview what will be executed:

```bash
/skill:auto-execute dry_run=true
```

Output:
```
[DRY RUN] Would execute:
  - T001: Create project structure
  - T002: Initialize go.mod
  - T003: Implement core module
```

### Continue After Failure

After manually fixing a failed task, continue with remaining tasks:

```bash
/skill:auto-execute
```

The skill automatically skips tasks marked as [X].

## Troubleshooting

### Orchestrate Script Not Found

**Error**: `~/.ai/skills/orchestrate/bin/orchestrate.sh not found`

**Solution**:
```bash
# Install orchestrate skill
mkdir -p ~/.ai/skills/orchestrate
# ... (copy orchestrate.sh and dependencies)
```

### Tasks File Not Found

**Error**: `tasks.md not found in current directory`

**Solution**:
```bash
# Create tasks file using speckit
/skill:speckit phase=plan
```

### Worker Agent Timeout

**Error**: `Worker agent timeout after 10 minutes`

**Solution**:
- Break the task into smaller tasks
- Check if the task is too complex for a single worker
- Consider manual execution for complex tasks

## See Also

- **Orchestrate Skill**: `/Users/genius/.ai/skills/orchestrate/SKILL.md`
- **Subagent Skill**: `/Users/genius/.ai/skills/subagent/SKILL.md`
- **Speckit Skill**: `/Users/genius/.ai/skills/speckit/SKILL.md`
- **Worker Persona**: `skills/orchestrate/references/worker.md`
- **Task-Checker Persona**: `skills/orchestrate/references/task-checker.md`