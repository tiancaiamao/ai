# Refactoring Improvement Plan for AI Project

Date: 2026-05-11
Status: DRAFT
Based on: debate-rewrite-vs-refactor.md analysis + SE book principles

## Executive Summary

The debate concluded in favor of **incremental refactoring on the existing system** rather than a clean rewrite. The Opponent's strongest arguments were:

1. **3 rewrites (ai2/ai3/ai4) produced zero deployable replacements** — 3 attempts, 0 deliveries
2. **8,589 existing test functions** encode hard-won domain knowledge that shouldn't be discarded
3. **The existing system already has interfaces defined** — `pkg/context/compactor.go` defines `Compactor`, and `compact.Compactor` implements it via `ToContextCompactor()`; the remaining gap is manageable
4. **Refactoring targets are standard, well-understood tasks** — extract method, split file, unify interfaces

This plan takes those conclusions and organizes them into a phased refactoring roadmap, guided by principles from:
- **Refactoring** (Fowler): small behavior-preserving steps, safety net first
- **Working Effectively with Legacy Code** (Feathers): characterize → create seams → break dependencies → change
- **A Philosophy of Software Design** (Ousterhout): deep modules, information hiding, reduce cognitive load
- **Clean Architecture** (Martin): dependency inversion, interfaces owned by inner layers

## Guiding Principles

1. **Every step preserves observable behavior** — tests must pass before and after each PR
2. **Small, reviewable increments** — one concern per PR, never mix structural + behavioral changes
3. **Safety net first** — before touching any function, ensure existing tests cover it; add characterization tests where gaps exist
4. **Stop when clear enough** — don't refactor past the point of diminishing returns (Fowler's "stop when the smell is gone")
5. **Deep modules** — prefer small interfaces hiding meaningful complexity (Ousterhout)
6. **Break dependencies at seams** — introduce interfaces where concrete implementations are coupled (Feathers)

## Current State Baseline

| Metric | Value |
|--------|-------|
| Total Go LOC (non-test) | ~69,481 |
| Total test LOC | ~22,629 |
| Test functions | ~684 |
| Test files | 86 |
| Largest single file | `cmd/ai/rpc_handlers.go` (2,406 lines) |
| Core loop function | `runInnerLoop` (~390 lines) |
| Compaction file | `pkg/compact/compact.go` (1,066 lines) |
| Agent package files | 38 files |
| Internal packages | 16 (`agent`, `command`, `compact`, `config`, `context`, `llm`, `logger`, `modelselect`, `prompt`, `rpc`, `run`, `session`, `skill`, `tools`, `traceevent`, `truncate`) |

## Key Pain Points Identified

### P1: Giant Functions
- `runInnerLoop` (loop.go:144-530, ~390 lines): 6+ responsibilities in one for-loop
- `streamAssistantResponse` (llm_stream.go:19, ~400 lines): streaming + parsing + error handling

### P2: Giant File
- `cmd/ai/rpc_handlers.go` (2,406 lines): HTTP handlers, session management, workflow, message formatting — all in one file

### P3: Dual Compaction Interface
- `pkg/context/compactor.go`: defines `Compactor` interface (ShouldCompact/Compact/CalculateDynamicThreshold)
- `pkg/compact/compact.go`: defines `Compactor` struct with `ShouldCompactOld`/`CompactOld`/`EstimateContextTokensOld` (3 legacy methods) alongside the new methods
- Adapter method `ToContextCompactor()` bridges the two, but the Old methods remain as dead code

### P4: Tight Coupling in Agent Package
- `pkg/agent/` imports 7 concrete implementation packages: `compact`, `context`, `llm`, `prompt`, `session`, `traceevent`, `truncate`
- Agent directly depends on concrete types, not interfaces — makes testing and substitution harder

### P5: Cross-cutting Concerns
- `traceevent` is a cross-cutting concern embedded directly in `pkg/agent/loop.go` rather than being injected
- Metrics collection mixed into loop logic

---

## Phase 0: Preparation (Foundation)

**Goal**: Establish safety net and measurement baselines before any structural changes.

### 0.1 Test Gap Analysis
- Run existing test suite, record baseline coverage: `go test ./... -coverprofile=baseline.out`
- Identify which functions lack test coverage using `go tool cover -func=baseline.out`
- Focus on: `runInnerLoop`, `streamAssistantResponse`, `rpc_handlers.go` handlers
- **Deliverable**: `docs/refactor/baseline-coverage.md`

### 0.2 Add Characterization Tests for `runInnerLoop`
- The loop is the riskiest area to refactor. Add integration-level characterization tests that capture current observable behavior:
  - Compaction trigger behavior
  - Max turns enforcement
  - Tool call loop guard
  - Error recovery paths
  - Context cancellation
