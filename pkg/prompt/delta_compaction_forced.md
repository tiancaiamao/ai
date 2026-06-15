<agent:context_compaction>
Context compaction is required. Summarize the preceding task messages (excluding recent messages which are auto-retained) into a concise task state summary (1K-3K tokens).
Preserve: user intent, files being edited, unresolved errors, key decisions, current plan.
Recent messages are automatically retained and do not need to be summarized.

Output format:
<summary>summary content</summary>
</agent:context_compaction>