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

| Type | Description | Data Field |
|------|-------------|------------|
| `prompt` | Send a user message | `PromptRequest` |
| `steer` | Inject a system message mid-conversation | String message |
| `follow_up` | Queue a prompt for after current processing | String message |
| `abort` | Cancel the current LLM stream | — |
| `get_context` | Retrieve current agent context | — |
| `set_context` | Update agent context | Context object |
| `slash_command` | Execute a slash command | Command string |
| `workflow_init` | Initialize workflow execution | `WorkflowState` |
| `workflow_start` | Begin workflow processing | — |
| `workflow_heartbeat` | Periodic workflow status check | — |

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

| Type | Description |
|------|-------------|
| `agent_event` | Agent lifecycle and stream events |
| `session_event` | Session save/load/compact notifications |
| `workflow_event` | Workflow state transitions |
| `context_limit_recovery_event` | Context overflow recovery started |
| `compaction_event` | Context compaction completed |
| `ready` | Agent initialized and ready for commands |

#### Agent Events (within `agent_event` envelope)

The `data` field of `agent_event` contains an `AgentEvent` with its own `type` discriminator:

| Event Type | Description |
|------------|-------------|
| `agent_start` | Agent run started |
| `agent_end` | Agent run completed |
| `turn_start` | New LLM turn begins |
| `turn_end` | LLM turn completed |
| `text_delta` | Streaming text chunk |
| `thinking_delta` | Reasoning/thinking content chunk |
| `tool_call_delta` | Partial tool call (name + arguments) |
| `tool_execution_start` | Tool execution begins |
| `tool_execution_end` | Tool execution completed |
| `message_update` | Full message snapshot |
| `error` | Error during processing |
| `checkpoint` | Session checkpoint saved |
| `compaction` | Context compaction performed |
| `context_limit_recovery` | Context overflow recovery |
| `tool_call_recovery` | Malformed tool call auto-recovered |
| `llm_retry` | LLM API call retry |

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

Workflow events carry a `WorkflowState` payload:

```json
{
  "type": "workflow_event",
  "data": {
    "phase": "worker",
    "tasksFile": "/path/to/tasks.md",
    "totalTasks": 5,
    "pendingTasks": 3,
    "doneTasks": 2,
    "failedTasks": 0,
    "inProgressTask": {
      "id": "task-1",
      "description": "Implement feature X",
      "status": "in_progress"
    },
    "startedAt": "2025-01-15T10:30:00Z",
    "lastUpdate": "2025-01-15T10:45:00Z"
  }
}
```

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