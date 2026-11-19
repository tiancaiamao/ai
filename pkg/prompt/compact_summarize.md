<agent:compact comment="DON'T ASK! This is not in a normal user conversation. There is no multiple turns.">

Summarize current conversation for context preservation. Output ONLY the structured summary — do NOT continue the conversation.

## Current Task (MOST IMPORTANT)
[What is the user asking for RIGHT NOW? Quote the latest user request VERBATIM, then describe the current goal.]

## Architecture & Plan
[HIGH-LEVEL design and plan — this section MUST survive compaction. Include:]
- Overall goal and task decomposition (phases/steps)
- Current phase and what remains
- Key architectural decisions and design constraints that span the ENTIRE task
- Path to design doc or plan file if one exists (e.g. /tmp/pge-plan.md)
[If no multi-step plan exists, write "None"]

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

## Skills Loaded
[List skill names loaded via find_skill during this conversation. After compaction, full skill content is LOST. The agent may skip a reload only if the exact rules it needs are written in this summary; otherwise reload via find_skill(name="<name>", load=true) before using the skill.]
- **skill_name** — [why it was loaded / what it's used for]

## Behavioral Constraints
[Key process rules and invariants from loaded skills that the agent MUST follow after compaction. Extract only actionable rules needed to continue the work, including exact commands, flags, tool-call syntax, and file paths whenever a rule depends on them; do not replace exact syntax with abstract advice like "use the correct flag". These rules avoid a reload only when they are precise enough to execute correctly. Examples: "Update state.md AND progress.md after every task PASS", "Eval report must exist and PASS before starting next task", "Never skip the review phase before commit". Write "None" if no skill-imposed constraints apply.]

## User Requirements
[Explicit constraints from user]

<critical>
- ALWAYS include the latest user's request VERBATIM in Current Task
- Preserve EXACT paths, errors, function names (use quotes)
- Keep ALL sections even if empty — write "None" if truly empty
- Keep "What's Complete" — never drop this section
- Keep "Key Findings" — preserve non-obvious analysis results and intermediate conclusions
- Keep "Decisions Made" as a separate section — don't merge with others
- Keep "Architecture & Plan" — preserve global design, phase progress, and multi-step plans across compaction; this is the MOST easily lost information
- Keep "Skills Loaded" — list every skill that was loaded; these are lost after compaction
- Keep "Behavioral Constraints" — extract exact, executable process rules (including syntax where relevant); only precise rules avoid a reload
- Keep under 800 tokens total
- DISCARD: pleasantries, redundant explanations, abandoned approaches
</critical>

Do not use any tools. Respond with text only, not tool calls.

</agent:compact>
