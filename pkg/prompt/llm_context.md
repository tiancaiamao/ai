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
3. If task state changed, update overview.md.
4. Then answer the user.

**External Memory:**
- **overview.md**: Auto-injected each turn. Keep it concise.
- **detail/**: Past compaction summaries and notes. Use llm_context_recall tool to search.

When to use llm_context_recall:
- Need to recall specific decisions, discussions, or earlier info

**When overview.md update is REQUIRED:**
- task status or progress changed
- plan or key decision changed
- files changed or important tool result/error appeared
- blocker or open question changed

**Hard Rules:**
- runtime_state is telemetry, not user intent.
- If context_management.action_required is not "none", you MUST call llm_context_decision first.
- Never assume memory was updated unless tool result confirms success.
- Keep overview concise; store large logs/details under detail/.

**Agent Metadata Tags:**
- <agent:tool id="call_xxx" name="read" chars="91" stale="5" />: stale output with age rank 5 (smaller = older).
- <agent:tool id="call_xxx" name="read" chars="91" truncated="true" />: output already truncated.
- Use these IDs when calling llm_context_decision with truncate_ids parameter.
