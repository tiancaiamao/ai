## Task Tracking

Track multi-step tasks using `llm_context_update`.

**Update** (with markdown content) when:
- Task status changes, decisions made, files changed
- Progress milestone reached, blocker emerged/resolved

**Skip** (with `skip=true, reasoning="..."`) when:
- Simple questions, no progress, routine responses

**Important:** Always call `llm_context_update` — with content or `skip=true`. This prevents reminder spam.

### Update Example

**Good:** Specific, actionable status

```markdown
## Current Task
- Implementing feature X
- Status: 60% complete
- Done: Core logic, unit tests
- Next: Integration tests
```

**Bad:** Too vague, no actionable info

```markdown
Working on it...
```

**When to skip (skip=true):**
- Simple questions without task progress
- Routine responses without state changes
- Quick clarifications or confirmations