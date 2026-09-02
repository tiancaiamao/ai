# pkg/app

Application runtime, agent/session setup, handlers, persistence, and shared
command-result rendering for the AI agent.

## Responsibilities

- Own application and agent lifecycle setup
- Route session and configuration requests to the agent runtime
- Load and replay persisted sessions
- Render slash-command results for local and external clients
- Record session and trace events through the shared application layer

`pkg/app` exposes the runtime consumed by `pkg/protocol`. It does not own ACP
message handling or wire transport; those responsibilities live in
`pkg/protocol` and `pkg/transport` respectively.

## Testing

```bash
go test ./pkg/app/...
```

## See also

- [ACP protocol reference](../../docs/rpc-protocol.md)
- [System architecture](../../docs/architecture.md)
- [Agent core](../agent/README.md)
