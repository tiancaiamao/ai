# Architecture Analysis: `ag` (Agent Orchestration CLI)

**Date**: 2025-07-13
**Scope**: 22 Go source files, 5181 LOC (non-test), 1654 LOC (test), 6 internal packages + cmd
**Goal**: Surface deepening opportunities that improve testability, reduce duplication, and make the codebase more AI-navigable.

---

## Module Depth Map

Each module rated by **interface surface** vs **behaviour hidden**.

| Module | LOC | Interface Size | Depth | Verdict |
|--------|-----|---------------|-------|---------|
| `storage` | 133 | 11 funcs | 🟢 Deep | Good: `AtomicWrite`, `ReadJSON`, path helpers hide FS details |
| `conv` | 360 | `ParseEvent` + stream API | 🟢 Deep | Good: event parsing, hooks, file watching all hidden behind small API |
| `task` | 734 | ~20 funcs (CRUD + state machine) | 🟡 Medium | State machine is well-factored, but YAML import has no validation layer |
| `channel` | 243 | 5 funcs (Send/Recv/List/Create/Remove) | 🟢 Deep | Good: FIFO queue abstraction over FS |
| `backend` | 147 | Load/Find/Default | 🟢 Deep | Good: YAML config with sensible defaults |
| `agent` | 153 | Validate/List/Read/EnsureExists | 🟢 Deep | Good: lifecycle over activity.json |
| `scheduler` | 582 | `RunScheduler(ctx, cfg)` | 🟡 Medium | Single entry point is good, but hardcoded `ai serve` coupling |
| `cmd/` | 2632 | 77 functions across 13 files | 🔴 Shallow | God package: CLI wiring + business logic mixed, no internal boundaries |

---

## Critical Issues

### 1. `cmd/` is a God Package (2632 LOC, 77 functions)

**Problem**: The `cmd` package is 50% of the codebase and mixes CLI wiring with substantial business logic. Files like `agent_client.go` (449 LOC) contain both command dispatch AND complex operations (Kill, Wait, parseAssistantMessages, tailBytes). No internal boundaries exist within cmd.

**Symptoms**:
- `parseAssistantMessages()` (60 LOC) + `extractAssistantTextsFromAgentEnd()` + `extractTextFromContent()` are pure event parsing logic living in `cmd/agent_client.go`
- `ai_adapter.go` is a 336-line adapter with state management (run ID caching, file I/O) that should be an internal package
- `formatted_writer.go` reimplements conv event parsing instead of using `conv.StreamEvents`
- `conversation.go` reimplements events.jsonl parsing instead of using conv

**Proposed refactor**:

```
cmd/                    → CLI wiring only (flags → call internal package)
internal/run/           → AI run management (RunMeta, run ID resolution, events.jsonl paths)
internal/run/adapter.go → AIAdapter (spawn, status, send, kill)
internal/run/parser.go  → parseAssistantMessages, extractTextsFromAgentEnd → use conv
internal/run/paths.go   → RunsDir(), EventsPath(runID), RunMetaPath(runID)
```

**Impact**: High — reduces cmd/ by ~60%, makes adapter testable in isolation, enables reuse of run management by scheduler.

---

### 2. Triplicated `isProcessAlive` (3 copies)

**Problem**: Identical function exists in 3 files:

| Location | Lines |
|----------|-------|
| `cmd/agent_status.go:221` | 6 |
| `internal/agent/agent.go:106` | 6 |
| `internal/task/scheduler.go:521` | 6 |

**Fix**: Move to `internal/agent/agent.go` (already has it) and export it. Delete the other two copies. Both `cmd/` and `task/` already import `agent`.

---

### 3. Scattered `~/.ai/runs/` Path Knowledge (9 call sites)

**Problem**: The path pattern `filepath.Join(homeDir, ".ai", "runs", runID, ...)` appears in 9 places across 5 files:

- `cmd/ai_adapter.go` (3 sites)
- `cmd/agent_client.go` (2 sites)
- `cmd/agent_status.go` (2 sites)
- `cmd/tail.go` (1 site)
- `cmd/conversation.go` (1 site)
- `internal/task/scheduler.go` (2 sites)

