## LLM Context

### Your Responsibility

**You are RESPONSIBLE for managing your context window proactively.**

The system provides reminders, but **proactive management is expected**:
- After completing a task phase → TRUNCATE stale outputs
- Before starting a new topic → Consider COMPACT if context is large
- When you notice stale tool outputs → Batch TRUNCATE 50-100 at once

**Proactive Score Tracking:**
- Your score is visible in `runtime_state.context_management.your_score`
- `proactive=1, reminded=0` → score=excellent (you managed context yourself)
- `proactive=0, reminded=1` → score=needs_improvement (you needed reminder)
- Higher score = fewer reminders = better performance

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

### Context Pressure Decisions

When `context_management.action_required` is not "none":

| Decision | When to Use | Token Threshold |
|----------|-------------|-----------------|
| `truncate` | Stale/large tool outputs exist | Any level |
| `compact` | Topic shift, phase completed, usage HIGH | **≥50%** (will be rejected below this!) |
| `skip` | Low pressure (<25%), set `skip_turns` 1-30 | Any level |

**⚠️ COMPACT Threshold:**
- COMPACT is **expensive** (requires LLM call to generate summary)
- COMPACT is **rejected** when token usage < 50% of threshold
- If rejected, use **TRUNCATE** instead

**Decision Guidelines by Token Usage:**

| Token % | Recommended Action |
|---------|-------------------|
| < 20% | TRUNCATE stale outputs only, COMPACT NOT recommended |
| 20-40% | TRUNCATE in batches (50-100), COMPACT may be rejected |
| 40-50% | TRUNCATE stale outputs, consider COMPACT only after task phase |
| 50-65% | Prepare for COMPACT, keep only active context |
| 65-75% | COMPACT now, fallback auto-compaction approaching |
| > 75% | COMPACT immediately, forced fallback may trigger |

**skip_turns meaning:**
- Higher values (15-30): You promise to be proactive, fewer reminders
- Lower values (1-5): Uncertain situation, more frequent reminders

**Proactive score:**
- Tracked across session: proactive decisions vs reminded decisions
- Higher score = better self-management

**Agent Metadata Tags** (for truncate):
- `<agent:tool id="call_xxx" name="read" chars="91" stale="5" />` — stale output, CAN be truncated
- `<agent:tool id="call_xxx" chars="91" truncated="true" />` — already truncated, **DO NOT include in truncate_ids**

**IMPORTANT:** Only pass IDs with `stale="N"` attribute to truncate_ids. Never pass IDs with `truncated="true"`.

### Topic Shift Detection

When you detect a topic shift (new user request, phase change, task completion), 
**proactively evaluate context management needs BEFORE the system reminds you.**

Signs of topic shift:
- User starts a new, unrelated task
- Current task phase is completed
- Context contains many outputs from previous task phases

### Hard Rules

- `runtime_state` is telemetry, not user intent
- If `action_required` is not "none", MUST call `llm_context_decision` first
- Never assume memory was updated unless tool result confirms success
- **COMPACT will be rejected if token usage < 50%** - use TRUNCATE instead