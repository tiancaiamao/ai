# pkg/rpc

ACP protocol server, client, handlers, and shared application types for the AI agent.

## Responsibilities

- Implement the ACP request/notification surface
- Route session and configuration requests to the agent
- Translate agent events into ACP `session/update` notifications
- Load and replay persisted sessions
- Render slash-command results for local and external clients
- Record session and trace events through the shared application layer

`pkg/rpc` is the ACP kernel. It is protocol-focused and does not own the wire transport; `pkg/transport` provides stdio and Unix-socket connections.

## ACP methods

The server implements `initialize`, `session/new`, `session/load`, `session/prompt`, `session/cancel`, and `session/set_config_option`. Session updates are emitted as JSON-RPC notifications. Unsupported ACP methods return method-not-found.

`session/load` restores a persisted session and replays its history. Diagnostic events use ACP's `_`-prefixed session-update extension mechanism, including `_compaction`, `_error`, `_llm_retry`, `_loop_guard`, and `_tool_call_recovery`.

## Shared rendering

`FormatCommandResult` in `render.go` is the pure formatter used for slash-command results by the ACP server and local TUI.

## Testing

```bash
go test ./pkg/rpc/...
```

## See also

- [ACP protocol reference](../../docs/rpc-protocol.md)
- [System architecture](../../docs/architecture.md)
- [Agent core](../agent/README.md)