- Use table-driven tests for different scenarios
- **PR**: `test(agent): add characterization tests for runInnerLoop` (~1 PR)

### 0.3 Add Characterization Tests for `streamAssistantResponse`
- Test streaming event emission order
- Test error handling during stream (network failure, malformed response)
- Test thinking block extraction
- **PR**: `test(agent): add characterization tests for streamAssistantResponse` (~1 PR)

### 0.4 Document Package Dependency Graph
- Generate and commit `docs/refactor/dependency-graph.md` showing current package imports
- Identify circular or tangled dependencies
- **Deliverable**: documentation only

---

## Phase 1: Split Giant File — `rpc_handlers.go` (Low Risk, High Visibility)

**Goal**: Break the 2,406-line monolith into focused files without changing any behavior.

**Rationale**: `rpc_handlers.go` is pure structure — HTTP handlers mapping requests to agent calls. No complex algorithms. Lowest risk refactoring target.

### 1.1 Split by Handler Group
Split into files organized by functional domain:
- `cmd/ai/rpc_server.go` — server setup, middleware, routing (~200 lines, extracted from `runRPC`)
- `cmd/ai/rpc_handlers.go` — core agent interaction handlers (send, abort, events, etc.)
- `cmd/ai/rpc_session_handlers.go` — session management handlers (list, resume, delete, etc.)
- `cmd/ai/rpc_message_handlers.go` — message display handlers (messages, context stats)
- `cmd/ai/rpc_workflow_handlers.go` — workflow status and management
- `cmd/ai/rpc_helpers.go` — shared utilities, formatting functions

**Approach**: Pure file split. Same package, same functions, just organized into logical files. Zero behavior change.

**Validation**: All existing tests in `cmd/ai/` must pass unchanged.

### 1.2 Remove Dead Code in `rpc_handlers.go`
- Remove pipeline placeholder (returns "not yet available" at lines 1937-1940)
- Remove any other unreachable handlers
- **PR**: `refactor(cmd): split rpc_handlers.go into focused files` (1 PR)

---

## Phase 2: Unify Compaction Interface (Medium Risk, High Impact)

**Goal**: Eliminate the dual Compactor interface, remove all `Old` methods.

**Rationale**: The debate identified this as a clear case of interface drift. `pkg/context/compactor.go` defines the canonical interface; `pkg/compact/compact.go` has 3 legacy methods (`ShouldCompactOld`, `CompactOld`, `EstimateContextTokensOld`) that should be removed.

### 2.1 Verify No Callers of Old Methods
- `grep -rn 'ShouldCompactOld\|CompactOld\|EstimateContextTokensOld' --include='*.go'`
- If any callers exist, migrate them to use the new interface
- **PR**: `refactor(compact): migrate remaining callers to new Compactor interface` (1 PR if needed)

### 2.2 Remove Old Methods
- Delete `ShouldCompactOld`, `CompactOld`, `EstimateContextTokensOld` from `pkg/compact/compact.go`
- Remove `ToContextCompactor()` adapter if the struct already implements the interface directly
- Clean up any test code testing the Old methods
- **PR**: `refactor(compact): remove legacy Compactor methods` (1 PR)

### 2.3 Flatten Compaction Packages
- Evaluate whether `pkg/compact/` and `pkg/context/` can be merged or reorganized:
  - `pkg/context/compactor.go` (35 lines) — interface definition
  - `pkg/context/context.go` (278 lines) — AgentContext management
  - `pkg/compact/compact.go` (1,066 lines) — compaction implementation
  - `pkg/compact/context_management.go` (761 lines) — mini/major compaction logic
- Current assessment: keep them separate but ensure the interface lives in `pkg/context/` (which it already does). The implementation stays in `pkg/compact/`.
- **Decision point**: Do we merge `context_management.go` logic into `compact.go` or keep it split? (Recommendation: keep split — they have different responsibilities: algorithm vs orchestration)

---

## Phase 3: Extract Responsibilities from `runInnerLoop` (High Risk, Highest Impact)

**Goal**: Break the ~390-line for-loop into clearly named, testable functions.

**Rationale**: This is the heart of the system and the most dangerous refactoring. We follow Feathers' legacy code approach: characterize first (Phase 0), then extract one responsibility at a time.

**Strategy**: Extract Method, one concern per step. Each extraction is a separate PR with tests verifying behavior preservation.

