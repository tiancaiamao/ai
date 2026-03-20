## Context Management

### Your Responsibility

**You are RESPONSIBLE for managing your context window proactively.**

The system provides reminders, but **proactive management is expected**:
- After completing a task phase → TRUNCATE stale outputs
- **When context usage > 40%** → Consider COMPACT to keep context concise
- When you notice stale tool outputs → Batch TRUNCATE 50-100 at once

### Proactive Score

You get reminder from the agent if you are not proactive.
IMPORTANT: When you receive `remind`, you MUST call `llm_context_decision` immediately

Your score is visible in `runtime_state.context_metrics.decision`:

```yaml
context_metrics:
  decision:
    proactive: N    # Times you called llm_context_decision without being reminded
    reminded: M      # Times you called llm_context_decision after being reminded
    score: excellent|good|fair|needs_improvement|no_data
```

Score calculation:
- `proactive > reminded` → score improves
- `reminded > proactive` → score degrades
- Higher proactive count = fewer reminders = better performance

### Context Pressure Decisions

| Decision | When to Use |
|----------|-------------|
| `truncate` | Stale/large tool outputs exist |
| `compact` | Context usage > 40%, topic shift, or phase completed |
| `skip` | Low pressure (<25%), set `skip_turns` 1-30 |

**skip_turns meaning:**
- Higher values (15-30): You promise to be proactive, fewer reminders
- Lower values (1-5): Uncertain situation, more frequent reminders

**Agent Metadata Tags** (for truncate):
- `<agent:tool id="call_xxx" name="read" chars="91" stale="5" />` — stale output, CAN be truncated

**IMPORTANT:** Only pass IDs with `stale="N"` attribute to truncate_ids.

### Topic Shift Detection

When you detect a topic shift (new user request, phase change, task completion), 
**proactively evaluate context management needs BEFORE the system reminds you.**

Signs of topic shift:
- User starts a new, unrelated task
- Current task phase is completed
- Context contains many outputs from previous task phases