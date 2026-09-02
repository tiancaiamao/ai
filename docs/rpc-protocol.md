# ACP Protocol Reference

The `ai` agent exposes the [Agent Client Protocol](https://agentclientprotocol.com/) as its only public programmatic protocol. ACP uses JSON-RPC 2.0 messages framed as newline-delimited JSON (NDJSON).

## Transports

The same ACP protocol is available over two transports:

- **stdio**: `ai acp` reads from stdin and writes to stdout. This is intended for editor and external ACP clients.
- **Unix socket**: `ai serve` listens at `~/.ai/runs/<id>/control.sock`. `ai run`, `ai watch`, and `ai send` connect to this socket as ACP clients.

Transport framing is one JSON-RPC message per line. The protocol implementation lives in `pkg/rpc`; transport implementations live in `pkg/transport`.

## Methods

The implemented ACP surface is intentionally minimal:

| Method | Direction | Description |
|---|---|---|
| `initialize` | request | Negotiate ACP protocol version and capabilities. |
| `session/new` | request | Establish the process's active session and advertise slash commands. |
| `session/load` | request | Load a persisted session and replay its history as `session/update` notifications. |
| `session/prompt` | request | Submit a text prompt and wait for its `stopReason`. |
| `session/cancel` | notification | Cancel the active turn. |
| `session/set_config_option` | request | Switch the active model; aliases are also accepted. |
| `session/update` | notification | Server-to-client stream of session updates. |

Unsupported ACP methods, including `fs/*`, `terminal/*`, and MCP transports, return JSON-RPC method-not-found. `mcpServers` in `session/new` is accepted and ignored.

## Session prompt

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/prompt",
  "params": {
    "sessionId": "session-id",
    "prompt": [{"type": "text", "text": "Fix the bug in auth.go"}]
  }
}
```

The response contains a stop reason, for example:

```json
{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}
```

The supported stop reasons are `end_turn` and `cancelled`.

## Session updates

Updates are JSON-RPC notifications with this envelope:

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": {
    "sessionId": "session-id",
    "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Hello"}}
  }
}
```

Standard update values currently emitted include:

| `sessionUpdate` | Description |
|---|---|
| `user_message_chunk` | User prompt or replayed user message. |
| `agent_message_chunk` | Streaming assistant text. |
| `agent_thought_chunk` | Streaming assistant reasoning text. |
| `tool_call` | Tool execution started. |
| `tool_call_update` | Tool execution completed or failed. |
| `available_commands_update` | Available slash commands. |
| `config_option_update` | Updated model/configuration catalog. |
| `usage_update` | Context token usage. |

The implementation also emits `_`-prefixed extensions for diagnostics:
`_compaction`, `_error`, `_llm_retry`, `_loop_guard`, `_tool_call_recovery`,
and `_turn_end`. Extension payloads are carried in `_meta`; standard ACP
clients may ignore them.

### Custom update payloads

The five diagnostic updates carry the corresponding agent event data in
`_meta`. `_compaction` additionally includes `status` (`start` or `end`).
`_turn_end` carries the completed turn metadata used by local clients to know
when a live prompt has finished.

## Session recovery

`session/load` uses the ACP session ID as the persisted session ID, restores the session, and replays its history before returning. This supports attaching to a running agent and recovering after a serve process restart without a separate attach protocol.

## Slash commands

Operational commands such as `/model`, `/compact`, `/help`, `/resume`, and `/fork` are sent as text through `session/prompt`. See `pkg/command` for the registry.

## Error handling

Errors use standard JSON-RPC error responses. Streaming failures and diagnostics are exposed through the `_error` ACP session update.