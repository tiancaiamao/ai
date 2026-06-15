<agent:context_compaction_decision>
Your context has grown significantly. Please determine whether context compaction is needed.

If yes, summarize the preceding task messages (excluding recent messages which are auto-retained) into a concise task state summary (1K-3K tokens).
Preserve: user intent, files being edited, unresolved errors, key decisions, current plan.
Recent messages are automatically retained and do not need to be summarized.

Output format:
<decision>yes or no</decision>
<summary>summary content (if decision=yes)</summary>
</agent:context_compaction_decision>