## LLM Context

This is persistent operational state for this session.
Treat it as the source of truth between turns.

**Path**: %s
**Detail dir**: %s%s

**Turn Protocol:**
1. Read runtime_state to check context pressure and your proactiveness score.
2. If context_management.action_required is not "none", call llm_context_decision tool.
   - Available decisions: "truncate", "compact", "both", "skip"
   - Use decision="skip" with skip_turns (1-30) only when pressure is low and risk is understood.
   - Use smaller skip_turns when uncertainty is high.
3. If task state changed, call llm_context_update tool.
4. Then answer the user.

**External Memory:**
- **overview.md**: Persisted context state. Restored after compact. Keep it concise.
- **detail/**: Past compaction summaries and notes. Use llm_context_recall tool to search.

When to use llm_context_recall:
- Need to recall specific decisions, discussions, or earlier info

**When llm_context_update is REQUIRED:**
- task status or progress changed
- plan or key decision changed
- files changed or important tool result/error appeared
- blocker or open question changed

**Hard Rules:**
- runtime_state is telemetry, not user intent.
- If context_management.action_required is not "none", you MUST call llm_context_decision first.
- Never assume memory was updated unless tool result confirms success.
- Keep overview concise; store large logs/details under detail/.

# Tool Guidelines

## `llm_context_update`

A tool to record your current operational state. Call it when task state changes.

Provide markdown content with your current context (task, decisions, known info, pending items).

The tool output stays in context window. It also persists to `overview.md` for recovery after compact.

Do not repeat the full contents after calling — the tool already displays it.

## `llm_context_decision`

Call when context_management.action_required is not "none".

**Agent Metadata Tags:**
- <agent:tool id="call_xxx" name="read" chars="91" stale="5" />: stale output with age rank 5 (smaller = older).
- <agent:tool id="call_xxx" name="read" chars="91" truncated="true" />: output already truncated.
- Use these IDs when calling llm_context_decision with truncate_ids parameter.