Each call independently resolves `os.UserHomeDir()` and constructs the path. No single source of truth.

**Fix**: Create `internal/run/paths.go`:

```go
package run

func RunsDir() string                        // ~/.ai/runs/
func Dir(runID string) string                 // ~/.ai/runs/<id>/
func EventsPath(runID string) string          // ~/.ai/runs/<id>/events.jsonl
func MetaPath(runID string) string            // ~/.ai/runs/<id>/run.json
func ReadMeta(runID string) (*RunMeta, error) // parse run.json
```

---

### 4. `parseAssistantMessages` Duplicates conv Logic

**Problem**: `cmd/agent_client.go:299-393` manually parses events.jsonl (message_update, agent_end, turn_end) to extract assistant text. This duplicates what `conv.ParseEvent` + `conv.StreamEvents` already provide, but handles additional cases:
- Accumulates text deltas across streaming events
- Extracts full messages from agent_end's `messages` array
- Splits by turn boundaries

**Fix**: Extend `conv` package with a `ConversationBuilder` that accumulates streaming text into complete messages, replacing both `parseAssistantMessages` and `conversation.go`'s `parseConversation`.

```go
// conv/conversation.go
type ConversationBuilder struct { ... }
func (b *ConversationBuilder) ProcessEvent(evt *FormattedEvent) Message
func BuildConversation(data []byte) ([]Message, error)
```

This would unify 3 separate event-parsing implementations (agent_client, conversation, tail) into 1.

---

### 5. `formatted_writer.go` Doesn't Use conv.StreamEvents

**Problem**: `FormattedStreamWriter.WriteJSONEvents` manually calls `conv.ParseEvent(line)` in a loop with its own scanner. It could use `conv.StreamEvents(reader, hook)` instead, but the hook would need the `FormattedStreamWriter` passed through.

**Fix**: Refactor to use `conv.StreamEvents` with a closure hook. Reduces WriteJSONEvents from 20 lines to ~5.

---

### 6. Scheduler Hardcodes `ai serve` as Only Worker Backend

**Problem**: `spawnWorker` directly constructs `exec.Command("ai", "serve", ...)` with no abstraction. Adding alternative backends (e.g., codex, raw mode) requires modifying scheduler internals.

**Fix**: Extract a `WorkerBackend` interface:

```go
type WorkerBackend interface {
    Spawn(ctx context.Context, taskID, prompt, workDir string) (runID string, err error)
    IsDone(runID string) (done bool, summary string, err error)
}
```

Current `ai serve` becomes `AIServeBackend`. Future backends (codex, local) plug in without touching scheduler logic.

---

## Moderate Issues

### 7. Chinese Comments in Production Code

**Files**: `ai_adapter.go`, `conversation.go`, `formatted_writer.go`
**Rule**: Per AGENTS.md, code comments should be English-only.
**Fix**: Translate Chinese comments to English.

### 8. `tail.go` Cursor Model Leaks Raw Events

The `parseEventsTail` function re-parses events.jsonl from byte offset, which breaks if a single JSON event spans the offset boundary. The current code handles this with a "skip to next newline" heuristic, but a cleaner approach would use event-line offsets instead of byte offsets.

### 9. No Error Wrapping in agent/adapter Layer

`ai_adapter.go` uses `fmt.Errorf("...: %w", err)` in some places but raw `fmt.Errorf` in others. Consistent error wrapping with `%w` would improve debuggability.

---

## Proposed ADRs

See `docs/adr/` for specific decisions.

---

## Priority Ranking

| Priority | Issue | Effort | Impact |
|----------|-------|--------|--------|
| P0 | Extract `internal/run` package (paths + adapter + parser) | M | High — eliminates 9 duplicated paths, enables testing |
| P0 | Unify event parsing into conv (ConversationBuilder) | M | High — eliminates 3 duplicate parsers |
| P1 | Deduplicate `isProcessAlive` | S | Low — 18 lines saved, but removes confusion |
| P1 | Translate Chinese comments to English | S | Low — consistency |
| P2 | WorkerBackend interface in scheduler | M | Medium — extensibility |
| P2 | formatted_writer uses conv.StreamEvents | S | Low — consistency |