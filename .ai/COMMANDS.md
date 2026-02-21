# RPC Commands Reference

This file documents all available RPC commands for the `ai` agent.

## Basic Interaction Commands

### prompt
Send a user message to the agent.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "prompt", "params": {"message": "your prompt here"}, "id": 1}
```

### steer
Steer the current response with additional context (only works while agent is streaming).

**Usage:**
```json
{"jsonrpc": "2.0", "method": "steer", "params": {"message": "additional guidance"}, "id": 1}
```

### follow_up
Add a follow-up message to be processed after current turn completes.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "follow_up", "params": {"message": "follow up message"}, "id": 1}
```

### abort
Abort the current operation.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "abort", "params": {}, "id": 1}
```

## Session Management Commands

### new_session
Create a new session with optional name and title.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "new_session", "params": {"name": "session-name", "title": "Session Title"}, "id": 1}
```

### switch_session
Switch to an existing session by ID or path.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "switch_session", "params": {"id": "session-id-or-path"}, "id": 1}
```

### delete_session
Delete a session by ID.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "delete_session", "params": {"id": "session-id"}, "id": 1}
```

### list_sessions
List all available sessions.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "list_sessions", "params": {}, "id": 1}
```

### clear_session
Clear all messages from the current session.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "clear_session", "params": {}, "id": 1}
```

### set_session_name
Set the name of the current session.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_session_name", "params": {"name": "new name"}, "id": 1}
```

## State Query Commands

### get_state
Get current agent and session state.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "get_state", "params": {}, "id": 1}
```

**Returns:** Model info, streaming status, session info, message count, etc.

### get_messages
Get all messages in the current session.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "get_messages", "params": {}, "id": 1}
```

### get_session_stats
Get statistics about the current session.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "get_session_stats", "params": {}, "id": 1}
```

**Returns:** User/assistant/tool message counts, token usage, cost estimates.

### get_last_assistant_text
Get the text content of the last assistant message.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "get_last_assistant_text", "params": {}, "id": 1}
```

## Model Management Commands

### get_available_models
List all available models from configuration.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "get_available_models", "params": {}, "id": 1}
```

### set_model
Set the active model by provider and model ID.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_model", "params": {"provider": "zai", "modelId": "glm-4.5-air"}, "id": 1}
```

### cycle_model
Cycle to the next available model.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "cycle_model", "params": {}, "id": 1}
```

## Compression Commands

### compact
Manually trigger context compaction.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "compact", "params": {}, "id": 1}
```

**Returns:** Summary, tokens before/after, first kept entry ID.

### set_auto_compaction
Enable or disable automatic context compaction.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_auto_compaction", "params": {"enabled": true}, "id": 1}
```

### set_thinking_level
Set the reasoning depth instruction level.

**Levels:** `off`, `minimal`, `low`, `medium`, `high`, `xhigh`

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_thinking_level", "params": {"level": "high"}, "id": 1}
```

### cycle_thinking_level
Cycle to the next thinking level.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "cycle_thinking_level", "params": {}, "id": 1}
```

### set_tool_call_cutoff
Set the threshold for automatic tool output summarization.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_tool_call_cutoff", "params": {"cutoff": 10}, "id": 1}
```

### set_tool_summary_strategy
Set the strategy for tool output summarization.

**Strategies:** `llm`, `heuristic`, `off`

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_tool_summary_strategy", "params": {"strategy": "llm"}, "id": 1}
```

## Branching and Forking Commands

### get_fork_messages
Get user messages available for forking.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "get_fork_messages", "params": {}, "id": 1}
```

### fork
Create a new session forked from a specific entry.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "fork", "params": {"entryId": "entry-id"}, "id": 1}
```

### get_tree
Get the conversation tree structure.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "get_tree", "params": {}, "id": 1}
```

### resume_on_branch
Resume conversation from a specific branch point.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "resume_on_branch", "params": {"entryId": "entry-id"}, "id": 1}
```

**Special:** Use `entryId: "root"` to resume from the beginning.

## Configuration Commands

### set_steering_mode
Set how steering commands are handled.

**Modes:** `all`, `one-at-a-time`

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_steering_mode", "params": {"mode": "one-at-a-time"}, "id": 1}
```

### set_follow_up_mode
Set how follow-up messages are handled.

**Modes:** `all`, `one-at-a-time`

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_follow_up_mode", "params": {"mode": "one-at-a-time"}, "id": 1}
```

### set_trace_events
Configure which trace events are enabled.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_trace_events", "params": {"events": ["all", "off", "enable", "disable"]}, "id": 1}
```

### get_trace_events
Get currently enabled trace events.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "get_trace_events", "params": {}, "id": 1}
```

### get_commands
Get available slash commands (skills).

**Usage:**
```json
{"jsonrpc": "2.0", "method": "get_commands", "params": {}, "id": 1}
```

## Utility Commands

### bash
Execute a bash command (separate from the `bash` tool).

**Usage:**
```json
{"jsonrpc": "2.0", "method": "bash", "params": {"command": "ls -la"}, "id": 1}
```

### abort_bash
Abort a running bash command.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "abort_bash", "params": {}, "id": 1}
```

### set_auto_retry
Enable or disable automatic LLM retry on transient errors.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "set_auto_retry", "params": {"enabled": true}, "id": 1}
```

### abort_retry
Abort the current retry attempt.

**Usage:**
```json
{"jsonrpc": "2.0", "method": "abort_retry", "params": {}, "id": 1}
```

### export_html
Export conversation as HTML (currently not supported).

**Usage:**
```json
{"jsonrpc": "2.0", "method": "export_html", "params": {"outputPath": "/path/to/output.html"}, "id": 1}
```

## Event Types

The agent emits the following events:

| Event Type | Description |
|------------|-------------|
| `server_start` | Server initialized |
| `agent_start` | New conversation started |
| `agent_end` | Conversation ended |
| `turn_start` | New turn started |
| `turn_end` | Turn ended |
| `message_start` | New message started |
| `message_end` | Message completed |
| `message_update` | Message content updated |
| `text_delta` | Text content increment |
| `toolcall_delta` | Tool call content increment |
| `tool_execution_start` | Tool execution started |
| `tool_execution_end` | Tool execution completed |
| `compaction_start` | Context compression started |
| `compaction_end` | Context compression completed |
| `error` | Error occurred |
