## Task Tracking

Track multi-step tasks using `llm_context_update` to maintain awareness of progress and direction.

### When to Update

Call `llm_context_update` with markdown content when meaningful progress or changes occur:
- Task status or milestone reached
- Plan or key decision made
- Significant files changed or results produced
- Blocker emerged or resolved

### When to Skip

Call `llm_context_update` with `skip=true` when:
- Answering simple questions
- No task progress to report
- Routine responses without state changes

**Important:** Always call `llm_context_update` — either with content or with `skip=true`. This signals you're active and prevents reminder spam.

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

When no update needed but you're actively working:

```
skip: true
reasoning: "Answering a question, no task progress"
```

### Why This Matters

- **With skip:** Resets reminder counter → fewer interruptions
- **Without calling llm_context_update:** System thinks you're idle → frequent reminders (penalty)