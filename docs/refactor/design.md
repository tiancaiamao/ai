# Refactoring Improvement Plan for AI Project

Date: 2026-05-11
Status: APPROVED (grill-me completed)
Based on: debate-rewrite-vs-refactor.md analysis + SE book principles

## Executive Summary

The debate concluded in favor of **incremental refactoring on the existing system** rather than a clean rewrite. The Opponent's strongest arguments were:

1. **3 rewrites (ai2/ai3/ai4) produced zero deployable replacements** — 3 attempts, 0 deliveries
2. **684 existing test functions** encode hard-won domain knowledge that shouldn't be discarded
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
- `cmd/ai/rpc_handlers.go` (2,406 lines): a single `runRPC` function (~2200 lines) where 35 `server.Register` closures capture massive local state

### P3: Dual Compaction Interface
- `pkg/context/compactor.go`: defines `Compactor` interface (ShouldCompact/Compact/CalculateDynamicThreshold)
- `pkg/compact/compact.go`: defines `Compactor` struct with `ShouldCompactOld`/`CompactOld`/`EstimateContextTokensOld` (3 legacy methods) alongside the new methods
- Old methods are NOT dead code — still called from 5 sites: `cmd/ai/rpc_handlers.go` (3), `pkg/agent/compaction_controller.go` (2), `pkg/session/compaction.go` (2)
- Adapter method `ToContextCompactor()` bridges the two
- Old methods account for ~314 lines; removing them leaves `compact.go` at ~750 lines

### P4: Cross-cutting Concerns
- `traceevent` is a cross-cutting concern embedded directly in `pkg/agent/loop.go`
- Metrics collection mixed into loop logic

---

## Grill-Me Decisions Log

All key decision branches resolved during design review:

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Phase 0 characterization tests scope | **(B) Through public Agent API** | Feathers: observe behavior through interface; internal tests would break when Phase 3 restructures |
| Q2 | Characterization test quantity | **Only fill coverage gaps** | 684 existing tests + 59 agent tests likely provide substantial coverage already |
| Q3 | rpc_handlers.go split strategy | **(A) rpcApp struct** | 35 closures capture shared state; cannot split files without a struct to hold state |
| Q4 | Compactor Old methods | **Migrate all callers, then delete** | Old methods are low-frequency manual slash command paths; dual interface adds maintenance burden |
| Q5 | compact.go further splitting post Phase 2 | **Decide after Phase 2** | 750 lines may be acceptable if internal structure is clear |
| Q6 | loopState struct introduction timing | **(B) After extracting independent functions first** | Avoid premature struct; extract low-coupling functions first, introduce struct when needed |
| Q7 | runInnerLoop extraction order | **A→B: runtime meta first, then checkpoint** | Start with most independent (read-only, no state mutation), accumulate gradually |
| Q8 | runInnerLoop target state | **(B) Deep split, ~150 lines, loopState** | Compaction appears twice in loop body; share `performCompaction` method |
| Q9 | streamAssistantResponse ownership | **Decide after Phase 3** | Wait to see loop's final structure before committing to struct method vs package function |
| Q10 | LLMCaller interface | **(C) Don't introduce** | Existing `httptest.NewServer` mock pattern is sufficient; no real second-adapter need |
| Q11 | Phase 6 cleanup scope | **(C) Hybrid** | Small cleanup follows each phase; final lightweight pass for docs and cross-cutting polish |
| Q12 | Execution strategy | **(A) Strict serial** | Phase 1 rpcApp references compact.Compactor; Phase 2 changes that interface. Serial avoids rebase conflicts |

---

## Phase 0: Preparation (Foundation)

**Goal**: Establish safety net and measurement baselines before any structural changes.

### 0.1 Test Gap Analysis
- Run existing test suite, record baseline coverage: `go test ./... -coverprofile=baseline.out`
- Identify which functions lack test coverage using `go tool cover -func=baseline.out`
- Focus on: `runInnerLoop`, `streamAssistantResponse`, `rpc_handlers.go` handlers
- **Deliverable**: `docs/refactor/baseline-coverage.md`

