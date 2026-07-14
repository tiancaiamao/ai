# RPC Protocol Reference

The `ai` agent communicates via a newline-delimited JSON protocol over stdin/stdout. This is the primary interface for TUI clients, editor plugins, and external tools.

## Transport

- **Input**: stdin (client → agent)
- **Output**: stdout (agent → client)
- **Encoding**: One JSON object per line (NDJSON)
- **Framing**: Newline (`\n`) delimited
- **Direction**: Bidirectional, asynchronous

When running via `ai rpc`, the agent reads commands from stdin and writes responses/events to stdout. When running via `ai serve`, the same protocol is available over a Unix domain socket at `~/.ai/runs/<id>/control.sock`.

## Message Types

### Commands (Client → Agent)

Every command has an `id` field for correlation with responses.

```json
{
  "id": "cmd-001",
  "type": "<command-type>",
  "message": "optional text body",
  "data": {}
}
```

#### Command Types

The actual protocol commands registered by the RPC server (`pkg/rpc`):

| Type | Constant | Description | Data Field |
|------|----------|-------------|------------|
| `prompt` | `CommandPrompt` | Send a user message | `PromptRequest` |

> **Note:** All other operations (state queries, settings, session management, model switching, etc.) are handled as **slash commands** sent through the `prompt` channel (e.g., `/model gpt-4`, `/compact`, `/help`). See `pkg/command` for the slash command registry.

#### PromptRequest

```json
{
  "id": "cmd-001",
  "type": "prompt",
  "data": {
    "message": "Fix the bug in auth.go",
    "streamingBehavior": "full",
    "images": [
      {"type": "image", "data": "<base64-encoded>"}
    ]
  }
}
```

- `message` (required): The user's text prompt
- `streamingBehavior`: Controls streaming granularity (`"full"`, `"minimal"`)
- `images`: Optional base64-encoded images for multimodal models

### Responses (Agent → Client)

Responses correlate to a specific command via the `id` field.

```json
{
  "id": "cmd-001",
  "type": "response",
  "command": "prompt",
  "success": true,
  "data": {},
  "error": ""
}
```

| Field | Description |
|-------|-------------|
| `id` | Matches the command `id` |
| `type` | Always `"response"` |
| `command` | The original command type |
| `success` | `true` if the command succeeded |
| `data` | Response payload (structure depends on command) |
| `error` | Error message if `success` is `false` |

### Events (Agent → Client)

Events are unsolicited messages emitted during processing. They have no `id` field.

```json
{
  "type": "agent_event",
  "data": {
    "type": "text_delta",
    "eventAt": 1705312345678901234,
    "message": {
      "role": "assistant",
      "content": [{"type": "text", "text": "Hello"}]
    }
  }
}
```

#### Event Envelope Types

Events are emitted via `server.EmitEvent()` as plain JSON objects. The `type` field discriminates the event kind:

| Type | Source | Description |
|------|--------|-------------|
| `server_start` | `rpc_app.go` | Agent initialized with model and tool list |
| `session_switch` | `rpc_session_handlers.go` | Active session changed |
| Agent event types | `pkg/agent/event.go` | All agent lifecycle/stream events (see below) |

Agent events are emitted directly (not nested under an envelope). Each has a `type` discriminator from `pkg/agent/event.go`:

| Event Type | Constant | Description |
|------------|----------|-------------|
| `agent_start` | `EventAgentStart` | Agent run started |
| `agent_end` | `EventAgentEnd` | Agent run completed |
| `turn_start` | `EventTurnStart` | New LLM turn begins |
| `turn_end` | `EventTurnEnd` | LLM turn completed |
| `message_start` | `EventMessageStart` | Message construction begins |
| `message_end` | `EventMessageEnd` | Message construction completed |
| `message_update` | `EventMessageUpdate` | Full message snapshot |
| `text_delta` | `EventTextDelta` | Streaming text chunk |
| `thinking_delta` | `EventThinkingDelta` | Reasoning/thinking content chunk |
| `tool_call_delta` | `EventToolCallDelta` | Partial tool call (name + arguments) |
| `tool_execution_start` | `EventToolExecutionStart` | Tool execution begins |
| `tool_execution_end` | `EventToolExecutionEnd` | Tool execution completed |
| `compaction_start` | `EventCompactionStart` | Context compaction started |
| `compaction_end` | `EventCompactionEnd` | Context compaction completed |
| `loop_guard_triggered` | `EventLoopGuardTriggered` | Tool-loop protection activated |
| `tool_call_recovery` | `EventToolCallRecovery` | Malformed tool call auto-recovered |
| `error` | `EventError` | Error during processing |
| `llm_retry` | `EventLLMRetry` | LLM API call retry |

#### Tool Execution Events

```json
{
  "type": "tool_execution_start",
  "eventAt": 1705312345678901234,
  "toolCallId": "call_abc123",
  "toolName": "bash",
  "args": {"command": "ls -la"}
}
```

```json
{
  "type": "tool_execution_end",
  "eventAt": 1705312345678901234,
  "toolCallId": "call_abc123",
  "toolName": "bash",
  "result": {
    "role": "tool",
    "content": [{"type": "text", "text": "file1.txt\nfile2.txt"}]
  },
  "isError": false
}
```

## Workflow State

> **Note:** The `WorkflowState` and `WorkflowTask` types are defined in `pkg/rpc/types.go`. They were used by a workflow engine that has been removed from the codebase. The types remain in the RPC schema for backward compatibility but are no longer actively used.

### Workflow Phases

| Phase | Description |
|-------|-------------|
| `init` | Workflow initialized, tasks loaded |
| `worker` | Tasks being executed |
| `completed` | All tasks done |
| `error` | Workflow failed |

### Task States

| State | Description |
|-------|-------------|
| `pending` | Not yet started |
| `in_progress` | Currently executing |
| `done` | Completed successfully |
| `failed` | Execution failed |

## Typical Session Flow

```
Client                              Agent
  │                                   │
  │─── prompt ──────────────────────→ │
  │←── response {success: true} ─────│
  │←── agent_event {agent_start} ────│
  │←── agent_event {turn_start} ─────│
  │←── agent_event {text_delta} ─────│
  │←── agent_event {text_delta} ─────│
  │←── agent_event {tool_call_delta} │
  │←── agent_event {tool_execution_start} ─│
  │←── agent_event {tool_execution_end} ───│
  │←── agent_event {turn_end} ───────│
  │←── agent_event {turn_start} ─────│
  │←── agent_event {text_delta} ─────│
  │←── agent_event {turn_end} ───────│
  │←── agent_event {agent_end} ──────│
  │                                   │
  │─── abort ───────────────────────→ │  (optional, cancels mid-stream)
  │←── response {success: true} ─────│
```

## Unix Domain Socket Protocol

When using `ai serve` + `ai watch`, the socket server uses a similar JSON protocol:

```json
{"type": "send", "message": "Fix the bug"}
{"type": "abort"}
{"type": "stream", "from_seq": 0}
{"type": "status"}
```

Response:

```json
{"ok": true, "data": {}}
```

The `stream` command establishes a long-lived connection that replays events from `from_seq` and then streams new events in real-time.

## Error Handling

- Commands that fail return `{"success": false, "error": "description"}`
- Unknown command types receive an error response
- Event stream errors are emitted as `agent_event` with type `error`
- LLM errors trigger retry events (`llm_retry`) before final failure