### 3.1 Extract Compaction Check & Execution
- The compaction trigger logic (pre-llm threshold + context limit recovery) is ~70 lines of conditional logic
- Extract into: `func (l *loopState) checkAndCompact(ctx context.Context) (compacted bool, err error)`
- This function encapsulates: threshold check → compaction execution → context update → error handling
- **PR**: `refactor(agent): extract compaction check from runInnerLoop` (1 PR)

### 3.2 Extract Tool Execution Block
- The tool execution block (receive tool calls → execute → push results) is ~80 lines
- Extract into: `func (l *loopState) executeToolCalls(ctx context.Context, toolCalls []ToolCall) ([]AgentMessage, error)`
- **PR**: `refactor(agent): extract tool execution from runInnerLoop` (1 PR)

### 3.3 Extract Checkpoint Management
- Checkpoint save/restore logic is ~40 lines scattered through the loop
- Extract into: `func (l *loopState) saveCheckpoint(messages []AgentMessage) error` and `func (l *loopState) restoreCheckpoint() ([]AgentMessage, error)`
- Already partially extracted: `pkg/agent/checkpoint_manager.go` exists
- **PR**: `refactor(agent): consolidate checkpoint logic from loop into checkpoint_manager` (1 PR)

### 3.4 Extract Turn Start/End Handling
- Turn counting, turn event emission, and turn boundary logic is ~30 lines
- Extract into: `func (l *loopState) beginTurn()` and `func (l *loopState) endTurn()`
- **PR**: `refactor(agent): extract turn boundary management from runInnerLoop` (1 PR)

### 3.5 Extract Runtime Meta Injection
- The runtime state telemetry injection (runtime_meta) is ~40 lines
- Already partially separated: `pkg/agent/runtime_meta.go` exists
- Move the injection call out of the loop body into a helper
- **PR**: `refactor(agent): extract runtime meta injection from loop body` (1 PR)

### 3.6 Introduce `loopState` Struct
- After extractions 3.1-3.5, introduce a `loopState` struct to hold the shared loop variables (compactionRecoveries, turnCount, loopGuard, etc.)
- This replaces the scattered local variables with a cohesive state object
- The resulting `runInnerLoop` should be ~100-150 lines: a clear for-loop that delegates to well-named helpers
- **PR**: `refactor(agent): introduce loopState struct for runInnerLoop` (1 PR)

**Target State for `runInnerLoop`** (~100-150 lines):
```go
func runInnerLoop(ctx, agentCtx, messages, config, stream) {
    state := newLoopState(config, agentCtx)
    defer state.cleanup()

    for {
        if state.shouldStop(ctx) { return }

        if state.checkAndCompact(ctx) { continue }

        response := callLLM(ctx, state)
        if state.handleResponse(response) { continue }

        toolCalls := parseToolCalls(response)
        if len(toolCalls) == 0 { return }

        results := state.executeToolCalls(ctx, toolCalls)
        state.emitToolResults(results, stream)
        state.advanceTurn()
    }
}
```

---

## Phase 4: Extract Responsibilities from `streamAssistantResponse` (High Risk)

**Goal**: Break the ~400-line streaming function into testable pieces.

### 4.1 Extract Stream Event Parsing
- The SSE/JSON parsing of streaming chunks is ~100 lines
- Extract into: `func parseStreamChunk(data []byte) (StreamEvent, error)`
- Pure function — easy to test in isolation
- **PR**: `refactor(agent): extract stream chunk parsing from streamAssistantResponse` (1 PR)

### 4.2 Extract Thinking Block Handling
- Thinking block detection and extraction is ~60 lines
- Extract into: `func processThinkingBlock(event StreamEvent) (thinking string, content string)`
- **PR**: `refactor(agent): extract thinking block processing from streamAssistantResponse` (1 PR)

### 4.3 Extract Error/Retry Logic
- Stream error handling, retry decision, and backoff logic is ~50 lines
- Already partially separated: `pkg/agent/llm_retry.go` exists
- Consolidate remaining retry logic
- **PR**: `refactor(agent): consolidate stream error handling` (1 PR)

---

## Phase 5: Dependency Inversion — Introduce Interfaces for Agent Package (Medium Risk)

**Goal**: Reduce `pkg/agent/`'s dependency on concrete packages by introducing local interfaces.

**Rationale**: The debate correctly identified that agent imports 7 concrete packages. Following the Dependency Inversion Principle (Clean Architecture), agent should depend on interfaces it owns, with concrete implementations injected.

**Approach**: Follow Ousterhout's "deep module" principle — don't create shallow interfaces just for testability. Only introduce interfaces where there's a real benefit (two adapters: production + test, or clear substitution value).

