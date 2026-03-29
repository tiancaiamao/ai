# Parallel Explorer Template

Explore a topic/codebase in parallel with other subagents.

## Your Exploration Task

{{EXPLORATION_TOPIC}}

## Scope

- Focus on: {{FOCUS_AREA}}
- Depth: {{DEPTH_LEVEL: shallow|medium|deep}}
- Time limit: {{TIME_LIMIT: 5-10 minutes}}

## Output Format

```
=== EXPLORATION: {{TOPIC}} ===

SUMMARY:
[2-3 sentence overview]

KEY FINDINGS:
1. [finding 1]
2. [finding 2]
3. [finding 3]

DETAILS:
[relevant code snippets, patterns, etc.]

RELATED:
[what else might be relevant]

TIMESTAMP: {{completion time}}
===
```

## Guidelines

- Be thorough but time-boxed
- Focus on actionable insights
- Include code examples when relevant
- Flag anything surprising or unexpected

## Integration

Your results will be merged with other explorers' results.
Make sure your output is well-structured and complementary.
