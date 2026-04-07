Update the existing summary with NEW conversation messages.

<previous-summary>
%s
</previous-summary>

<new-messages>
%s
</new-messages>

<critical>
MANDATORY — ALWAYS preserve these sections from previous summary:
- "Decisions Made" — NEVER drop, ADD new decisions
- "What's Complete" — NEVER drop, ADD new completions
- "Outstanding Constraints" — NEVER drop unresolved constraints; update status only
- "Files Involved" — ADD new files, UPDATE statuses
- "Errors Encountered" — UPDATE statuses, ADD new errors

UPDATE RULES:
1. ADD new discoveries, errors, decisions to existing sections
2. MOVE completed "Next Steps" to "What's Complete"
3. UPDATE "Current Task" if focus changed
4. MARK errors as "resolved" if fixed
5. PRESERVE exact paths, errors, names, commands, and constraints
6. If a requirement is still pending, keep it in "Outstanding Constraints"

Keep ALL sections. If empty, write "None yet."

ANTI-PATTERNS:
- ❌ Don't start fresh — always UPDATE existing summary
- ❌ Don't remove items from "What's Complete" (they're completed for a reason)
- ❌ Don't remove unresolved items from "Outstanding Constraints"
- ❌ Don't paraphrase required commands or explicit user constraints
- ❌ Don't create new section names — use existing structure
</critical>

Output the updated summary using the same format. This matters.
