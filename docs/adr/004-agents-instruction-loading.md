# ADR 004: AGENTS.md Instruction Loading and Prompt Boundary

- Status: Proposed
- Date: 2026-05-08

## Context

The system prompt currently includes a static protocol section for AGENTS.md behavior.
At the same time, project context may also inject project-local instruction files.
This creates a risk of duplicated policy in multiple channels, increasing token usage
and conflict probability when wording drifts.

We want a deterministic and low-duplication model aligned with coding-agent behavior
commonly used by Codex-style runtimes.

## Decision

Use a single-source instruction model with explicit boundaries:

1. `system prompt` contains only platform-global, stable behavior.
2. `AGENTS.md` is the authoritative project instruction source.
3. `PROJECT_CONTEXT` is reserved for project facts (e.g. tool hints, identity hints),
   not policy that duplicates AGENTS instructions.
4. Precedence is strict and explicit: `system > AGENTS.md > user`.

## Codex-Style Behavior Reference

In Codex-style runtimes, AGENTS instructions are injected by the orchestrator at
session/runtime level so the model receives scoped project rules before execution,
instead of relying on the model to remember to read files first.

Implication:

- Do not depend on “model may read AGENTS.md later”.
- Ensure AGENTS instructions are available before first actionable response.

## Why

- Determinism: first turn already has project rules.
- Lower risk: avoids early-turn non-compliance.
- Lower prompt entropy: avoids duplicated policy text in multiple sections.
- Better maintainability: AGENTS policy is edited in one place.

## Non-Goals

- This ADR does not define the full nested-scope AGENTS resolution algorithm.
- This ADR does not change context compaction strategy.

## Implementation Plan (Minimal)

1. Remove AGENTS protocol text from static `pkg/prompt/prompt.md`.
2. Keep AGENTS content injection as a runtime/orchestrator responsibility.
3. Keep `PROJECT_CONTEXT` for fact files only (`TOOLS.md`, `IDENTITY.md`, etc.).
4. Add/adjust tests:
   - Prompt template does not include AGENTS convention prose.
   - AGENTS instructions still appear in the final effective instruction set.

## Consequences

Positive:

- Clear ownership: policy in AGENTS, platform rules in system prompt.
- Fewer duplicate tokens in steady-state turns.

Trade-offs:

- Runtime/orchestrator path must be reliable; failures to load AGENTS become
  critical and should be surfaced.

## Validation

- Unit tests for prompt builder/template behavior.
- Integration test proving AGENTS instructions are present before first tool call.
- Regression check that `PROJECT_CONTEXT` still loads fact files correctly.
