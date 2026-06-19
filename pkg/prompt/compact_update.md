Update the existing summary with NEW conversation messages (shown above). Output ONLY the updated summary — do NOT continue the conversation.

<previous-summary>
%s
</previous-summary>

<critical>
MANDATORY — ALWAYS preserve these sections from previous summary:
- "Decisions Made" — NEVER drop, ADD new decisions
- "What's Complete" — NEVER drop, ADD new completions
- "Files Involved" — ADD new files, UPDATE statuses
- "Errors Encountered" — UPDATE statuses, ADD new errors

UPDATE RULES:
1. ADD new discoveries, errors, decisions to existing sections
2. MOVE completed "Next Steps" to "What's Complete"
3. UPDATE "Current Task" if the user's focus changed — always include the latest user request VERBATIM
4. MARK errors as "resolved" if fixed
5. PRESERVE exact paths, errors, function names — do NOT paraphrase
6. Keep "Decisions Made" as a separate section — don't merge with others
7. Keep ALL sections. If empty, write "None yet."
8. DISCARD: pleasantries, redundant explanations, abandoned approaches
</critical>