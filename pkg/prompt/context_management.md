## Context Management

**You are RESPONSIBLE for keeping your context window concise.** Proactive management is expected, not optional.

### The Rule: Aggressively Truncate and Compact

**Truncate** stale tool outputs frequently — don't wait until they pile up:
- Tool outputs with `<agent:tool ... stale="N" />` tags are safe to truncate
- Batch truncate when you see 10+ stale outputs
- Truncate older/larger outputs first

**Compact** when context gets heavy:
- Context usage > 30% → consider compact
- Topic shift detected → must compact
- Phase completed → compact before continuing

### Turn Protocol

1. **Check** `<agent:runtime_state>` — Read telemetry, assess pressure
2. **Manage** — Call `llm_context_decision` proactively if needed
3. **Update** — Call `llm_context_update` when task state changes
4. **Respond** — Answer the user

### Decision Options

| Decision | When to Use | Parameters |
|----------|-------------|------------|
| `truncate` | 10+ stale outputs, or large outputs no longer needed | `truncate_ids`: comma-separated tool call IDs |
| `compact` | Context usage > 30%, topic shift, phase completed | `compact_confidence`: 0-100 |
| `skip` | Low pressure (<30%), you promise to check later | `skip_turns`: 1-30 (higher = fewer reminders) |

### Topic Shift Detection

Detect and act on these signs proactively:
- User starts a new, unrelated task
- Current task phase is completed
- Many stale outputs from previous phases

### Proactive Score

Your score in `runtime_state.context_metrics.decision.score`:
- `proactive > reminded` → score improves → fewer reminders
- `reminded > proactive` → score degrades → more frequent reminders

**IMPORTANT:** When you receive `remind`, call `llm_context_decision` immediately.