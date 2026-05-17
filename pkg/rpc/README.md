# pkg/rpc

JSON-RPC server over stdin/stdout for editor and CLI integration.

## Overview

The RPC server implements a newline-delimited JSON protocol over stdin/stdout. It receives commands, dispatches to registered handlers, and emits events. This is the primary interface for TUI, editor plugins, and external tools to interact with the agent.

## Protocol

### Commands (stdin → agent)

Each command is a single JSON line:

```json
{
  "id": "unique-id",
  "type": "prompt",
  "message": "Hello, agent",
  "data": {}
}
```

**Command types:**

| Type | Description |
|------|-------------|
| `prompt` | Send a user message to the agent |
| `steer` | Inject a system message (mid-conversation guidance) |
| `follow_up` | Queue a follow-up prompt after current processing |
| `abort` | Cancel current LLM stream |
| `get_context` | Retrieve current agent context |
| `set_context` | Update agent context |
| `slash_command` | Execute a slash command (e.g., `/model`, `/compact`) |
| `workflow_init` | Initialize workflow execution |
| `workflow_start` | Start workflow processing |
| `workflow_heartbeat` | Periodic workflow status update |

**Prompt-specific data** (`PromptRequest`):

```json
{
  "type": "prompt",
  "data": {
    "message": "Fix the bug",
    "streamingBehavior": "full",
    "images": [{"type":"image","data":"base64..."}]
  }
}
```

### Responses (agent → stdout)

```json
{
  "id": "matching-command-id",
  "type": "response",
  "command": "prompt",
  "success": true,
  "data": {},
  "error": ""
}
```

### Events (agent → stdout)

Events are unsolicited JSON lines emitted during processing:

```json
{
  "type": "agent_event",
  "data": {
    "type": "text_delta",
    "message": {"role":"assistant","content":[{"type":"text","text":"Hello"}]}
  }
}
```

**Event types:**

| Type | Description |
|------|-------------|
| `agent_event` | Agent lifecycle and stream events (see `pkg/agent` event types) |
| `session_event` | Session save/load/compact events |
| `workflow_event` | Workflow state changes |
| `context_limit_recovery_event` | Context overflow recovery |
| `compaction_event` | Compaction summary |
| `ready` | Agent ready for commands |

### Workflow State

```json
{
  "type": "workflow_state",
  "data": {
    "phase": "worker",
    "tasksFile": "/path/to/tasks.md",
    "totalTasks": 5,
    "pendingTasks": 3,
    "doneTasks": 2,
    "failedTasks": 0,
    "inProgressTask": {"id":"task-1","description":"...","status":"in_progress"}
  }
}
```

## Server

```go
type Server struct { ... }
```

Thread-safe RPC server. Created with `NewServer(ctx)`:

```go
srv := rpc.NewServer(ctx)
srv.Register("prompt", handlePrompt)
srv.Register("abort", handleAbort)
srv.Run() // Blocks, reads stdin, dispatches commands
```

Key methods:
- `Register(cmdType, handler)` — Register a command handler
- `Run()` — Start reading stdin and dispatching
- `EmitEvent(event)` — Send an unsolicited event to stdout
- `SetOutput(writer)` — Override output (useful for testing)
- `Context()` — Access the server's context

## Handler Type

```go
type Handler func(cmd RPCCommand) (any, error)
```

Handlers receive the raw `RPCCommand` and return arbitrary data or an error. Parameter parsing is the handler's responsibility.

## Key Files

| File | Description |
|------|-------------|
| `server.go` | Server struct, command dispatch, Run(), EmitEvent() |
| `types.go` | Protocol types — RPCCommand, RPCResponse, event types, workflow types |

## Dependencies

- `pkg/command` — Slash command registry (re-exported for backward compatibility)