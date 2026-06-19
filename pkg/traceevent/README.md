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
    Phase     Phase           // "B" (begin), "E" (end), "I" (instant), "X" (complete), "C" (counter)
    TraceID   []byte
    SpanID    []byte          // Parent span ID for nested events
    Fields    []Field
    Category  TraceCategory
}
```

### TraceCategory (bit flags)

```go
const (
    CategoryLLM     TraceCategory = 1 << iota // LLM calls
    CategoryTool                              // Tool execution
    CategoryEvent                             // Agent events
    CategoryMetrics                           // Performance metrics
    CategoryLog                               // Log events (from slog)
)
```

### Phase

```go
const (
    PhaseBegin    Phase = "B"
    PhaseEnd      Phase = "E"
    PhaseComplete Phase = "X"
    PhaseInstant  Phase = "I"
    PhaseCounter  Phase = "C"
)
```

### Field

```go
type Field struct {
    Key   string
    Value interface{}
}
```

### TraceBuf

Buffered trace writer. Obtained from context via `GetTraceBuf(ctx)`. Writes events to Perfetto-compatible JSON files via a configurable `TraceHandler`.

## Event Configuration

Events are configured via bit flags in `config.go`:

```go
traceevent.IsEventEnabled("tool_execution") // Check if an event is active
traceevent.EnableEvent("text_delta")        // Enable a specific event
traceevent.DisableEvent("text_delta")       // Disable a specific event
traceevent.GetEnabledEvents()               // List currently enabled events
traceevent.ResetToDefaultEvents()           // Reset to defaults
```

Event categories (selector groups):
- `tool` — Tool execution, truncation, normalization
- `llm` — API calls, streaming, retries
- `event` — Agent lifecycle, turns, messages
- `log` — slog bridge output (info/warn/error)
- `metrics` — Performance metrics

Events are selected by name or category selector via `ExpandEventSelectors()`. Default enabled events are defined in `defaultEnabledEvents`.

## API

### Recording Events

```go
// Instant event
traceevent.Log(ctx, traceevent.CategoryTool, "tool_execution_start",
    traceevent.Field{Key: "tool", Value: "bash"},
    traceevent.Field{Key: "timeout", Value: 120})

// Error event (key/value pairs)
traceevent.Error(ctx, traceevent.CategoryTool, "message", "key", value)
```

### Spans

```go
span := traceevent.StartSpan(ctx, "llm.StreamLLM", traceevent.CategoryLLM)
defer span.End()

// Child spans
child := span.StartChild("sub_operation")
defer child.End()

// Add fields mid-span
span.AddField("result", "ok")
```

### Slog Bridge

```go
handler := traceevent.WrapSlogHandler(innerHandler)
logger := slog.New(handler)
```

All `logger.Info/Warn/Error` calls are converted to trace events. Debug-level logs get fine-grained event names (`log:<slug>`).

### Buffer Management

```go
type TraceBuf struct { ... }

func NewTraceBuf() *TraceBuf
func (tb *TraceBuf) Record(event TraceEvent)
func (tb *TraceBuf) RecordWithContext(ctx context.Context, event TraceEvent)
func (tb *TraceBuf) Flush(ctx context.Context) error
func (tb *TraceBuf) FlushIfNeeded(ctx context.Context) error
func (tb *TraceBuf) DiscardOrFlush(ctx context.Context) error
func (tb *TraceBuf) Snapshot() []TraceEvent
func (tb *TraceBuf) AddSink(sink func(TraceEvent))
```

Buffered writes with incremental flush. Flush is triggered by event count (`flushEvery`) or time interval (`flushInterval`), whichever comes first.

### Handlers

```go
// File handler — writes Perfetto JSON to a directory
handler, err := traceevent.NewFileHandler(outputDir)
traceevent.SetHandler(handler)

// Interfaces
type TraceHandler interface {
    Handle(ctx context.Context, traceID []byte, events []TraceEvent) error
}
type ChunkTraceHandler interface {
    HandleChunk(ctx context.Context, traceID []byte, events []TraceEvent, final bool) error
}
```

## Key Files

| File | Description |
|------|-------------|
| `trace.go` | `Log`, `Error` — event recording functions |
| `types.go` | `TraceEvent`, `Field`, `Phase`, `TraceCategory`, `Span`, `StartSpan` |
| `config.go` | Bit-flag event selection, `IsEventEnabled`, `EnableEvent`, `ExpandEventSelectors` |
| `buffer.go` | `TraceBuf` — buffered event recording, context integration, `FlushIfNeeded` |
| `perfetto.go` | `PerfettoFile`, `WritePerfettoFile` — Perfetto JSON format serialization |
| `slog_bridge.go` | `WrapSlogHandler` — `slog.Handler` wrapper for trace integration |
| `handler.go` | `TraceHandler`, `FileHandler` — global handler, file-based trace output |

## Output Format

Trace files are Perfetto-compatible JSON:

```json
[{"ph":"I","name":"tool_execution","cat":"tool","ts":1234567890,"pid":1,"tid":1,"args":{"tool":"bash"}},...]
```

View at `chrome://tracing` or https://ui.perfetto.dev.