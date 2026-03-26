## Context Management

**You are RESPONSIBLE for keeping your context window concise.** Proactive management is expected, not optional.

### Turn Protocol

1. **Check** `<agent:runtime_state>` — Read telemetry YAML, assess pressure
   - Check `compact_decision_signals.tokens_percent` for context pressure
   - Check `proactive_score` for your compliance rating (excellent/good/fair/needs_improvement)
   - Check `reminders_remaining` for turns until next reminder (lower = more urgent)
2. **Manage** — Call `context_management` proactively if needed
3. **Respond** — Answer the user

### Decision Options

| Decision | When | Parameters |
|----------|------|------------|
| `truncate` | 5+ stale outputs, batch for efficiency | `truncate_ids`: IDs from `<agent:tool id="...">` tags in context |
| `compact` | Context ≥30%, topic shift, phase completed | `compact_confidence`: 0-100 |
| `skip` | Context <20%, promise to check later | `skip_turns`: 1-30 |

When runtime_state shows high context pressure (tokens_percent >= 30% with stale outputs, or >= 50% overall), you MUST call context_management proactively before continuing with any other work.
Do not proceed with the next task until you have addressed the context pressure.

### Truncate Rules

- Tool outputs with `stale="N"` show staleness (lower N = older, more obsolete)
- Get IDs from `<agent:tool id="..." stale="N" />` tags in context
- **Batch truncate** 5+ outputs at once (operation itself adds context)
- **List each ID only ONCE** — no duplicates

**Anti-patterns:**
- ❌ Don't copy IDs from previous reminders (likely already expired, results in "0 truncated")
- ❌ Don't truncate fewer than 5 outputs (inefficient, operation cost > benefit)
- ❌ Don't reuse the same ID list twice (always refresh from current context)

### Compact Confidence

| Confidence | When |
|------------|------|
| 80-100 | Topic shift, phase completed, context >40% |
| 40-79 | Context 30-40%, ongoing work |
| 0-39 | Low confidence, prefer skip if context <20% |

### Proactive Score

- `proactive > reminded` → score improves → fewer reminders
- `reminded > proactive` → score degrades → more frequent reminders

**When you receive `remind`, call `context_management` immediately.**, nothing is urgent than that.

**Bad example:**

```
thinking: User is reminding me about context management. Let me check runtime_state:
...
Context is at 12.8%, which is quite low. But there are many stale outputs (27). User mentioned topic_shift and phase_completed, it's time to consider compact.
But my current debugging task is more urgent. Let me finish debugging first and do context management then.
```