### 0.2 Fill Coverage Gaps via Public API Characterization Tests
- Test through `Agent` public API (`NewAgent*`, `Prompt`, `Events`, `Wait`, `Steer`)
- Use existing `httptest.NewServer` mock pattern (established in `loop_stream_integration_test.go`)
- Only add tests where coverage gaps exist — do NOT blanket-write new tests
- Focus on behaviors:
  - Compaction trigger behavior
  - Max turns enforcement
  - Tool call loop guard
  - Error recovery paths
  - Context cancellation
- **PR**: `test(agent): fill characterization test gaps for loop behavior` (~1 PR, size depends on gap analysis)

### 0.3 Document Package Dependency Graph
- Generate and commit `docs/refactor/dependency-graph.md` showing current package imports
- Identify circular or tangled dependencies
- **Deliverable**: documentation only

---

## Phase 1: Split `rpc_handlers.go` via `rpcApp` Struct (Low Risk, High Visibility)

**Goal**: Break the 2,406-line monolith into focused files by introducing a struct to replace closure captures.

**Rationale**: `runRPC` is a ~2200-line function where 35 `server.Register`/`server.RegisterSlash` closures capture ~15 local variables (agent, sess, sessionMgr, cfg, compactor, ctxManager, etc.). Without a struct, these closures cannot be moved to separate files.

### 1.1 Introduce `rpcApp` Struct
- Create `cmd/ai/rpc_app.go` with `type rpcApp struct`
- Move all closure-captured variables into struct fields
- Convert `runRPC` body into `rpcApp` construction + method calls
- **PR**: `refactor(cmd): introduce rpcApp struct for handler state` (1 PR)

### 1.2 Migrate RPC Command Handlers to rpcApp Methods
- Move the 4 `server.Register(rpc.Command*)` handlers to `cmd/ai/rpc_handlers.go`
- Each becomes `(app *rpcApp) handlePrompt(cmd) (any, error)` etc.
- **PR**: `refactor(cmd): migrate RPC command handlers to rpcApp methods` (1 PR)

### 1.3 Migrate Slash Command Handlers to rpcApp Methods
- Move ~30 `server.RegisterSlash`/`server.RegisterHiddenSlash` handlers to topic-specific files:
  - `cmd/ai/rpc_session_handlers.go` — `/new`, `/resume`, `/session`
  - `cmd/ai/rpc_message_handlers.go` — `/messages`, `/context`, `/export_html`
  - `cmd/ai/rpc_config_handlers.go` — `/set`, `/model`, `/thinking`, `/toggle`, `/show`
  - `cmd/ai/rpc_workflow_handlers.go` — `/rewind`, `/fork`, hidden workflow commands
  - `cmd/ai/rpc_help_handlers.go` — `/help`, `/skills`, `/quit`, `/abort`, `/follow-up`
- Each handler becomes `(app *rpcApp) handleSlash*(args) (any, error)`
- **PR**: `refactor(cmd): migrate slash command handlers to rpcApp methods` (1-2 PRs)

### 1.4 Extract Setup Helpers
- Move config loading, session initialization, tool registration, compactor setup into helper functions
- `cmd/ai/rpc_setup.go` — `newRPCApp(...)` constructor and setup helpers
- Remove pipeline placeholder (returns "not yet available")
- **PR**: `refactor(cmd): extract rpcApp setup helpers` (1 PR)

**Target**: `cmd/ai/rpc_handlers.go` under 500 lines, with handler logic distributed across 5-6 files.

---

## Phase 2: Unify Compaction Interface (Medium Risk, High Impact)

**Goal**: Eliminate the dual Compactor interface, migrate all callers to new interface, remove all `Old` methods.

### 2.1 Migrate Callers from Old to New Interface
Sites to migrate:
- `cmd/ai/rpc_handlers.go` → `rpcApp` methods (3 call sites after Phase 1)
- `pkg/agent/compaction_controller.go` (2 call sites)
- `pkg/session/compaction.go` (2 call sites)
- Each site needs to construct or pass `*agentctx.AgentContext` instead of `[]AgentMessage`
- **PR**: `refactor: migrate all callers from Compactor Old methods to new interface` (1 PR)

