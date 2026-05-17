# pkg/traceevent

Perfetto-compatible tracing with configurable event categories and slog bridge.

## Overview

Provides structured tracing for the agent runtime. Events are written as Perfetto-compatible JSON traces that can be viewed in Chrome's `chrome://tracing` UI or Perfetto UI (ui.perfetto.dev). The package also includes a `slog` handler bridge that redirects all structured logs into the trace event stream.

## Core Types

### TraceEvent

```go
type TraceEvent struct {
    Timestamp time.Time
    Name      string
    Phase     TracePhase    // "B" (begin), "E" (end), "I" (instant)
    Category  TraceCategory
    Fields    []Field
}
```

### TraceBuf

Buffered trace writer. Obtained from context via `GetTraceBuf(ctx)`. Writes events to a Perfetto-compatible JSON file at `~/.ai/traces/<id>.json`.

## Event Configuration

Events are configured via bit flags in `config.go`:

```go
// Enable/disable specific events at runtime
traceevent.IsEventEnabled("tool_execution") // Check if an event is active
```

Event categories:
- `tool` — Tool execution, truncation, normalization
- `llm` — API calls, streaming, retries
- `event` — Agent lifecycle, turns, messages
- `log` — slog bridge output (info/warn/error)

Events are selected by name or category selector. Default enabled events are defined in `defaultEnabledEvents`.

## API

### Recording Events

```go
// Instant event
traceevent.Log(ctx, traceevent.CategoryTool, "tool_execution_start",
    traceevent.String("tool", "bash"),
    traceevent.Int("timeout", 120))

// Begin/End (duration)
traceevent.Begin(ctx, traceevent.CategoryLLM, "llm_stream", ...)
traceevent.End(ctx, traceevent.CategoryLLM, "llm_stream", ...)

// Convenience wrappers
traceevent.Error(ctx, traceevent.CategoryTool, "tool_failed", "message", "error", err)
```

### Slog Bridge

```go
handler := traceevent.WrapSlogHandler(innerHandler)
logger := slog.New(handler)
```

All `logger.Info/Warn/Error` calls are converted to trace events in addition to their normal output.

### Buffer Management

```go
type TraceBuf struct { ... }

func (tb *TraceBuf) Record(event TraceEvent)
func (tb *TraceBuf) RecordWithContext(ctx context.Context, event TraceEvent)
func (tb *TraceBuf) Flush() error
```

Buffered writes with periodic flush. Flush is triggered every 256 events or 1 second, whichever comes first.

## Key Files

| File | Description |
|------|-------------|
| `trace.go` | `Log`, `Begin`, `End`, `Error` — event recording functions |
| `types.go` | `TraceEvent`, `Field`, `TracePhase`, `TraceCategory` |
| `config.go` | Bit-flag event selection, `IsEventEnabled()` |
| `buffer.go` | `TraceBuf` — buffered Perfetto JSON writer |
| `perfetto.go` | Perfetto JSON format serialization |
| `slog_bridge.go` | `slog.Handler` wrapper for trace integration |
| `handler.go` | Context integration for TraceBuf |

## Output Format

Trace files are Perfetto-compatible JSON:

```json
[{"ph":"I","name":"tool_execution","cat":"tool","ts":1234567890,"pid":1,"tid":1,"args":{"tool":"bash"}},...]
```

View at `chrome://tracing` or https://ui.perfetto.dev.