# ADR 004: AGENTS.md Instruction Loading and Prompt Boundary

- Status: Accepted
- Date: 2026-05-08

## Context

The system prompt should contain only platform-global, stable behavior. Project
instructions and per-project facts must be loaded through a separate channel so
policy stays in one editable place and the system prompt does not drift as
projects accumulate conventions.

Previously the prompt builder also inlined project fact files (`TOOLS.md`,
`IDENTITY.md`, ...) into the system prompt via a `%PROJECT_CONTEXT%` placeholder.
That path was removed: it created a second policy surface, increased steady-state
token usage, and offered no mechanism for the orchestrator to refresh or scope
the content between turns.

## Decision

Use a single-source instruction model with explicit boundaries:

1. `system prompt` contains only platform-global, stable behavior.
2. `AGENTS.md` is the authoritative project instruction source.
3. `AGENTS.md` is injected by the orchestrator as a **user-role message** placed
   before the user's actual input on each LLM call — matching the codex
   `contextual_user_message` pattern. It is **not** inlined into the system prompt.
4. The `%PROJECT_CONTEXT%` placeholder and its loader (`buildProjectContext`,
   `bootstrapFiles`) are removed. The prompt builder no longer reads or inlines
   `TOOLS.md`, `IDENTITY.md`, or any other workspace fact file. Per-project
   tool/identity hints, if needed, should live in `AGENTS.md`.
5. Precedence is strict and explicit: `system > AGENTS.md > user`.

## Why

- **Determinism**: first turn already has project rules.
- **Lower risk**: avoids early-turn non-compliance.
- **Lower prompt entropy**: no duplicated policy text across system prompt and
  injected instructions.
- **Better maintainability**: AGENTS policy is edited in one place; the system
  prompt no longer carries project-specific content that drifts between
  workspaces.
- **Token stability**: the system prompt is now workspace-independent, so its
  length does not grow with the number of `TOOLS.md` / `IDENTITY.md` files in a
  workspace.

## Non-Goals

- This ADR does not define the full nested-scope AGENTS resolution algorithm.
- This ADR does not change context compaction strategy.

## Implementation

1. `loadAgentInstructions()` reads `AGENTS.md` from `.ai/AGENTS.md` (preferred)
   or `AGENTS.md` (workspace root).
2. `BuildInstructionsMessage()` wraps the content in `<agent:instructions>...</agent:instructions>`
   for injection as a user-role message.
3. The orchestrator (`cmd/ai/rpc_app.go::buildAgentInstructions`) calls
   `BuildInstructionsMessage()` and prepends it to the user message on each
   LLM call, regardless of system prompt source (default / role template /
   custom / agent-config). AGENTS.md carries project facts orthogonal to
   system-prompt-defined role behavior.
4. `pkg/prompt/prompt.md` has no `%PROJECT_CONTEXT%` placeholder.
5. `pkg/prompt/builder.go` no longer contains `buildProjectContext` or
   `bootstrapFiles`; the system prompt is independent of workspace fact files.

## Consequences

Positive:

- Clear ownership: policy in `AGENTS.md`, platform rules in system prompt.
- Fewer duplicate tokens in steady-state turns.
- The system prompt is byte-identical across workspaces with the same config.

Trade-offs:

- The orchestrator's instruction-injection path is now critical. Failures to
  load `AGENTS.md` must be surfaced (currently: silent fallback to "no
  instructions", which is observably correct when no `AGENTS.md` is present).
- Workspaces that previously relied on `TOOLS.md` / `IDENTITY.md` being inlined
  into the system prompt must migrate that content into `AGENTS.md`.

## Validation

- Unit tests for prompt builder: `pkg/prompt/builder_test.go`
  - `TestBuildInstructionsMessage` covers loading, `.ai/` shadowing, and
    empty-workspace fallback.
  - No `buildProjectContext` / `bootstrapFiles` symbols remain.
- Integration: `cmd/ai/rpc_app.go::buildAgentInstructions` is the single
  call-site that turns the file into an injected message.