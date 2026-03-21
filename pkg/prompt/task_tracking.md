## Task Tracking

Track multi-step tasks using `llm_context_update` to maintain awareness of progress and direction.

### When to Update

Call `llm_context_update` when meaningful progress or changes occur:
- Task status or milestone reached
- Plan or key decision made
- Significant files changed or results produced
- Blocker emerged or resolved

**Do not update** for simple questions, no-progress continuations, or routine responses.

### How to Update

Provide structured markdown with clear sections:

```markdown
## Current Task
- Implementing feature X
- Status: 60% complete
- Done: Core logic, unit tests
- Next: Integration tests
- Blockers: None
```

### Skip Pattern

When no update needed but you're actively working, call `llm_context_update` with `skip=true`:

```
skip: true
reasoning: "Answering a question, no task progress"
```

This prevents reminder spam by signaling you're alive.

### Why This Matters

- **With skip:** Resets reminder counter → fewer interruptions
- **Without skip:** System thinks you're idle → frequent reminders (penalty)