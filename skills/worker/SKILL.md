---
name: worker
description: Execute implementation tasks from tasks.md with isolation and verification. Use parallel.sh or chain.sh for task execution.
allowed-tools: [bash, read, write, edit]
---

# Worker Skill

Execute implementation tasks from `tasks.md` using isolated subagent processes.

## Workflow

```
1. Read tasks.md for pending tasks
2. Select task(s) to execute
3. Choose execution mode:
   - parallel.sh: Independent tasks in parallel
   - chain.sh: Sequential tasks with data flow
4. Execute with verification (compile/test)
5. Auto-update tasks.md status ← NEW!
```

## Auto-Update tasks.md

Worker scripts now **automatically update** task status in tasks.md:

| Status | Checkbox | When |
|--------|----------|------|
| pending | `[ ]` | Before execution |
| in_progress | `[-]` | Task started |
| done | `[X]` | Task succeeded |
| failed | `[!]` | Task failed/timeout |

Pass `-f <tasks.md>` to enable auto-update:

```bash
# Parallel mode with auto-update
~/.ai/skills/worker/bin/parallel.sh \
    -f tasks.md \
    -n 2 \
    "Task 1: Implement feature X" \
    "Task 2: Write tests for feature X"

# Chain mode with auto-update
~/.ai/skills/worker/bin/chain.sh \
    -f tasks.md \
    "Task 1: Generate code skeleton" \
    "Task 2: Review and refine - {previous}"
```

## Execution Modes

### Parallel Mode (independent tasks)

```bash
~/.ai/skills/worker/bin/parallel.sh \
    -n 2 \
    -p @worker.md \
    -t 10m \
    "Task 1: Implement feature X" \
    "Task 2: Write tests for feature X"
```

### Chain Mode (sequential with data flow)

```bash
~/.ai/skills/worker/bin/chain.sh \
    -p @worker.md \
    -t 10m \
    "Task 1: Generate code skeleton" \
    "Task 2: Review and refine - {previous}"
```

## Task Format (tasks.md)

```markdown
## Task 1: Implement Feature X
- Status: todo
- Priority: high
- Dependencies: []
- Assignee: worker

## Task 2: Write Tests
- Status: todo
- Priority: high
- Dependencies: [Task 1]
- Assignee: worker
```

## Manual Update (if not using -f)

```bash
# Or use update_tasks.sh directly
~/.ai/skills/worker/bin/update_tasks.sh tasks.md "Task name" done
```

## Verification

Always verify after implementation:

```bash
# Compile check
go build ./...

# Run tests
go test ./... -short

# Lint check
golangci-lint run
```

## Error Handling

| Scenario | Action |
|----------|--------|
| Task fails | Retry once, then mark failed |
| Timeout | Kill session, mark timeout |
| Partial output | Save to /tmp for debugging |
| All tasks fail | Report to orchestrator |

## Output Convention

Worker outputs to `/tmp/worker-*.txt`:
- `/tmp/worker-output.txt` - main output
- `/tmp/worker-errors.txt` - errors and warnings

## Usage Example

```bash
# Read tasks
cat tasks.md

# Execute first todo task
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
    /tmp/worker-output.txt \
    15m \
    @worker.md \
    "Read tasks.md and implement Task 1")

tmux_wait.sh "$(echo $SESSION | cut -d: -f1)" 900

# Verify
go build ./... && go test ./... -short

# Update status
sed -i 's/Status: todo/Status: done/' tasks.md
```

## Notes

- MAX_PARALLEL = 2 (avoid rate limits)
- Prefer chain mode for dependent tasks
- Always verify after implementation
- Clean up tmux sessions on failure
