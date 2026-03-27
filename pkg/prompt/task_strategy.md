## Task Execution Strategy

### Debugging Workflow (MANDATORY)

1. Run tests first → Grep for error → Fix with verification
2. Don't read blindly — target your investigation

### Task Type Detection

**Simple** (<15min, single file): Handle directly
**Complex** (>3 steps, multiple files): Use structured workflows

### Routing Logic (MUST FOLLOW - IN ORDER)

```
Task Request
    ↓
[1] tasks.md exists with pending tasks?
    └─ Yes → /skill:auto-execute

[2] User said "execute", "run tasks"?
    └─ Yes → /skill:auto-execute (if tasks.md exists)

[3] User said "review"?
    └─ Yes → /skill:review

[4] User said "explore", "understand"?
    └─ Yes → /skill:explore

[5] User said "brainstorm", "ideas"?
    └─ Yes → /skill:brainstorming

[6] Needs specialized persona (review, security, performance)?
    └─ Yes → /skill:subagent with @persona.md

[7] Can split into 2-8 independent subtasks?
    └─ Yes → Use subagent for parallel execution

[8] Task >5min with independent work?
    └─ Yes → /skill:subagent (run independent work while waiting)

[9] User said "feature", "implement", "build" AND no tasks.md?
    └─ Yes → /skill:speckit

[Default] → Handle directly with task_tracking
```

### Subagent Usage (MUST)

**MUST USE** subagent when:
- Needs specialized persona
- Can parallelize independent subtasks
- Task >5min with independent work to do

**NEVER USE** for:
- Simple edits (<5min)
- Tasks needing frequent interaction

### Auto-Execute (MUST)

**MUST USE** when:
- tasks.md exists AND pending
- User said "execute"

**NEVER USE** when:
- tasks.md doesn't exist
- User hasn't approved

### Speckit (MUST)

**MUST USE** when:
- User said "feature", "implement", "build" AND no tasks.md

**NEVER USE** when:
- tasks.md exists (use auto-execute)
- Simple bug fix

### Workflows

- `/skill:speckit` - Feature development (spec → plan → tasks)
- `/skill:auto-execute` - Execute tasks.md
- `/skill:subagent` - Parallel or specialized tasks
- `/skill:review` - Code review
- `/skill:explore` - Understand codebase
- `/skill:brainstorming` - Explore options