### 2.2 Remove Old Methods and Adapter
- Delete from `pkg/compact/compact.go`:
  - `ShouldCompactOld` (L84-103, 20 lines)
  - `CompactOld` (L190-279, 90 lines)
  - `EstimateContextTokensOld` (L424-442, 19 lines)
  - `ensureToolCallPairing` (L767-845, 79 lines) — only used by Old path
  - `ensureToolCallPairingWithGrace` (L846-951, 106 lines) — only used by Old path
  - `ToContextCompactor` (L952-961, 10 lines) — adapter no longer needed
  - `CompactIfNeeded` (L443-453, 11 lines) — wrapper around Old methods
- Remove associated test code
- **PR**: `refactor(compact): remove legacy Compactor Old methods` (1 PR)

### 2.3 Evaluate compact.go Structure
- After removal: ~750 lines remain
- Decide whether to further split helpers into `compact_helpers.go`
- **Decision point**: assess readability and decide on the spot

---

## Phase 3: Deep Split of `runInnerLoop` (High Risk, Highest Impact)

**Goal**: Break the ~390-line for-loop into clearly named, testable methods on a `loopState` struct, targeting ~150 lines.

**Strategy**: Extract independent functions first, accumulate until shared state demands a struct, then introduce `loopState`.

### 3.1 Extract Runtime Meta Injection
- Most independent: read-only, no state mutation
- Move injection logic from loop body to a helper function
- Already partially separated: `pkg/agent/runtime_meta.go` exists
- **PR**: `refactor(agent): extract runtime meta injection from loop body` (1 PR)

### 3.2 Extract Checkpoint Save/Restore
- Logic is self-contained, checkpoint manager already exists in `pkg/agent/checkpoint_manager.go`
- Extract: `func saveCheckpoint(mgr, agentCtx, messages, turnCount) error`
- Extract: `func restoreCheckpoint(mgr) ([]AgentMessage, error)`
- **PR**: `refactor(agent): extract checkpoint logic from loop into helpers` (1 PR)

### 3.3 Extract Compaction Check & Execution
- The compaction trigger logic appears **twice** in the loop (pre-LLM threshold + context limit recovery)
- Extract a shared `func performCompaction(ctx, agentCtx, config, stream, trigger) (compacted bool, err error)`
- Both call sites delegate to this shared function
- **PR**: `refactor(agent): extract shared performCompaction from loop` (1 PR)

### 3.4 Extract Tool Execution Block
- Tool call dispatch → execution → result collection → stream emission
- Extract into a helper function
- **PR**: `refactor(agent): extract tool execution from loop body` (1 PR)

### 3.5 Extract Turn Boundary Management
- Turn counting, turn event emission, turn start/end
- Extract: `func beginTurn(...)` and `func endTurn(...)`
- **PR**: `refactor(agent): extract turn boundary management from loop body` (1 PR)

### 3.6 Introduce `loopState` Struct
- After 3.1-3.5, shared state (compactionRecoveries, turnCount, loopGuard, emptyResponseRetries, checkpointMgr) is passed around as parameters
- Introduce `type loopState struct` to hold these, convert extracted functions to methods
- The resulting `runInnerLoop` should be ~100-150 lines: a clear for-loop delegating to methods

**Target state for `runInnerLoop`** (~100-150 lines):
```go
func runInnerLoop(ctx, agentCtx, messages, config, stream) {
    state := newLoopState(config, agentCtx)
    defer state.cleanup()

    for {
        if state.shouldStop(ctx) { return }

        if state.performCompaction(ctx) { continue }

        response := state.callLLM(ctx)
        if state.handleResponse(response) { continue }

        toolCalls := parseToolCalls(response)
        if len(toolCalls) == 0 { return }

        state.executeToolCalls(ctx, toolCalls, stream)
        state.advanceTurn()
    }
}
```

---

## Phase 4: Extract `streamAssistantResponse` (High Risk)

**Goal**: Break the ~400-line streaming function into testable pieces.

**Decision**: Whether `streamAssistantResponse` becomes a `loopState` method or stays as a package-level function will be determined after Phase 3 is complete.

### 4.1 Extract Stream Event Parsing
- SSE/JSON parsing of streaming chunks (~100 lines)
- Extract into pure function: `func parseStreamChunk(data []byte) (StreamEvent, error)`
- **PR**: `refactor(agent): extract stream chunk parsing` (1 PR)

