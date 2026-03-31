# RPC Integration for AgentNew

This document describes how to integrate AgentNew with the RPC layer.
It shows the minimal integration required to use the new agent implementation.

## Overview

The `rpc_handlers_new.go` file provides the integration between the new AgentNew implementation and the existing RPC interface. This allows the new agent to be used without changing the client-side RPC protocol.

## Key Components

### 1. AgentNewServer

A wrapper struct that adapts AgentNew to the RPC interface:

```go
type AgentNewServer struct {
    agent          *agent.AgentNew
    sessionDir     string
    sessionID      string
    model          *llm.Model
    sessionMgr     *session.SessionManager
    // ... other fields
}
```

### 2. SetupAgentNewHandlers

Configures the RPC server to use AgentNew:

```go
func SetupAgentNewHandlers(
    server *rpc.Server,
    agentNewServer *AgentNewServer,
)
```

### 3. LoadOrNewAgentSession

Loads an existing session or creates a new one:

```go
func LoadOrNewAgentSession(
    sessionPath string,
    sessionMgr *session.SessionManager,
    model *llm.Model,
    apiKey string,
    registry *tools.Registry,
    skills []*skill.Skill,
    workspace *tools.Workspace,
) (*AgentNewServer, *session.Session, error)
```

## Integration Example

To use AgentNew in `runRPC()`, replace the existing agent initialization:

```go
// Load or create session using AgentNew
agentNewServer, sess, err := LoadOrNewAgentSession(
    sessionPath,
    sessionMgr,
    model,
    apiKey,
    registry,
    skillResult.Skills,
    ws,
)
if err != nil {
    return fmt.Errorf("failed to load/create agent session: %w", err)
}

// Create RPC server
server := rpc.NewServer()
server.SetOutput(output)

// Set up handlers to use AgentNew
SetupAgentNewHandlers(server, agentNewServer)

// Connect event emission
agentNewServer.SetEventEmitter(server)
```

## Migration Notes

When migrating from old Agent to AgentNew:

1. **Session loading**: Use `LoadOrNewAgentSession()` instead of `session.LoadSessionLazy()`
2. **Agent creation**: Use `NewAgentNewServer()` instead of `agent.NewAgentFromConfigWithContext()`
3. **RPC handlers**: Use `SetupAgentNewHandlers()` instead of individual handler setup
4. **Event emission**: Via `agentNewServer.SetEventEmitter(server)`
5. **Session persistence**: Automatic via journal (messages.jsonl)
6. **Context management**: Automatic trigger detection and checkpointing

## Compatibility

The RPC interface remains the same - clients don't need to change. All existing RPC methods work identically:

- `prompt`: User message → agent response
- `steer`: Interrupt and redirect
- `follow_up`: Add message to queue
- `abort`: Stop execution
- `get_state`: Get agent state
- `get_messages`: Get conversation history

## Benefits of AgentNew

1. **Automatic context management**: Triggers based on token usage, stale outputs, turn count
2. **Better persistence**: Event-sourced journal (messages.jsonl) + checkpoint system
3. **Cleaner architecture**: Separation of LLM context vs recent messages
4. **More observability**: Turn tracking, token estimation, trigger logging

## Implementation Status

**Phase 7: RPC Integration - Task 7.1**

✅ Created `cmd/ai/rpc_handlers_new.go`
✅ AgentNewServer wrapper with RPC-compatible methods
✅ SetupAgentNewHandlers to configure RPC handlers
✅ LoadOrNewAgentSession for session management
✅ rpcEventEmitterAdapter for event forwarding
✅ Code compiles successfully
✅ Existing tests still pass

## Testing

To test AgentNew integration:

1. Build the binary: `go build -o ai_new ./cmd/ai`
2. Run with existing sessions: `./ai_new --mode rpc < input.jsonl`
3. Verify checkpoint creation: `ls -la ~/.ai/sessions/--<cwd>--/checkpoints/`
4. Verify journal: `cat ~/.ai/sessions/--<cwd>--/messages.jsonl`
5. Check trace events: `grep "context_mgmt" ~/.ai/traces/<latest>.json`

## Next Steps

For Phase 8 (Observability):
- Add comprehensive trace events
- Test with real conversations
- Performance benchmarking
- Documentation updates

## Notes

- The integration is minimal and focused on getting AgentNew working via RPC
- Event emission is simplified - can be enhanced later
- Session management reuses existing session package
- Tools and skills integration unchanged
- Model and API key handling unchanged

## See Also

- `/Users/genius/project/ai/design/tasks.md` (Task 7.1)
- `/Users/genius/project/ai/pkg/agent/agent_new.go` (AgentNew implementation)
- `/Users/genius/project/ai/pkg/agent/loop_normal.go` (Normal mode execution)
- `/Users/genius/project/ai/pkg/agent/loop_context_mgmt.go` (Context management mode)
