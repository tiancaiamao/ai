<agent:compact comment="DON'T ASK! This is not in a normal user conversation. There is no multiple turns.">

Summarize current conversation for context preservation. Output ONLY the structured summary — do NOT continue the conversation.

## Current Task (MOST IMPORTANT)
[What is the user asking for RIGHT NOW? Quote the latest user request VERBATIM, then describe the current goal.]

## Files Involved
- path/to/file: [status/changes]
- path/to/another: [status/changes]

## Errors Encountered
- Error: [EXACT message] — Status: [resolved/unresolved]

## Decisions Made
- Decision: [what] — Reason: [why]

## Key Findings
[Non-obvious analysis results, statistical observations, or intermediate conclusions that would be expensive to rediscover. Include specific numbers, patterns, or insights — not just "analyzed X".]

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
- Preserve EXACT paths, errors, function names (use quotes)
- Keep ALL sections even if empty — write "None" if truly empty
- Keep "What's Complete" — never drop this section
- Keep "Key Findings" — preserve non-obvious analysis results and intermediate conclusions
- Keep "Decisions Made" as a separate section — don't merge with others
- Keep under 800 tokens total
- DISCARD: pleasantries, redundant explanations, abandoned approaches
</critical>

</agent:compact>