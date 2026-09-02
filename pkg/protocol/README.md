# pkg/protocol

ACP protocol server and client implementation. This package owns JSON-RPC
message handling and session/update translation, but not application setup or
wire transport.

## Responsibilities

- Serve ACP requests and notifications
- Translate agent events into ACP `session/update` notifications
- Provide the ACP client used by local frontends
- Depend only on the explicit `Runtime` and `Conn` interfaces

Concrete stdio and Unix-socket connections are provided by `pkg/transport`;
application lifecycle and agent/session setup are provided by `pkg/app`.

## Testing

```bash
go test ./pkg/protocol/...
```

## See also

- [ACP protocol reference](../../docs/rpc-protocol.md)
- [System architecture](../../docs/architecture.md)