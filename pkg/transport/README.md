# pkg/transport

Transport implementations for ACP connections.

## Responsibilities

- Frame one newline-delimited message per read/write operation
- Provide stdio and network-backed connections
- Listen on and dial Unix-domain sockets
- Serialize concurrent writes to each connection

This package does not interpret ACP methods or JSON-RPC semantics; those
responsibilities belong to `pkg/protocol`.

## Testing

```bash
go test ./pkg/transport/...
```