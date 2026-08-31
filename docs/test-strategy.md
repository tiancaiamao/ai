# Test Strategy

## Overview

This document outlines the testing strategy for the `ai` project, covering unit tests, integration tests, regression tests, and E2E benchmark tests.

## Test Pyramid

```
        E2E Benchmark Tests (benchmark/)
              ↓
      Integration Tests
              ↓
        Unit Tests
              ↓
    Regression Tests (guardrails)
```

## Layer 1: Unit Tests (Fast, Focused)

### Purpose

Test individual functions and methods in isolation.

### Key Test Files

| Package | Test File | Focus |
|---------|-----------|-------|
| `pkg/agent` | `agent_test.go` | Agent lifecycle, config |
| `pkg/agent` | `loop_test.go` | Loop behavior, telemetry |
| `pkg/agent` | `executor_test.go` | Tool execution pool |
| `pkg/agent` | `tool_output_test.go` | Output normalization |
| `pkg/agent` | `tool_guard_test.go` | Guard rail enforcement |
| `pkg/agent` | `tool_call_normalize_test.go` | Tool call parsing |
| `pkg/agent` | `result_test.go` | Result processing |
| `pkg/agent` | `error_stack_test.go` | Error chain tracking |
| `pkg/agent` | `checkpoint_manager_test.go` | AgentState persistence |
| `pkg/compact` | `compact_test.go` | Compaction logic |
| `pkg/compact` | `context_management_test.go` | LLM-driven context management |
| `pkg/compact` | `compact_tool_pairing_test.go` | Compact tool integration |
| `pkg/config` | `config_test.go` | Configuration loading |
| `pkg/config` | `auth_test.go` | API key resolution |
| `pkg/config` | `models_test.go` | Model spec handling |
| `pkg/context` | `checkpoint_test.go` | AgentState save/load |
| `pkg/llm` | `client_test.go` | LLM client |
| `pkg/llm` | `errors_test.go` | Error classification |
| `pkg/prompt` | `builder_test.go` | Prompt construction |
| `pkg/rpc` | `server_test.go` | RPC server |
| `pkg/session` | `session_test.go` | Session CRUD |
| `pkg/session` | `lazy_test.go` | Lazy loading |
| `pkg/session` | `compaction_test.go` | Session compaction |
| `pkg/skill` | `skill_test.go` | Skill loading |
| `pkg/skill` | `integration_test.go` | Skill discovery |
| `pkg/tools` | `bash_timeout_test.go` | Bash timeout handling |
| `pkg/tools` | `bash_sleep_test.go` | Sleep detection |
| `pkg/tools` | `grep_test.go` | Grep tool |
| `pkg/tools` | `read_test.go` | Read tool |
| `pkg/tools` | `hashline_test.go` | Hashline mode |
| `pkg/traceevent` | `trace_test.go` | Trace recording |
| `pkg/traceevent` | `slog_bridge_test.go` | Log bridge |
| `pkg/traceevent` | `config_selectors_test.go` | Event configuration |
| `pkg/truncate` | `truncate_test.go` | Output truncation |

### Running Unit Tests

```bash
# Run all tests
go test ./...

# Run specific package
go test ./pkg/agent -v

# Run with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

# Run a single test
go test -run TestRegression001 ./pkg/agent -v
```

## Layer 2: Integration Tests

### Purpose

Test component interactions within the agent system.

### Key Test Files

| Test File | Focus |
|-----------|-------|
| `pkg/agent/agent_integration_test.go` | Agent + tools + session |
| `pkg/agent/agent_stress_test.go` | Concurrent agent operations |
| `pkg/agent/loop_stream_integration_test.go` | Streaming + loop interaction |
| `pkg/agent/loop_recovery_test.go` | Error recovery flows |
| `pkg/agent/loop_empty_response_test.go` | Empty response handling |
| `pkg/agent/loop_tool_parallel_test.go` | Parallel tool execution |
| `pkg/agent/conversion_visibility_test.go` | Message visibility filtering |
| `pkg/agent/llm_context_test.go` | LLM context lifecycle |
| `pkg/agent/runtime_meta_test.go` | Runtime telemetry |
| `pkg/agent/metrics_trace_test.go` | Metrics via trace events |
| `pkg/session/compaction_snapshot_test.go` | Session compaction snapshots |
| `pkg/skill/integration_test.go` | Skill loading integration |
| `cmd/ai/integration_test.go` | Full CLI integration |
| `cmd/ai/session_writer_test.go` | Session writer compaction |
| `cmd/ai/traceevent_handler_test.go` | Trace event handling |
| `cmd/ai/kill_test.go` | Agent lifecycle (kill) |
| `cmd/ai/ls_test.go` | Run listing |
| `cmd/ai/watch_test.go` | Watch TUI |
| `pkg/run/conv_test.go` | Run metadata conversion |
| `pkg/run/socket_test.go` | Socket server |
| `pkg/run/meta_test.go` | Run metadata |

### Running Integration Tests

```bash
# Run integration tests
go test -v ./pkg/agent -run Integration

# Run stress tests
go test -v ./pkg/agent -run Stress

# Run full agent tests (includes LLM calls, may be slow)
go test -v ./pkg/agent -run Agent
```

## Layer 3: Regression Tests (Guardrails)

### Purpose

Prevent regression of critical safety and behavior properties.

### Test File

`pkg/agent/regression_test.go`

### Regression Test Suite

