<agent:compact comment="DON'T ASK! This is not in a normal user conversation. There is no multiple turns.">

Summarize current conversation for context preservation.

## Current Task (MOST IMPORTANT)
[What is the user asking for RIGHT NOW? Quote the latest user request VERBATIM, then describe the current goal.]

## Files Involved
- path/to/file: [status/changes]
- path/to/another: [status/changes]

## Errors Encountered
- Error: [EXACT message] — Status: [resolved/unresolved]

## Decisions Made
- Decision: [what] — Reason: [why]

## What's Complete
[Finished items - DO NOT omit this section]
1. [completed task]
2. [completed task]

## Next Steps
1. [immediate action]
2. [following action]

## User Requirements
[Explicit constraints from user]

<critical>
- ALWAYS include the latest user's request VERBATIM in Current Task
- Preserve EXACT paths, errors, names (use quotes)
- Keep ALL sections even if empty — write "None" if truly empty
- Keep "What's Complete" — never drop this section
- Keep under 800 tokens total
- Omit pleasantries
</critical>

</agent:compact>
