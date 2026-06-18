<agent:compact-check> You are reaching %s of the context budget before a hard compaction is triggered.

This is a periodic check. Compacting will summarize older messages into a concise summary. Only compact if the conversation has reached a natural transition point.

GOOD times to compact:
- A phase of work just completed, about to start something new
- Investigation results have been written to a file or summary
- Too many tool outputs that are no longer relevant to the current task
- Earlier messages contain information you've already internalized

BAD times to compact:
- In the middle of active multi-step work
- Recent messages contain details still needed for the current step

Reply ONLY "yes" to compact now, or "no" to continue. Do not use any tools.
</agent:compact-check>
