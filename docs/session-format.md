# Session Format

Sessions persist the full conversation history for an agent instance as append-only JSONL files. This document describes the file layout, entry types, loading strategies, and crash recovery mechanisms.

## Design Principles

1. **Append-only**: New entries are appended to `messages.jsonl`. Existing entries are never modified in place.
2. **Tree structure**: Entries are linked via `parentId`, forming a conversation tree. The current branch tip is the `leafID`.
3. **Crash-safe**: Append-only writes minimize data loss risk. Periodic checkpoints enable fast recovery.
4. **Lazy-loadable**: Large sessions can be restored from checkpoint + journal, or loaded with only the recent messages needed.

## File Layout

```
~/.ai/sessions/
└── --<sanitized-path>--/
    ├── <session-uuid-1>/
    │   ├── messages.jsonl      # Append-only entry log
    │   ├── meta.json           # Session metadata
    │   ├── llm_context.txt     # Persisted LLM context (from context management)
    │   └── checkpoint.json     # Periodic context snapshot
    ├── <session-uuid-2>/
    │   ├── messages.jsonl
    │   └── meta.json
    └── ...
```

The top-level directory name is derived from the working directory at session creation:

```
sanitizePath("/Users/genius/project/myapp")  →  "--Users-genius-project-myapp--"
sanitizePath("C:\Users\genius\project")       →  "--Users-genius-project--"
```

Path separators, backslashes, and colons are replaced with `-`.

## Entry Types

Each line in `messages.jsonl` is a JSON object. All entries share common fields:

```json
{
  "type": "<entry-type>",
  "id": "<unique-entry-id>",
  "parentId": "<parent-entry-id | null>",
  "timestamp": "<RFC3339Nano>"
}
```

### session (Header)

First line of every `messages.jsonl`. Exactly one per session.

```json
{
  "type": "session",
  "version": 1,
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "timestamp": "2025-01-15T10:30:00.123456789Z",
  "cwd": "/Users/genius/project/myapp"
}
```

| Field | Description |
|-------|-------------|
| `version` | Format version (currently `1`) |
| `id` | Session UUID |
| `cwd` | Working directory at session creation |

### message

A single user, assistant, or tool message.

```json
{
  "type": "message",
  "id": "msg-001",
  "parentId": "msg-000",
  "timestamp": "2025-01-15T10:30:01.000Z",
  "message": {
    "role": "user",
    "content": [
      {"type": "text", "text": "Fix the bug in auth.go"}
    ]
  }
}
```

Assistant messages with tool calls:

```json
{
  "type": "message",
  "id": "msg-002",
  "parentId": "msg-001",
  "timestamp": "2025-01-15T10:30:02.000Z",
  "message": {
    "role": "assistant",
    "content": [
      {"type": "text", "text": "I'll read the file first."}
    ],
    "toolCalls": [
      {
        "id": "call_abc123",
        "type": "function",
        "function": {
          "name": "read",
          "arguments": "{\"path\":\"auth.go\"}"
        }
      }
    ]
  }
}
```

Tool result messages:

```json
{
  "type": "message",
  "id": "msg-003",
  "parentId": "msg-002",
  "timestamp": "2025-01-15T10:30:03.000Z",
  "message": {
    "role": "tool",
    "toolCallId": "call_abc123",
    "content": [
      {"type": "text", "text": "package main\n\nfunc auth() { ... }"}
    ]
  }
}
```

### compaction

Replaces older messages with an LLM-generated summary.

```json
{
  "type": "compaction",
  "id": "comp-001",
  "parentId": "msg-003",
  "timestamp": "2025-01-15T10:35:00.000Z",
  "summary": "The user asked to fix a bug in auth.go. The assistant read the file and identified an issue with token validation...",
  "firstKeptEntryId": "msg-010",
  "tokensBefore": 45000,
  "tokensAfter": 5000
}
```

| Field | Description |
|-------|-------------|
| `summary` | LLM-generated summary of all messages before `firstKeptEntryId` |
| `firstKeptEntryId` | First message ID after the compaction cut point |
| `tokensBefore` | Estimated tokens before compaction |
| `tokensAfter` | Estimated tokens after compaction |

On replay, the compaction entry is converted to a synthetic user message:

```
The conversation history before this point was compacted into the following summary:

<summary>
...summary text...
</summary>
```

All entries before `firstKeptEntryId` are skipped during replay.

### compact_event

Records individual context management actions for fine-grained replay.

```json
{
  "type": "compact_event",
  "id": "ce-001",
  "parentId": "msg-005",
  "timestamp": "2025-01-15T10:32:00.000Z",
  "actions": [
    {"action": "truncate", "ids": ["call_abc123", "call_def456"]},
    {"action": "update_llm_context", "ids": []}
  ]
}
```

