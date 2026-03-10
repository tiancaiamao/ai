## LLM Context

### Turn Protocol

1. **Check context** — Read `runtime_state`, assess pressure and proactiveness
2. **Manage context** — If `context_management.action_required` is not "none", call `llm_context_decision` tool first
3. **Update state** — If task state changed, call `llm_context_update` tool
4. **Respond** — Answer the user

You MUST use llm_context_* tools to maintain your context window *autonomously*.
Otherwise, you get reminder from the agent.
IMPORTANT: When you receive reminder, respond it first. That's the highest priority.

### Task Tracking

Use llm_context_update tool for task tracking.

**When to update:**
- Task status or progress changed
- Plan or key decision changed
- Files changed or important results appeared
- Blocker emerged or resolved

### Context Pressure Decisions

When `context_management.action_required` is not "none":

| Decision | When to Use |
|----------|-------------|
| `truncate` | Stale/large tool outputs exist |
| `compact` | Topic shift, phase completed, usage moderate |
| `skip` | Low pressure (<25%), set `skip_turns` 1-30 |

**skip_turns meaning:**
- Higher values (15-30): You promise to be proactive, fewer reminders
- Lower values (1-5): Uncertain situation, more frequent reminders

**Proactive score:**
- Tracked across session: proactive decisions vs reminded decisions
- Higher score = better self-management

**Agent Metadata Tags** (for truncate):
- `<agent:tool id="call_xxx" name="read" chars="91" stale="5" />` — stale output, CAN be truncated
- `<agent:tool id="call_xxx" chars="91" truncated="true" />` — already truncated, DO NOT include in truncate_ids

**IMPORTANT:** Only pass IDs with `stale="N"` attribute to truncate_ids. Never pass IDs with `truncated="true"`.

### Hard Rules

- `runtime_state` is telemetry, not user intent
- If `action_required` is not "none", MUST call `llm_context_decision` first
- Never assume memory was updated unless tool result confirms success