## Task Tracking

Use llm_context_update tool for task tracking.

**When to update:**
- Task status or progress changed
- Plan or key decision changed
- Files changed or important results appeared
- Blocker emerged or resolved

**When to skip update:**
- No significant state change
- Simple responses without progress
- Continuation of same task

**⚠️ IMPORTANT: Skip Behavior**

If you decide NOT to update context, you MUST still call `llm_context_update` with `skip=true`:

```
skip: true
reasoning: "No significant state change, just answering a question"
```

**Why this matters:**
- **Without skip:** Reminder mechanism thinks you're inactive → more frequent reminders (penalty)
- **With skip:** System knows you're still active → resets reminder counter (no penalty)

**The pattern:**
1. Task changed → `llm_context_update` with `content`
2. No change but active → `llm_context_update` with `skip=true` + `reasoning`
3. Neither → You get frequent reminders (bad)