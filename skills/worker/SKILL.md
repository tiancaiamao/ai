name: worker
description: Execute implementation tasks from tasks.md with isolation and verification. Use parallel.sh or chain.sh for task execution.
tools: [bash, read, write, edit]
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
5. Update tasks.md status
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

## Update tasks.md

After task completion, update status:

```bash
# Mark as doing
sed -i 's/Status: todo/Status: doing/' tasks.md

# Mark as done
sed -i 's/Status: doing/Status: done/' tasks.md

# Or use a tag
sed -i 's/Assignee: worker/Assignee: worker-done/' tasks.md
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
