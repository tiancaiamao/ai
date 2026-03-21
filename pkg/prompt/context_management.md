## Context Management

**You are RESPONSIBLE for keeping your context window concise.** Proactive management is expected, not optional.

### Turn Protocol

1. **Check** `<agent:runtime_state>` — Read telemetry YAML, assess pressure
2. **Manage** — Call `llm_context_decision` proactively if needed
3. **Update** — Call `llm_context_update` when task state changes
4. **Respond** — Answer the user

### Decision Options

| Decision | When | Parameters |
|----------|------|------------|
| `truncate` | 5+ stale outputs, batch for efficiency | `truncate_ids`: IDs from `<agent:tool id="...">` tags in context |
| `compact` | Context ≥30%, topic shift, phase completed | `compact_confidence`: 0-100 |
| `skip` | Context <20%, promise to check later | `skip_turns`: 1-30 |

### Truncate Rules

- Tool outputs with `stale="N"` show staleness (lower N = older, more obsolete)
- Get IDs from: (1) `<agent:tool id="..." stale="N" />` tags in context, or (2) reminder message examples
- **Batch truncate** 5+ outputs at once (operation itself adds context)
- **List each ID only ONCE** — no duplicates
- **IDs expire quickly** — never use IDs from old messages, always check current context

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

**When you receive `remind`, call `llm_context_decision` immediately.**