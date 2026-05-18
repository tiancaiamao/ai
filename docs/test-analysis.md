# AI Project Test Analysis

> Generated from branch `test-infra`, commit `ee34382`.
> Total: **95 test files**, **802 test functions**, **4 benchmarks**, **~26,500 lines of test code**.

---

## Table of Contents

1. [Overview](#overview)
2. [Coverage Summary](#coverage-summary)
3. [Package-by-Package Analysis](#package-by-package-analysis)
4. [Test Patterns](#test-patterns)
5. [Uncovered Areas](#uncovered-areas)
6. [Recommendations](#recommendations)

---

## Overview

### Test Statistics

| Metric | Value |
|--------|-------|
| Test files | 95 |
| Test functions | 802 |
| Benchmark functions | 4 |
| Total test code (lines) | ~26,500 |
| `pkg/...` statement coverage | **62.8%** |
| Pre-existing failures | 1 (`TestFindSkill_CaseInsensitiveSearch` in `pkg/tools`) |

### Module Breakdown

| Module | Test Files | Test Functions | Test Lines |
|--------|-----------|----------------|------------|
| `pkg/agent` | 23 | 177 | 7,517 |
| `skills/ag` | 10 | 92 | 2,178 |
| `pkg/run` | 5 | 102 | 1,940 |
| `pkg/tools` | 7 | 73 | 2,276 |
| `cmd/ai` | 8 | 38 | 1,451 |
| `claw` | 3 | 23 | 1,156 |
| `benchmark` | 2 | 17 | 512 |
| `pkg/session` | 6 | 33 | 1,408 |
| `pkg/compact` | 4 | 29 | 1,409 |
| `pkg/traceevent` | 4 | 37 | 1,106 |
| `pkg/skill` | 4 | 29 | 1,100 |
| `pkg/context` | 2 | 27 | 717 |
| `pkg/testutil` | 3 | 35 | 909 |
| `pkg/config` | 4 | 21 | 866 |
| `pkg/llm` | 4 | 17 | 516 |
| `pkg/prompt` | 2 | 16 | 437 |
| `pkg/rpc` | 1 | 12 | 337 |
| `pkg/truncate` | 1 | 11 | 300 |
| `pkg/command` | 1 | 13 | 242 |
| `pkg/modelselect` | 1 | 4 | 91 |

---

## Coverage Summary

### `pkg/...` — Overall: 62.8%

| Package | Coverage | Test Files | Key Gaps |
|---------|----------|------------|----------|
| `pkg/truncate` | **95.4%** | 1 | Nearly complete |
| `pkg/command` | **96.8%** | 1 | Nearly complete |
| `pkg/modelselect` | **86.2%** | 1 | Good |
| `pkg/testutil` | **85.9%** | 3 | New infrastructure |
| `pkg/skill` | **79.0%** | 4 | Loader partially covered |
| `pkg/traceevent` | **70.7%** | 4 | Handler/sink not exercised |
| `pkg/agent` | **71.5%** | 23 | Compaction controller, event stream, setters 0% |
| `pkg/tools/context_mgmt` | **82.9%** | 1 | No-action tool helpers 0% |
| `pkg/session` | **61.3%** | 6 | Compaction logic, manager edge cases |
| `pkg/run` | **61.2%** | 5 | Socket paths partially covered |
| `pkg/prompt` | **63.5%** | 2 | Good |
| `pkg/llm` | **53.2%** | 4 | Anthropic request builder at 20.7% |
| `pkg/compact` | **47.2%** | 4 | Summary generation, token estimation 0% |
| `pkg/tools` | **43.5%** | 7 | edit, write, change_workspace, registry all 0% |
| `pkg/config` | **40.2%** | 4 | concurrency.go 0%, config loading 41% |
| `pkg/context` | **33.1%** | 2 | LLM context, token estimation, message helpers 0% |
| `pkg/rpc` | **31.7%** | 1 | RPC server handlers ~32% |
| `pkg/logger` | **0.0%** | 0 | No tests at all |
| `pkg/version` | **0.0%** | 0 | No tests (init-only) |

### Other Modules

| Module | Coverage |
|--------|----------|
| `cmd/ai` | 6.4% |
| `skills/ag/internal/conv` | 79.1% |
| `skills/ag/internal/backend` | 61.9% |
| `skills/ag/internal/channel` | 60.0% |
| `skills/ag/internal/storage` | 35.3% |
| `skills/ag/internal/task` | 27.2% |
| `skills/ag/internal/agent` | 18.4% |
| `skills/ag/cmd` | 5.4% |
| `claw/cmd/aiclaw` | 5.3% |
| `claw/pkg/adapter` | 19.7% |

---

## Package-by-Package Analysis

### `pkg/agent` — 71.5% coverage (23 files, 177 tests)

The core agent loop package. Most heavily tested in the project.

#### Test Files and What They Cover

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `agent_test.go` | 11 | Unit | Agent API: follow-up queue, abort, steer, tools, context, state, auto-retry defaults |
| `loop_characterization_test.go` | 8 | **Integration** (httptest SSE) | Full loop behaviors: max turns, tool loop guard, cancellation, compaction trigger, event sequence |
| `loop_recovery_test.go` | 20 | **Integration** (event stream mock) | Error recovery: context-length error → compaction, repeated tool call guard, malformed tool call recovery, max turns, 4xx skip |
| `llm_stream_parse_test.go` | 28 | Unit | SSE chunk parsing: text delta, thinking delta, tool call delta, done event, error event, full stream simulation |
| `loop_stream_integration_test.go` | 6 | **Integration** (httptest SSE) | Stream-level: tool call recovery from thinking, runtime state injection, LLM context after compact |
| `loop_empty_response_test.go` | 6 | Unit | Empty/malformed LLM response handling |
| `loop_tool_parallel_test.go` | 4 | Unit | Parallel tool execution |
| `conversion_visibility_test.go` | 12 | Unit | AgentMessage ↔ LLM message conversion, visibility rules |
| `tool_call_normalize_test.go` | 9 | Unit | Tool call name normalization, argument parsing |
| `tool_tag_parser_test.go` | 9 | Unit | Parsing tool tags from LLM output |
| `tool_output_test.go` | 5 | Unit | Tool output truncation, error pattern detection |
| `tool_guard_test.go` | 1 | Unit | Tool call repetition guard |
| `executor_test.go` | 6 | Unit | Tool executor: dispatch, error handling, timeout |
| `runtime_meta_test.go` | 11 | Unit | Runtime metadata collection |
| `metrics_trace_test.go` | 8 | Unit | Metrics tracing helpers |
| `result_test.go` | 1 | Unit | Agent result type |
| `checkpoint_manager_test.go` | 6 | Unit (tmpdir) | Checkpoint save/load/restore |
| `llm_context_test.go` | 4 | Unit (tmpdir) | LLM context persistence |
| `error_stack_test.go` | 2 | Unit | Error stack operations |
| `agent_integration_test.go` | 3 | **Integration** | Agent + LLM server end-to-end |
| `agent_stress_test.go` | 4 | **Integration** (concurrent) | Concurrent prompts, race conditions |
| `agent_metrics_wiring_test.go` | 1 | Unit | Metrics wiring |
| `regression_test.go` | 12 | Unit | Regression tests for specific bugs |

#### Key Test Patterns

1. **SSE Server Mock** — `loop_characterization_test.go` defines `sseToolCallsResponse()` which builds raw SSE strings. `httptest.NewServer` serves them. Agent connects to `server.URL` as its LLM endpoint. This is the primary integration pattern.

2. **Event Stream Mock** — `loop_recovery_test.go` creates `llm.EventStream` directly via `newTestAgentEventStream()`, pushes events programmatically. Tests the loop logic without any HTTP.

3. **Direct Function Testing** — `llm_stream_parse_test.go` calls `processStreamChunk()` directly with constructed `llm.LLMEvent` values. Pure unit tests.

#### 0% Coverage Files

| File | Lines | Reason |
|------|-------|--------|
| `compaction_controller.go` | 136 | Wired in `cmd/ai` main, never exercised in tests |
| `eventstream.go` | 174 | Generic `EventStream[T]` — tested indirectly through agent but never directly |
| `agent.go` setters (SetModel, etc.) | ~60 | Simple setter methods, no tests |
| `loop.go:randFloat64` | 3 | Randomized retry jitter helper |

---

### `pkg/session` — 61.3% coverage (6 files, 33 tests)

Session persistence with append-only JSONL storage.

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `session_test.go` | 7 | Unit (tmpdir) | Core CRUD: SaveMessages, LoadSession, AppendMessage, Clear, overwrite |
| `lazy_test.go` | 11 | Unit (tmpdir) | Lazy loading: empty, non-existent, partial reads, tail-only |
| `lazy_bench_test.go` | 4 | **Benchmark** | Large session loading performance |
| `manager_test.go` | 4 | Unit (tmpdir) | Session manager: create, list, delete |
| `compaction_test.go` | 3 | Unit | Compaction: cut point finding, summary insertion |
| `compact_event_test.go` | 4 | Unit (tmpdir) | Compact event recording and replay |

#### 0% Coverage Areas
- `compaction.go` at 35.6% — `findCompactionCutPoint`, `serializeMessages` partially covered
- `manager.go` at 63.0% — fork, rename, restore paths not exercised

---

### `pkg/llm` — 53.2% coverage (4 files, 17 tests)

LLM client abstraction layer.

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `client_test.go` | 5 | **Integration** (httptest) | SSE stream parsing, `[DONE]` frame handling, error responses |
| `types_test.go` | 3 | Unit | Type helpers: PartialMessage append, content block access |
| `errors_test.go` | 6 | Unit | Error classification: retryable, rate limit, context length |
| `anthropic_request_test.go` | 3 | Unit | Anthropic-specific request formatting |

#### 0% Coverage Areas
- `anthropic.go` at 20.7% — Most Anthropic-specific logic untested
- `eventstream.go` at 53.5% — EventStream generic type partially covered

---

### `pkg/compact` — 47.2% coverage (4 files, 29 tests)

Context compaction and window management.

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `compact_test.go` | 9 | Unit | ShouldCompact threshold, disabled, token counting |
| `compact_tool_pairing_test.go` | 9 | Unit | Tool pairing for compaction |
| `compact_visibility_test.go` | 3 | Unit | Visibility rules during compaction |
| `context_management_test.go` | 8 | **Integration** (httptest) | Full compaction flow with mock LLM for summaries |

#### 0% Coverage Areas
- `compact.go` — `GenerateSummary` (calls real LLM, never mocked in isolation), `KeepRecentMessages`, `EstimateMessageTokens`, `serializeConversation` all at 0%
- `compact_tool.go` — Description/Parameters methods 0% (trivial)
- `context_management.go` — `Compact()` at 0% (requires real LLM call)

---

### `pkg/tools` — 43.5% coverage (7 files, 73 tests)

Built-in tools: bash, grep, read, edit, write, etc.

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `bash_sleep_test.go` | 2 | Integration | Bash sleep command behavior |
| `bash_timeout_test.go` | 5 | Integration | Timeout handling, kill behavior |
| `grep_test.go` | 11 | Integration (tmpdir) | Grep patterns, file filtering, context lines |
| `read_test.go` | 14 | Integration (tmpdir) | File reading, offset/limit, binary detection |
| `hashline_test.go` | 7 | Unit | Hashline parsing and generation |
| `find_skill_test.go` | 22 | Integration (tmpdir) | Skill search, index loading, case-insensitive |
| `context_mgmt/tools_test.go` | 12 | Unit | Context management tool dispatch |

#### 0% Coverage Files (entire files untested)

| File | Lines | Description |
|------|-------|-------------|
| `edit.go` | 492 | File editing tool — **largest untested tool** |
| `write.go` | 88 | File write tool |
| `change_workspace.go` | 98 | Workspace switching tool |
| `registry.go` | 53 | Tool registry — constructor, Register, Get, All, ToLLMTools |
| `workspace.go` | 24% | Workspace path resolution |

---

### `pkg/context` — 33.1% coverage (2 files, 27 tests)

Agent context management (messages, checkpoints, state).

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `checkpoint_test.go` | 23 | Unit (tmpdir) | Checkpoint create/restore/serialize, index, compaction |
| `reconstruction_counters_test.go` | 4 | Unit | Counter operations |

#### 0% Coverage Files
- `context.go` — AgentContext struct constructor/helpers
- `llm_context.go` — LLM context persistence
- `token_estimation.go` — Token counting helpers
- `message.go` at 26.1% — Message construction helpers

---

### `pkg/config` — 40.2% coverage (4 files, 21 tests)

Configuration loading, auth, model specs.

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `config_test.go` | 11 | Unit (tmpdir) | Config load/save, defaults, invalid JSON |
| `auth_test.go` | 4 | Unit (tmpdir) | Auth credential handling |
| `models_test.go` | 3 | Unit (tmpdir) | Model spec loading, overrides, sorting |
| `config_example_test.go` | 3 | Unit | Config example validation |

#### 0% Coverage
- `concurrency.go` — Full file at 0% (ResolveConcurrencyConfig, GetEnvInt)

---

### `pkg/run` — 61.2% coverage (5 files, 102 tests)

Run layer: event conversion, socket transport, metadata.

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `conv_test.go` | 54 | Unit | Event parsing: text delta, tool calls, thinking, errors, edge cases |
| `meta_test.go` | 25 | Unit | Metadata operations |
| `event_broadcaster_test.go` | 13 | Unit (concurrent) | Event broadcasting to multiple subscribers |
| `socket_stream_test.go` | 6 | Unit (tmpdir) | Socket stream framing |
| `socket_test.go` | 4 | Unit (tmpdir) | Socket connection |

---

### `pkg/traceevent` — 70.7% coverage (4 files, 37 tests)

Tracing and event recording.

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `trace_test.go` | 28 | Unit (tmpdir) | Trace recording, categories, filtering |
| `slog_bridge_test.go` | 5 | Unit | Slog handler bridge |
| `config_selectors_test.go` | 3 | Unit | Config-based event selectors |
| `traceevent_on_test.go` | 1 | Unit | Enabled trace event |

#### 0% Coverage
- `handler.go` — `Handle()`, `TraceFilePath()`, `IncrementPromptCount()` all 0%
- `buffer.go` — `AddSink()`, `Flush()` 0%
- `slog_bridge.go` — `WithAttrs()`, `WithGroup()` 0%

---

### `pkg/testutil` — 85.9% coverage (3 files, 35 tests)

New shared test infrastructure (this PR).

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `testutil_test.go` | 22 | Unit | MockTool, EventCollector, SSEBuilder, LLMServer, AgentHarness unit tests |
| `harness_integration_test.go` | 13 | **Integration** | Agent loop behaviors via harness: retry, tool call, compaction, abort, session persistence |

---

### `pkg/skill` — 79.0% coverage (4 files, 29 tests)

Skill discovery, loading, formatting.

| File | Tests | Pattern | Coverage Focus |
|------|-------|---------|----------------|
| `skill_test.go` | 12 | Unit (tmpdir) | Skill loading, matching, indexing |
| `stats_test.go` | 9 | Unit (tmpdir, concurrent) | Usage stats: load, save, concurrent access |
| `formatter_prompt_test.go` | 7 | Unit | Prompt formatting for skills |
| `integration_test.go` | 1 | Unit | End-to-end skill loading |

---

### `cmd/ai` — 6.4% coverage (8 files, 38 tests)

Application layer. Low coverage expected — thin handlers wiring `pkg/*` together.

| File | Tests | Pattern |
|------|-------|---------|
| `integration_test.go` | 9 | Integration |
| `watch_test.go` | 10 | Unit |
| `messages_handler_test.go` | 10 | Unit |
| `traceevent_handler_test.go` | 1 | Unit |
| `kill_test.go` | 3 | Unit |
| `ls_test.go` | 3 | Unit |
| `session_writer_test.go` | 1 | Unit |
| `main_test.go` | 1 | Unit |

---

## Test Patterns

The project uses five primary testing approaches:

### 1. Direct Unit Testing

The most common pattern. Construct inputs, call a function, assert outputs.

```go
func TestShouldCompact_TokenThreshold(t *testing.T) {
    c := NewCompactor(WithTokenThreshold(1000))
    // ... assert c.ShouldCompact(...)
}
```

**Used in:** `llm_stream_parse_test.go`, `conv_test.go`, `tool_call_normalize_test.go`, `tool_tag_parser_test.go`, most `*_test.go` files.

### 2. HTTP Test Server (SSE Mock)

Spin up `httptest.NewServer` that serves pre-built SSE responses. Agent connects to it as its LLM backend.

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/event-stream")
    io.WriteString(w, sseResponse)
}))
defer server.Close()
agent.SetBaseURL(server.URL)
```

**Used in:** `loop_characterization_test.go`, `loop_stream_integration_test.go`, `agent_integration_test.go`, `client_test.go`, `context_management_test.go`.

### 3. Event Stream Mock

Create an `llm.EventStream` directly and push events into it programmatically. Tests the agent loop without HTTP.

```go
stream := newTestAgentEventStream()
stream.Push(AgentEvent{Type: EventTurnEnd, ...})
stream.End()
// Run the loop against this stream
```

**Used in:** `loop_recovery_test.go`, `loop_tool_parallel_test.go`, `loop_empty_response_test.go`.

### 4. Integration Test (Real Tools)

Use real tool implementations (BashTool, GrepTool, ReadTool) against temp directories.

```go
func TestGrep_BasicPattern(t *testing.T) {
    dir := t.TempDir()
    os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world"), 0644)
    tool := tools.NewGrepTool(dir)
    result, err := tool.Execute(ctx, map[string]any{"pattern": "hello"})
    // assert result
}
```

**Used in:** `grep_test.go`, `read_test.go`, `bash_timeout_test.go`, `find_skill_test.go`.

### 5. New: Harness Pattern (this PR)

Use the shared `testutil` package to build agent test scenarios declaratively.

```go
response := NewSSEBuilder().Text("hello").Finish("stop", UsageFields{Prompt: 10, Completion: 5})
harness := NewAgentHarness(t, []string{response}, WithTools(NewEchoTool()))
collector := harness.Run("test prompt")
collector.HasEvent(EventAgentEnd)
```

**Used in:** `harness_integration_test.go`.

---

## Uncovered Areas

### Critical (0% coverage, high business value)

| Area | File | Lines | Impact |
|------|------|-------|--------|
| **Edit tool** | `pkg/tools/edit.go` | 492 | Core editing capability — fuzzy match, hashline edit, diff generation |
| **Compaction controller** | `pkg/agent/compaction_controller.go` | 136 | Orchestrates context compaction in agent loop |
| **Context management Compact()** | `pkg/compact/context_management.go` | ~200 | Actual compaction execution path |
| **LLM summary generation** | `pkg/compact/compact.go` | ~100 | `GenerateSummary` — calls LLM for compaction summaries |
| **RPC handlers** | `pkg/rpc/server.go` | ~200 | Server-side RPC message handling |
| **Anthropic provider** | `pkg/llm/anthropic.go` | ~300 | Anthropic-specific request building |

### Moderate (low coverage, moderate value)

| Area | File | Coverage | Impact |
|------|------|----------|--------|
| Tool registry | `pkg/tools/registry.go` | 0% | Tool registration and lookup |
| Write tool | `pkg/tools/write.go` | 0% | File writing |
| Change workspace tool | `pkg/tools/change_workspace.go` | 0% | Directory switching |
| Config concurrency | `pkg/config/concurrency.go` | 0% | Concurrency config resolution |
| Token estimation | `pkg/context/token_estimation.go` | 0% | Token counting |
| LLM context persistence | `pkg/context/llm_context.go` | 0% | LLM context save/load |
| Session compaction | `pkg/session/compaction.go` | 35.6% | Compaction cut point finding |
| Workspace resolution | `pkg/tools/workspace.go` | 24% | Path resolution, git root detection |

### Low Priority (0% but trivial/dead code)

| Area | File | Lines | Notes |
|------|------|-------|-------|
| Logger | `pkg/logger/logger.go` | ~30 | Thin slog wrapper |
| Version | `pkg/version/version.go` | ~15 | Init-only, sets GitCommit/GitVersion |
| Agent setters | `pkg/agent/agent.go` | ~60 | Simple getter/setter methods |
| Tool Description/Parameters | Various | ~30 | Trivial interface implementations |

---

## Recommendations

### 1. Add `pkg/tools/edit_test.go` (HIGH)

`edit.go` is 492 lines with **0% coverage**. It contains the most complex tool logic: fuzzy matching (`findBestMatch`, `editDistance`), hashline parsing, diff generation. These are all pure functions that can be tested without any I/O.

```go
// Suggested test cases:
// - findBestMatch: exact match, fuzzy match, no match
// - editDistance: basic string distance calculation
// - parseHashlineEditFromMap: valid/invalid input
// - generateDiff: verify unified diff output
// - resolvePath: relative/absolute path handling
// - executeHashlineEdits: single edit, multiple edits, conflict
```

### 2. Add `pkg/agent/compaction_controller_test.go` (HIGH)

The compaction controller orchestrates context compaction but has 0% coverage. It should be tested with a mock compactor (like `testCompactor` in `testutil`).

### 3. Add `pkg/tools/registry_test.go` (MEDIUM)

Simple unit tests for Register, Get, All, ToLLMTools. Low effort, helps prevent regressions.

### 4. Add `pkg/tools/write_test.go` (MEDIUM)

File writing tool — test basic write, overwrite, create directory, permission errors.

### 5. Add summary generation tests for `pkg/compact` (MEDIUM)

`GenerateSummary` is the key untested path. Use `SSEBuilder` to mock the LLM summary response, then verify the compactor produces correct summaries.

### 6. Migrate agent tests to use `testutil` harness (ONGOING)

Many tests in `pkg/agent/loop_characterization_test.go` and `pkg/agent/loop_recovery_test.go` duplicate harness setup code. Over time, migrate them to use `AgentHarness` for consistency and reduced boilerplate.

### 7. Add `pkg/config/concurrency_test.go` (LOW)

Simple tests for `ResolveConcurrencyConfig` and `GetEnvInt`.

### 8. Add regression test directory (FUTURE)

Create `test/regressions/` (or `pkg/agent/test/regressions/`) with issue-numbered test files, following pi's pattern: `<issue-number>-<slug>.test.go`.