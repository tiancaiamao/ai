# pkg/rpc

RPC server, handlers, and types for the AI agent system.

## Overview

The `rpc` package implements the RPC application layer — JSON-RPC server, request/response handlers, and shared types for agent interactions. This package bridges the CLI layer (`pkg/cli`) and the core agent logic (`pkg/agent`).

**Key responsibilities:**

- JSON-RPC server (stdin/stdout I/O loop)
- Request routing to handlers (messages, config, session, etc.)
- Event streaming to clients
- Session writer for recording agent events
- Trace event handling for Perfetto integration

## Core Types

### RPCApp

`RPCApp` is the main application struct that holds all components:

```go
type RPCApp struct {
    agent           *agent.Agent
    agentCtx        *agentctx.AgentContext
    sessionWriter   *sessionWriter.SessionWriter
    // ... other fields
}
```

### Handlers

Request handlers are implemented as methods on `RPCApp`:

| Handler | Purpose |
|---------|---------|
| `HandleInit()` | Initialize agent with config |
| `HandleStream()` | Start agent execution with streaming |
| `HandleMessages()` | Send messages to agent (without streaming) |
| `HandleStop()` | Stop current agent turn |
| `HandleGetConfig()` | Get current configuration |
| `HandleSetConfig()` | Update configuration |
| `HandleListSessions()` | List available sessions |
| `HandleLoadSession()` | Load a previous session |

### Types

| Type | Purpose |
|------|---------|
| `AgentConfig` | Agent configuration (model, tools, limits) |
| `StreamRequest` | Request for streaming agent execution |
| `StreamResponse` | Response with streaming events |
| `SessionInfo` | Session metadata |

## Request/Response Flow

```
Client → JSON-RPC → RPCApp → Agent
Client ← JSON-RPC ← RPCApp ← Agent
```

1. Client sends JSON-RPC request (stdin)
2. `rpc_server` reads and parses request
3. `rpc_handlers` routes to appropriate handler
4. Handler calls agent methods (e.g., `Agent.RunTurn`)
5. Agent emits events (via channel)
6. `RPCApp` streams events back to client

## Event Streaming

Events are streamed to clients as they're generated:

```go
for event := range agent.Events() {
    resp := &types.StreamResponse{
        Event: event,
    }
    rpc.WriteMessage(resp)
}
```

Supported event types:
- Text chunks (LLM output)
- Tool calls
- Tool results
- Status updates

## Session Writer

`SessionWriter` records agent events to JSONL format for persistence:

```go
type SessionWriter struct {
    file   *os.File
    encoder *json.Encoder
}
```

Events are appended as JSON lines for crash-safe recovery.

## Testing

Run tests with:

```bash
go test ./pkg/rpc/...
```

Integration tests (`rpcapp_smoke_test.go`) test end-to-end RPC interactions.

## See Also

- [architecture.md](../../docs/architecture.md) - System architecture
- [rpc-protocol.md](../../docs/rpc-protocol.md) - RPC protocol specification
- [pkg/agent](../agent/README.md) - Agent core logic