| Action | Description | Replay Behavior |
|--------|-------------|-----------------|
| `truncate` | Trim tool output to head+tail | Apply `TruncateWithHeadTail` to matching tool results |
| `update_llm_context` | Update structured LLM context | Context loaded from `llm_context.txt` |
| `compact` | Major compaction triggered | Replayed via parent `compaction` entry |

### branch_summary

Summary of a forked branch when the conversation returns from it.

```json
{
  "type": "branch_summary",
  "id": "bs-001",
  "parentId": "msg-015",
  "timestamp": "2025-01-15T11:00:00.000Z",
  "summary": "In a branched conversation, the assistant explored an alternative approach..."
}
```

Replayed as a synthetic user message:

```
The following is a summary of a branch that this conversation came back from:

<summary>
...summary text...
</summary>
```

### session_info

Session metadata (name, title).

```json
{
  "type": "session_info",
  "id": "si-001",
  "parentId": "msg-000",
  "timestamp": "2025-01-15T10:30:00.000Z",
  "name": "my-session",
  "title": "Fix auth bug"
}
```

## Conversation Tree

Entries form a tree via `parentId` links:

```
session ─→ msg-001(user) ─→ msg-002(assistant) ─→ msg-003(tool)
                │
                └─→ msg-004(assistant) ─→ msg-005(tool) ─→ ...
```

The `leafID` points to the current conversation tip. Forking creates a new branch by setting a different `leafID` and appending new entries from that point.

## Forking

A fork creates a new session directory that copies entries from the source session up to the specified fork point:

1. New session directory created with a fresh UUID
2. All entries from root to the fork `leafID` are copied
3. The `session` header records `parentSession` pointing to the source
4. A new `session_info` entry is appended with the fork's name/title
5. New messages are appended to the fork's own `messages.jsonl`

The original session is never modified.

## Lazy Loading

For large sessions, lazy loading avoids reading the entire JSONL file:

```go
opts := session.LoadOptions{
    MaxMessages:    0,    // 0=auto, -1=all, N>0=limit
    IncludeSummary: true, // Include compaction summary
    Lazy:           true, // Enable lazy loading
}
sess, err := session.LoadSessionLazy(dir, opts)
```

### Loading Strategy

1. Read the session header (first line)
2. Scan backwards from the end of the file
3. Find the most recent `compaction` entry (if any)
4. Load entries from `firstKeptEntryId` to the end
5. Fix broken parent links: if an entry's parent is not in the loaded set, link it to the compaction entry (or set to `nil`)

### MaxMessages Behavior

| Value | Behavior |
|-------|----------|
| `0` | Auto — load from `firstKeptEntryId` to end |
| `-1` | Load all entries |
| `N > 0` | Load at most N recent messages |

## Checkpoint and Journal

### Journal

The `AgentContextCheckpointManager` maintains an append-only journal in the session directory:

- Each message append is written to the journal
- Turn boundaries trigger periodic checkpoints
- Journal entries are compacted into checkpoints periodically

### Checkpoint

A checkpoint is a full snapshot of `AgentContext`:

```json
{
  "turn": 5,
  "messageIndex": 23,
  "context": { ... }
}
```

### Recovery

On crash or restart:

1. Load the last checkpoint
2. Replay journal entries after the checkpoint
3. Continue from the recovered state

## Compaction Flow

When context exceeds the configured threshold:

1. **Trigger**: `ShouldCompact()` returns `true` based on estimated tokens
2. **Cut point**: Find the oldest cuttable message (user messages are cuttable boundaries)
3. **Summarize**: LLM generates a summary of all messages before the cut point
4. **Record**: Append a `compaction` entry with the summary and `firstKeptEntryId`
5. **Update**: Remove old messages from `AgentContext.RecentMessages`, replace with summary message

The session JSONL retains all original entries. Compaction only affects the in-memory state — the append-only log is preserved for replay from any point.

## meta.json

Session metadata stored alongside the JSONL:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "my-session",
  "title": "Fix auth bug",
  "createdAt": "2025-01-15T10:30:00Z",
  "updatedAt": "2025-01-15T11:00:00Z",
  "messageCount": 42,
  "workspace": "/Users/genius/project/myapp",
  "currentWorkdir": "/Users/genius/project/myapp"
}
```

Managed by `SessionManager`. Updated on each session save.

## Legacy Format

> **Warning:** Legacy session formats (single `.jsonl` files without directory layout, or old entry schemas) still exist in the codebase. The loader auto-detects and handles them, but this code should be cleaned up. Once legacy format support is removed, update this section accordingly.