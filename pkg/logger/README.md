# pkg/logger

Slog logger with traceevent bridge.

## Overview

Creates a `slog.Logger` that redirects all log output into the traceevent system. No log files are written — all structured logging becomes trace events visible in Perfetto traces.

## API

```go
func NewLogger(cfg *Config) (*slog.Logger, error)
func NewDefaultLogger() *slog.Logger
```

Creates a logger that wraps a discard handler with the `traceevent.WrapSlogHandler` bridge. Event filtering is handled entirely by the traceevent config.

## Key Files

| File | Description |
|------|-------------|
| `logger.go` | Logger construction with traceevent bridge |

## Dependencies

- `pkg/traceevent` — `WrapSlogHandler` for slog → trace bridge