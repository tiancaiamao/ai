# Session Format

Sessions persist the full conversation history for an agent instance as append-only JSONL files. This document describes the file layout, entry types, loading strategies, and crash recovery mechanisms.

## Design Principles

1. **Append-only**: New entries are appended to `messages.jsonl`. Existing entries are never modified in place.
2. **Tree structure**: Entries are linked via `parentId`, forming a conversation tree. The current branch tip is the `leafID`.
3. **Crash-safe**: Append-only writes minimize data loss risk. AgentState is persisted after compaction for fast recovery.
4. **Lazy-loadable**: Large sessions can be restored from compaction snapshots, or loaded with only the recent messages needed.

## File Layout

```
~/.ai/sessions/
└── --<sanitized-path>--/
    ├── <session-uuid-1>/
    │   ├── messages.jsonl               # Append-only entry log
        │   ├── compactions/                 # Compaction snapshot files
    │   │   ├── compaction_00001.jsonl   # Post-compaction messages
    │   │   └── compaction_00002.jsonl
    │   ├── agent_state.json             # Persisted AgentState (turn, CWD, etc.)
    │   └── (meta.json managed externally by SessionManager)
    ├── <session-uuid-2>/
    │   ├── messages.jsonl
    │   └── ...
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

**Entry type constants** (defined in `pkg/session/entries.go`):

| Type | Constant | Description |
|------|----------|-------------|
| `session` | `EntryTypeSession` | Session header (first line) |
| `message` | `EntryTypeMessage` | User/assistant/tool message |
| `compaction` | `EntryTypeCompaction` | Compaction event |
| `branch_summary` | `EntryTypeBranchSummary` | Summary of a forked branch |
| `session_info` | `EntryTypeSessionInfo` | Session metadata (name, title) |

### session (Header)

First line of every `messages.jsonl`. Exactly one per session.

```json
{
  "type": "session",
  "version": 1,
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "timestamp": "2025-01-15T10:30:00.123456789Z",
  "cwd": "/Users/genius/project/myapp",
  "gitCommit": "3ac71c28aaf41755fbe046570152096e6469f9ff",
  "gitVersion": ""
}
```

| Field | Description |
|-------|-------------|
| `version` | Format version (currently `1`, defined by `CurrentSessionVersion`) |
| `id` | Session UUID |
| `cwd` | Working directory at session creation |
| `parentSession` | Parent session UUID (set when forked; omitted otherwise) |
| `lastCompactionId` | Most recent compaction entry ID (resume optimization; omitted if no compaction) |
| `gitCommit` | Git commit hash of the `ai` binary that created this session |
| `gitVersion` | Git version/tag of the `ai` binary |

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

Records a compaction event. The post-compaction messages are saved to an external snapshot file (Proposal B approach), keeping `messages.jsonl` append-only.

```json
{
  "type": "compaction",
  "id": "comp-001",
  "parentId": "msg-003",
  "timestamp": "2025-01-15T10:35:00.000Z",
  "snapshotRef": "compactions/compaction_00001.jsonl",
  "summary": "The user asked to fix a bug in auth.go. The assistant read the file and identified an issue with token validation..."
}
```

| Field | Description |
|-------|-------------|
| `snapshotRef` | Relative path (within session dir) to the post-compaction snapshot file. Contains the full `AgentMessage` array after compaction. |
| `summary` | LLM-generated summary of compacted messages |
| `firstKeptEntryId` | *(Legacy)* First message ID after the compaction cut point. Used by old sessions without `snapshotRef`. |
| `tokensBefore` | *(Optional)* Estimated tokens before compaction |

**On replay**, the loader follows `snapshotRef` to load post-compaction messages directly from the snapshot file. If `snapshotRef` is empty (legacy sessions), it falls back to the `summary` text + `firstKeptEntryId` reconstruction path.

The summary is also converted to a synthetic user message at the start of the replayed message set:

```
The conversation history before this point was compacted into the following summary:

<summary>
...summary text...
</summary>
```

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

## SessionEntry Struct

**File:** `pkg/session/entries.go`

```go
type SessionEntry struct {
    Type      string                `json:"type"`
    ID        string                `json:"id"`
    ParentID  *string               `json:"parentId"`
    Timestamp string                `json:"timestamp"`

    Message          *AgentMessage  `json:"message,omitempty"`
    SnapshotRef      string         `json:"snapshotRef,omitempty"`
    Summary          string         `json:"summary,omitempty"`
    FirstKeptEntryID string         `json:"firstKeptEntryId,omitempty"`
    TokensBefore     int            `json:"tokensBefore,omitempty"`

    FromID string `json:"fromId,omitempty"`
    Name   string `json:"name,omitempty"`
    Title  string `json:"title,omitempty"`
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

## Loading

**File:** `pkg/session/entries.go` — `buildSessionContext()`

Session loading reconstructs the conversation from the entry tree:

1. Read all entries from `messages.jsonl`
2. Walk the tree from `leafID` back to root via `parentId` links
3. Find the most recent `compaction` entry on the path (if any)
4. If compaction exists with `snapshotRef`: load post-compaction messages from the snapshot file
5. If compaction exists with `firstKeptEntryID` (legacy): reconstruct using summary + kept entries
6. Append all entries after the compaction point

### Lazy Loading

For large sessions, lazy loading avoids reading the entire JSONL file:

```go
opts := session.LoadOptions{
    MaxMessages:    0,    // 0=auto, -1=all, N>0=limit
    IncludeSummary: true, // Include compaction summary
    Lazy:           true, // Enable lazy loading
}
sess, err := session.LoadSessionLazy(dir, opts)
```

The loader scans backwards from the end of the file to find the most recent compaction entry, then loads only from that point forward.

## AgentState Persistence

**Files:** `pkg/context/checkpoint_io.go`, `pkg/agent/checkpoint_manager.go`

`AgentState` (turn count, CWD, token usage, compaction counters) is persisted to `agent_state.json` in the session directory. This file is written after compaction events and loaded on session resume.

Messages are NOT stored in `agent_state.json` — they come from `sess.GetMessages()` which handles compaction snapshot refs internally.

### Recovery

On crash or restart:

1. Load messages from `sess.GetMessages()` (handles compaction snapshots)
2. Load `agent_state.json` for AgentState (CWD, turn count, etc.)
3. Continue from the recovered state

## Compaction Persistence Flow

When `Compact()` succeeds:

1. **Snapshot save** (`session.AppendCompaction`): Post-compaction messages are written to `compactions/compaction_NNNNN.jsonl`
2. **Entry append**: A `compaction` entry with `snapshotRef` is appended to `messages.jsonl`
3. **Header update**: `lastCompactionId` in the session header is updated

The session JSONL retains all original entries. Compaction only adds new entries — history is never rewritten.

## meta.json

Session metadata stored alongside the JSONL, managed by `SessionManager`:

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

## Key File Index

| File | Responsibility |
|------|---------------|
| `pkg/session/session.go` | `Session` struct, `AppendMessage`, `AppendCompaction`, loading |
| `pkg/session/entries.go` | `SessionEntry`, `SessionHeader`, entry type constants, `buildSessionContext` |
| `pkg/session/lazy.go` | Lazy session loading |
| `pkg/context/checkpoint_io.go` | `SaveAgentState` / `LoadAgentState`, `SplitLines` |
| `pkg/agent/checkpoint_manager.go` | AgentState persistence lifecycle |
| `pkg/agent/resume.go` | `LoadResumeState()` — session resume |