### 4.2 Extract Thinking Block Handling
- Thinking block detection and extraction (~60 lines)
- Extract into pure function
- **PR**: `refactor(agent): extract thinking block processing` (1 PR)

### 4.3 Consolidate Stream Error/Retry Logic
- Already partially separated in `pkg/agent/llm_retry.go`
- Consolidate remaining retry logic
- **PR**: `refactor(agent): consolidate stream error handling` (1 PR)

---

## Phase 5: Dependency Evaluation (Minimal Scope)

**Goal**: Evaluate remaining coupling opportunities.

With the decision to NOT introduce `LLMCaller` interface, Phase 5 is minimal:

### 5.1 Evaluate Session Interface
- Only if there's a concrete testing benefit (e.g., integration tests without filesystem)
- Skip if not justified by actual two-adapter need
- **Decision point**: likely skip entirely

---

## Phase 6: Clean Up and Polish (Low Risk, Ongoing)

**Goal**: Address remaining code quality issues.

**Strategy**: Hybrid — small cleanup follows each phase (delete dead code exposed by that phase, fix naming introduced by that phase). Final lightweight pass for:

### 6.1 Cross-Phase Dead Code Removal
- Remove any functions left unused after Phases 1-5
- Remove `CompactIfNeeded` style legacy orchestration methods

### 6.2 Standardize Error Handling
- Audit `error_stack.go` usage for consistency
- Ensure all error paths preserve context

### 6.3 Update Documentation
- Update AGENTS.md with new file organization
- Document the loop architecture after extraction
- Remove obsolete references to Old methods

---

## Phase Summary and Ordering

| Phase | Risk | Impact | Estimated PRs | Dependency |
|-------|------|--------|---------------|------------|
| 0: Preparation | Low | Critical (safety net) | 1-2 | None |
| 1: Split rpc_handlers | Low | Medium (readability) | 3-4 | None |
| 2: Unify Compaction | Medium | High (interface clarity) | 1-2 | None |
| 3: Extract runInnerLoop | High | Highest (core complexity) | 5-6 | Phase 0 |
| 4: Extract streamAssistant | High | High (streaming complexity) | 3 | Phase 3 |
| 5: Dependency Eval | Low | Low (likely skip) | 0-1 | Phase 3 |
| 6: Clean Up | Low | Low (polish) | 1-2 | Phases 1-5 |

**Execution order**: Phase 0 → Phase 1 → Phase 2 → Phase 3 → Phase 4 → Phase 5 → Phase 6

Strict serial. No parallel phases — Phase 1's `rpcApp` references `compact.Compactor`, and Phase 2 changes that interface. Serial avoids rebase conflicts.

## What We're NOT Doing

Per the debate conclusions, SE book principles, and grill-me decisions:

1. **No new `core` package consolidation** — the multi-package structure has clear responsibilities
2. **No architectural rewrite** — extracting within the existing design, not redesigning
3. **No test framework changes** — add tests in existing patterns
4. **No package reorganization** — clean up within packages
5. **No LLMCaller interface** — `httptest.NewServer` mock pattern is sufficient
6. **No premature struct** — `loopState` introduced only after independent extractions accumulate

## Success Metrics

After all phases:
- [ ] `runInnerLoop` is under 150 lines
- [ ] `streamAssistantResponse` is under 200 lines
- [ ] `cmd/ai/rpc_handlers.go` is under 500 lines (split across 5-6 files)
- [ ] Zero `Old`/`Legacy`/`Deprecated` methods in `pkg/compact/`
- [ ] All 684+ existing tests still pass
- [ ] Coverage gaps filled with public-API characterization tests
- [ ] `rpcApp` struct replaces all closure captures in `cmd/ai/`

## References

- Debate file: `/tmp/debate-rewrite-vs-refactor.md`
- Refactoring principles: Fowler, "Refactoring" — see `refactoring.mini.md`
- Legacy code approach: Feathers, "Working Effectively with Legacy Code" — see `legacy-code.mini.md`
- Deep modules: Ousterhout, "A Philosophy of Software Design" — see `philosophy-of-sd.mini.md`
- Dependency inversion: Martin, "Clean Architecture" — see `clean-architecture.mini.md`
- Architecture improvement skill: `improve-codebase-architecture` skill