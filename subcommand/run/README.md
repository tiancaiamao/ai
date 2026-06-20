# pkg/run

Run metadata, Unix domain socket server, and event broadcasting for `ai serve`/`ai run`.

## Overview

A "run" is a single invocation of `ai rpc` — a background agent process. This package manages run lifecycle metadata, inter-process communication via Unix domain sockets, and event broadcasting to attached watchers.

## RunMeta

```go
type RunMeta struct {
    ID           string `json:"id"`             // 6-char hex ID (crypto/rand)
    PID          int    `json:"pid"`             // Process ID
    CWD          string `json:"cwd"`             // Working directory
    Status       string `json:"status"`          // "running", "done", "failed", "killed"
    StartedAt    int64  `json:"started_at"`      // Unix timestamp
    FinishedAt   int64  `json:"finished_at"`     // 0 if running
    Name         string `json:"name"`            // Optional human-readable name
    ParentRun    string `json:"parent_run"`      // Parent run ID (subagents)
    PidStartTime int64  `json:"pid_start_time"`  // Process start epoch (PID reuse detection)
}
```

### File Layout

```
~/.ai/runs/<id>/
├── run.json          # RunMeta JSON
├── events.jsonl      # Event log (replay for late-attaching watchers)
└── control.sock      # Unix domain socket for commands
```

### Process Detection

`IsRunning(meta)` checks:
1. Status is `"running"`
2. Process with `meta.PID` exists
3. PID start time matches `meta.PidStartTime` (prevents false positives from PID reuse)

### Discovery

```go
func FindRunningByCwd(baseDir, cwd string) ([]RunMeta, error)
func FindByPrefix(baseDir, prefix string) ([]RunMeta, error)
```

## SocketServer

Unix domain socket server for run control and event streaming.

```go
type SocketServer struct { ... }

func NewSocketServer(sockPath string, handler CommandHandler) *SocketServer
```

### Commands

```go
type Command struct {
    Type    string `json:"type"`              // "prompt", "steer", "abort", "stream", "get_state"
    Message string `json:"message"`
    FromSeq uint64 `json:"from_seq,omitempty"` // For "stream": replay from this sequence
}
```

### Event Broadcasting

`EventBroadcaster` provides fan-out event delivery:

```go
type EventBroadcaster struct { ... }

func (b *EventBroadcaster) Push(event []byte)       // Send to all subscribers
func (b *EventBroadcaster) Subscribe(fromSeq uint64) *Consumer  // Create subscriber
```

Consumers receive events via a ring buffer. Late-joining consumers can replay from a sequence number.

## Key Files

| File | Description |
|------|-------------|
| `meta.go` | `RunMeta`, `GenerateID`, discovery functions, `IsRunning` process detection |
| `meta_linux.go` | Linux-specific process start time detection (build tag: linux) |
| `socket.go` | `SocketServer`, `Command`, `CommandHandler`, Unix domain socket handling |
| `event_broadcaster.go` | `EventBroadcaster`, `Consumer` — ring-buffer fan-out with replay |
| `event_parser.go` | `ParseEvent` — parse raw JSONL event lines into `FormattedEvent` |
| `event_renderer.go` | Event rendering for terminal display |
| `agent_end.go` | `AgentEndInfo`, `FindLastAgentEnd` — locate last agent_end in event log |
| `types.go` | `EventKind`, `FormattedEvent` — formatted output types |