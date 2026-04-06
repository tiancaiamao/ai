# Context Management Interaction Loop Refactor

Status: Draft (2026-04)

## Goal

Improve context management stability by keeping token-based triggering, while making post-trigger decisions quality-driven.

This refactor does **not** optimize primarily for token minimization. It optimizes for better context quality: less noise, better continuity, and fewer unstable management decisions.

## Principles

1. Trigger layer remains token-driven.
2. Decision layer remains LLM-driven.
3. One context-management trigger can include multiple LLM interactions.
4. Prompt rules stay stable across interactions; only round feedback changes.
5. Compact is still available, but truncate + LLM context update remains the default low-risk path.

## Execution Model

Current:
- One trigger -> one LLM interaction -> tool execution -> exit.

New:
- One trigger -> up to N interactions (default 3) within the same context-management step.
- Each interaction receives `round_state` describing previous actions and observed effects.

### Stop Conditions

Stop the inner loop when any condition is met:

1. `no_action` is called.
2. No state change after tool execution.
3. Maximum interactions reached.

## Input Contract Additions

In context-management mode, each interaction can include:

- `<current_state>` (existing)
- `<round_state>` (new, from previous interaction)
- current LLM context
- tool outputs + stale score metadata
- recent messages

`stale` remains a weak signal, not a deletion directive.

## Prompt Contract Updates

`context_mgmt_system.md` should explicitly include:

- `compact_messages` as an available action.
- multi-interaction behavior with `round_state` feedback.
- stale score as secondary to semantic relevance.

## Validation Plan

Primary acceptance criterion:

- Symphony task replays should complete successfully with improved context-management stability.

Secondary checks:

- No regression in checkpoint behavior.
- No obvious increase in context-management dead loops.

## Validation Note

Validated on 2025-07-11: context-management interaction loop refactor remains structurally sound after latest changes; focused prompt test passes and no regressions observed in checkpoint or trigger behavior.