| Test | What It Guards |
|------|---------------|
| `TestRegression001_MaxConsecutiveToolCalls` | Prevent infinite tool call loops |
| `TestRegression002_AutoCompactConfiguration` | Context overflow recovery |
| `TestRegression003_ToolCallCutoffConfiguration` | Stale tool output truncation |
| `TestRegression004_MaxToolCallsPerName` | Prevent tool abuse |
| `TestRegression005_MaxTurnsConfiguration` | Prevent runaway conversations |
| `TestRegression006_ContextWindowConfiguration` | Enforce context limits |
| `TestRegression007_LLMRetryConfiguration` | Retry on rate limit errors |
| `TestRegression008_ToolOutputLimits` | Large output truncation |
| `TestRegression009_ExecutorPoolConfiguration` | Tool execution concurrency |
| `TestRegression010_EnableCheckpointConfiguration` | Checkpoint control |
| `TestRegression011_LLMTimeoutConfiguration` | LLM call timeouts |
| `TestRegression012_RuntimeMetaConfiguration` | Runtime meta updates |

### Running Regression Tests

```bash
# Run all regression tests
go test -v -run TestRegression ./...

# Run specific regression test
go test -v -run TestRegression001 ./pkg/agent
```

**Rule:** All regression tests must pass before merging. 100% pass rate required.

## Layer 4: E2E Tests (Real Model)

### Purpose

Test complete agent behaviors against a **real, OpenAI-compatible model
endpoint** — the full pipeline: agent loop → streaming LLM client → tool
execution → multi-turn state. These are the only tests that catch protocol
drift between the code and an actual model server.

### Test Suite

Located under `pkg/e2e/` (opt-in via the `e2e` build tag — **not** part of
`make test` / CI, since they need a reachable endpoint and a live model).
Each test spawns the real `ai rpc` binary as a black box and drives it over
stdin/stdout JSON-RPC:

| Test | What It Verifies |
|------|------------------|
| `TestE2E_RealTask` | Pre-seeded buggy Go code: fix off-by-one + race condition + create SVG. Verified by `go run` / `go run -race` / XML parse |
| `TestE2E_SlashCommands` | Full server lifecycle: protocol errors → tool turns → large prompts → `/compact` → `/fork` → `/rewind` → `/new` → `/resume` → `/help` → EOF |
| `TestE2E_BusyAndAbort` | Streaming-time policies (`reject`/`cancel`/`submit`), abort |
| `TestE2E_TimeoutWatchdog` | Stall watchdog terminates the agent |
| `TestE2E_FlagsAndRoles` | CLI flags (`-max-turns`/`-session`) and `--role` wiring |
| `TestE2E_Subcommands` | `ai serve` / `ls` / `send` / `kill` lifecycle + dead-run reconcile |
| `TestE2E_DestructiveGuard` | `--role guard` destructive-command middleware reacts to `rm -rf` |
| `TestE2E_Skills` | Skill discovery from `~/.ai/skills` via `find_skill` |
| `TestLRRepro_SameBatchEvents` | Fast, deterministic log-replay regression repro (no model needed) |

### Coverage

The binary is built with `-cover` (in `TestMain`), so every spawned `ai rpc`
subprocess records coverage of the **whole application** to `GOCOVERDIR`. At
the end of the run the profiles are merged (`go tool covdata`) and the total
printed, e.g.:

```
=== E2E coverage (whole app via `ai rpc` subprocess) ===
total: (statements) 47.3%
```

This is real subprocess coverage: `pkg/rpc`, `pkg/session`, `pkg/skill`,
`cmd/ai` etc. are exercised through the same entry point a user invokes —
something agent-level tests and mock servers cannot do.

### Running E2E Tests

```bash
# Default: first ollama/* model from ~/.ai/models.json (prefers "laguna")
make e2e

# Override endpoint / model via environment variables
E2E_BASE_URL=http://localhost:11434/v1 E2E_MODEL=qwen3:8b make e2e
```

A test **skips** (does not fail) when the endpoint is unreachable or no model
is configured, so the suite can also be run on machines without the model.
Use `-v` to see which model each test targets.

### Model Resolution

1. `E2E_BASE_URL` env var wins (with `E2E_MODEL`, `E2E_PROVIDER`, `E2E_API`)
2. Otherwise the first `ollama/*` model from `~/.ai/models.json` (prefers an
   ID containing `laguna`)
3. Otherwise the test skips

## Test Best Practices

### When to Write Tests

- **Unit test**: New business logic, bug fixes (test first), refactoring
- **Integration test**: New integration points, interface changes, error paths
- **Regression test**: Immediately after fixing a bug — prevent recurrence
- **E2E test**: Complete workflow validation

### Naming Conventions

- Unit tests: `Test<Component>_<Behavior>` (e.g., `TestAgent_Prompt`)
- Regression tests: `TestRegression<NNN>_<Description>`
- Integration tests: `Test<Feature>Integration` or file suffix `_integration_test.go`

### Test Patterns

```go
// Table-driven tests
func TestToolNormalization(t *testing.T) {
    tests := []struct{
        name string
        input string
        want string
    }{
        {"basic", "hello", "hello"},
        {"truncated", longString, truncatedResult},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := normalize(tt.input)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

## CI Pipeline

### Stages

1. **Unit tests**: `go test -cover ./...`
2. **Regression tests**: `go test -run TestRegression ./...`
3. **Integration tests**: `go test -v ./pkg/agent -run Integration`
4. **E2E tests**: `make e2e` (manual, not per-PR — requires a live model endpoint)

### Pre-Commit

```bash
# Quick validation
go test ./pkg/agent -run TestRegression -count=1
go test ./... -short -count=1
```