### 5.1 Audit Which Dependencies Benefit from Interfaces
- `llm.Client` — YES. Already has interface-like behavior. Worth formalizing.
- `session.Session` — MAYBE. File-backed session is the only implementation. Only worth it if we want in-memory sessions for testing.
- `prompt.Builder` — NO. Pure function, no state, no substitution benefit.
- `traceevent` — NO. Cross-cutting concern; wrapping in interface adds complexity without depth.
- `truncate` — NO. Pure utility functions.
- `compact.Compactor` — ALREADY DONE. Interface exists in `pkg/context/compactor.go`.
- `context.AgentContext` — MAYBE. It's a data structure, not a service. Low priority.

### 5.2 Introduce LLM Client Interface in Agent Package
- Define `type LLMCaller interface { StreamChat(...) (*EventStream, error) }` in `pkg/agent/`
- Update `runInnerLoop` to accept `LLMCaller` instead of constructing `llm.Client` directly
- Production adapter wraps `llm.Client`; test adapter provides mock streaming
- **PR**: `refactor(agent): introduce LLMCaller interface for dependency inversion` (1 PR)

### 5.3 Evaluate Session Interface
- Only if there's a concrete testing benefit (e.g., integration tests without filesystem)
- Skip if not justified by two-adapter test
- **Decision point**: defer to implementation phase

---

## Phase 6: Clean Up and Polish (Low Risk, Ongoing)

**Goal**: Address remaining code quality issues discovered during refactoring.

### 6.1 Remove Dead Code
- Remove unused functions identified during Phases 1-5
- Remove any `CompactIfNeeded` style legacy orchestration methods if superseded

### 6.2 Standardize Error Handling
- The debate noted `error_stack.go` exists — audit whether error wrapping is consistent
- Ensure all error paths preserve context

### 6.3 Improve Naming Where Friction Was Found
- During extraction, rename any unclear variables/functions
- Follow "names are design" principle

### 6.4 Update Documentation
- Update AGENTS.md with new file organization
- Document the loop architecture after extraction

---

## Phase Summary and Ordering

| Phase | Risk | Impact | Estimated PRs | Dependency |
|-------|------|--------|---------------|------------|
| 0: Preparation | Low | Critical (safety net) | 3-4 | None |
| 1: Split rpc_handlers | Low | Medium (readability) | 1-2 | None |
| 2: Unify Compaction | Medium | High (interface clarity) | 2-3 | None |
| 3: Extract runInnerLoop | High | Highest (core complexity) | 5-6 | Phase 0 |
| 4: Extract streamAssistant | High | High (streaming complexity) | 3 | Phase 0 |
| 5: Dependency Inversion | Medium | High (testability/coupling) | 1-3 | Phase 3 |
| 6: Clean Up | Low | Low (polish) | Ongoing | Phases 1-5 |

**Recommended execution order**: Phase 0 → Phase 1 (parallel with Phase 2) → Phase 3 → Phase 4 → Phase 5 → Phase 6

Phases 1 and 2 are independent and can be done in parallel. Phase 3 depends on Phase 0's characterization tests. Phase 5 depends on Phase 3 because the interfaces should be defined against the extracted loop structure.

## What We're NOT Doing

Per the debate conclusions and SE book principles, we explicitly exclude:

1. **No new `core` package consolidation** — the Opponent showed the existing multi-package structure has clear responsibilities. A God Package would be worse.
2. **No architectural rewrite** — we're not changing the fundamental agent loop design, only extracting responsibilities within it.
3. **No test framework changes** — we add tests to existing patterns, not introduce new test infrastructure.
4. **No package reorganization** — the 16-package structure is reasonable. We clean up within packages, not reorganize across them.

## Success Metrics

After all phases:
- [ ] `runInnerLoop` is under 150 lines
- [ ] `streamAssistantResponse` is under 200 lines
- [ ] `cmd/ai/rpc_handlers.go` is under 500 lines (split across files)
- [ ] Zero `Old`/`Legacy`/`Deprecated` methods in `pkg/compact/`
- [ ] All 684+ existing tests still pass
- [ ] New characterization tests cover the extracted functions
- [ ] Package dependency graph shows reduced coupling in `pkg/agent/`

## References

- Debate file: `/tmp/debate-rewrite-vs-refactor.md`
- Refactoring principles: Fowler, "Refactoring" — see `refactoring.mini.md`
- Legacy code approach: Feathers, "Working Effectively with Legacy Code" — see `legacy-code.mini.md`
- Deep modules: Ousterhout, "A Philosophy of Software Design" — see `philosophy-of-sd.mini.md`
- Dependency inversion: Martin, "Clean Architecture" — see `clean-architecture.mini.md`
- Architecture improvement skill: `improve-codebase-architecture` skill