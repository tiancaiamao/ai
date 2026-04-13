You are a code reviewer. Review the git diff for a feature called "Earlier Conservative Compact".

This feature adds the ability for the LLM mini-compact to proactively decide when to perform full context compaction, rather than waiting for the 75% token threshold. Key changes:

1. **AgentState statistics**: Added TotalTruncations, TotalCompactions, LastCompactTurn fields
2. **CompactTool**: A new tool that LLM can call to perform full compaction with configurable strategy (conservative/balanced/aggressive)
3. **Enhanced mini-compact context**: The LLM now sees cumulative statistics to make better decisions
4. **Updated system prompt**: Added compact tool guidance and decision rules

## Your Task

Review the git diff thoroughly and produce a detailed code review report.

## Review Focus

1. **AgentState changes**:
   - Are new fields properly initialized in NewAgentState?
   - Are they cloned correctly?
   - Will they persist correctly through checkpoints?

2. **CompactTool implementation**:
   - Does it handle the three strategies correctly?
   - Are edge cases handled (nil compactor, invalid params)?
   - Does it update AgentState statistics correctly?

3. **Integration points**:
   - Are all call sites of NewLLMMiniCompactor updated correctly?
   - Does the tool get added to the mini-compact tool set?
   - Will the LLM actually be able to use this tool?

4. **System prompt**:
   - Does it give clear guidance on when to use compact vs truncate?
   - Are the decision rules sound?

5. **Safety & backward compatibility**:
   - Will existing code that doesn't pass a compactor still work?
   - Is there any risk of data loss from aggressive compaction?

6. **Code quality**:
   - Are there any obvious bugs?
   - Is error handling complete?
   - Are the trace events useful?

## Output Format

Write your review to stdout in markdown format with these sections:

# Code Review: Earlier Conservative Compact

## Executive Summary
Either "APPROVED" or "REJECTED" based on overall assessment.

## Critical Issues
If any, list them here. These are bugs or safety concerns that must be fixed.

## Important Issues
Issues that should be addressed but don't block merging.

## Nice-to-Have Improvements
Suggestions that would improve the code but aren't necessary.

## Specific File Notes
Call out specific files or sections with notable observations.

## Conclusion
One sentence summary.

Be thorough. Look for actual problems. If you find issues, be specific about where they are in the diff.