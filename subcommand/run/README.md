# pkg/run

Run metadata and Unix-socket lifecycle support for `ai serve` and `ai run`.

## Overview

A run is a background ACP server instance. This package manages run metadata, process detection, socket lifecycle, and discovery of running or completed instances.

## RunMeta

```go
type RunMeta struct {
    ID           string `json:"id"`
    PID          int    `json:"pid"`
    CWD          string `json:"cwd"`
    Status       string `json:"status"`
    StartedAt    int64  `json:"started_at"`
    FinishedAt   int64  `json:"finished_at"`
    Name         string `json:"name"`
    ParentRun    string `json:"parent_run"`
    PidStartTime int64  `json:"pid_start_time"`
}
```

### File layout

```
~/.ai/runs/<id>/
├── run.json
├── events.jsonl
└── control.sock
```

The control socket carries ACP JSON-RPC messages. `ai watch`, `ai send`, and the `ai run` TUI connect as ACP clients; the server emits `session/update` notifications for live events and history replay.

## Key files

| File | Description |
|---|---|
| `meta.go` | Run metadata, IDs, discovery, and process detection |
| `meta_linux.go` | Linux process start-time detection |
| `socket.go` | Unix socket lifecycle and ACP connection handling |
| `event_broadcaster.go` | Event fan-out and replay support |
| `event_parser.go` | ACP update parsing for display |
| `event_renderer.go` | Display rendering helpers |
| `agent_end.go` | Locate the last completed agent turn |
| `types.go` | Display event types |