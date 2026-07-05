# pkg/session

Append-only JSONL session persistence with compaction, forking, and lazy loading.

## Overview

Sessions store the full conversation history for an agent instance. The storage format is append-only JSONL — new entries are appended, never modified. This design enables crash-safe writes, lazy loading for large sessions, and branching (forking) from any point in the conversation.

## Session Layout

Sessions are stored under `~/.ai/sessions/` (or a configured base directory):

```
~/.ai/sessions/
├── --Users-genius-project-myapp--/   # One directory per project (sanitized path)
│   ├── <uuid-1>/                     # Session directory
│   │   ├── messages.jsonl            # Append-only entry log
│   │   ├── meta.json                 # Session metadata (name, title, timestamps)
│   │   ├── agent_state.json          # Persisted AgentState (turn, CWD, etc.)
│   │   └── compactions/              # Compaction snapshot files
│   ├── <uuid-2>/
│   └── ...
└── --Users-genius-project-other--/
    └── ...
```

The session directory name is derived from the working directory at session creation:

```
sanitizePath("/Users/genius/project/myapp") → "--Users-genius-project-myapp--"
```

## Entry Types

Each line in `messages.jsonl` is a JSON object with a `type` discriminator:

| Type | Constant | Description |
|------|----------|-------------|
| `session` | `EntryTypeSession` | Header entry — first line, contains session ID, version, CWD |
| `message` | `EntryTypeMessage` | User/assistant/tool message |
| `compaction` | `EntryTypeCompaction` | Compaction summary replacing older messages (has `snapshotRef` pointing to `compactions/compaction_NNNNN.jsonl`) |
| `branch_summary` | `EntryTypeBranchSummary` | Summary of a branched conversation |
| `session_info` | `EntryTypeSessionInfo` | Session name/title metadata |

### Session Header

```json
{"type":"session","version":1,"id":"<uuid>","timestamp":"2025-01-15T10:30:00Z","cwd":"/Users/genius/project/myapp","gitCommit":"<sha>","gitVersion":"<tag>","lastCompactionId":"<entry-id>"}
```

### Message Entry

```json
{"type":"message","id":"<entry-id>","parentId":"<parent-entry-id>","timestamp":"...","message":{"role":"user","content":[...]}}
```

Messages form a tree via `parentId` links. The current conversation tip is the `leafID`.

### Compaction Entry

```json
{"type":"compaction","id":"<entry-id>","parentId":"<parent-entry-id>","timestamp":"...","summary":"...","firstKeptEntryId":"<id>","tokensBefore":50000,"snapshotRef":"compactions/compaction_00001.jsonl"}
```

The `firstKeptEntryId` marks where messages resume after the summary.
The `snapshotRef` points to a file in `compactions/` containing the full post-compaction message list (Proposal B: append-only design).

## Core Types

### Session

```go
type Session struct { ... }
```

Main session struct. Manages an in-memory tree of entries backed by `messages.jsonl`. Thread-safe via mutex. Key methods:

- `AppendMessage(msg)` — Append a user/assistant/tool message
- `GetMessages()` — Get current conversation branch as `[]AgentMessage`
- `AppendCompaction(summary, messages)` — Append a compaction entry with summary
- `GetUserMessagesForForking()` — List user messages suitable as fork points
- `GetBranch(id)` — Get entries from root to a specific leaf

### SessionManager

```go
type SessionManager struct { ... }
```

Manages multiple sessions within a sessions directory. Handles:

- `ListSessions()` — Enumerate all sessions (sorted by update time)
- `CreateSession(name, title)` — Create a new session
- `ForkSessionFrom(source, leafID, name, title)` — Branch a conversation from any point
- `DeleteSession(id)` — Remove a session
- `UpdateSessionName(id, name, title)` — Rename/update a session

### Session Meta

```go
type SessionMeta struct {
	Role string `json:"role,omitempty"`
}
```

Persistent metadata attached to each session:

- `GetMeta(id)` — Read metadata for a session
- `SetSessionRole(id, role)` — Record the agent role used in a session

The `Role` field is written on first use (via `SetSessionRole`) and recovered on resume
to restore the previous role without requiring `--role` on re-attach.

## Forking

A fork creates a new session that copies entries from the source session up to the specified `leafID`. The new session gets:

- A copy of all branch entries from root to the fork point
- A `parentSession` reference to the source session
- Its own `session_info` entry with the new name/title

This enables exploring alternate conversation paths without modifying the original.

## Lazy Loading

Sessions support lazy loading to avoid reading the entire JSONL file for large conversations:

```go
opts := session.DefaultLoadOptions() // Lazy: true, auto message limit
sess, err := session.LoadSessionLazy(dir, opts)
```

Lazy loading reads only:
1. The session header
2. The most recent compaction entry (if any)
3. Recent entries (working backwards from the end)

The `MaxMessages` field controls how many recent entries to load:
- `0` — Auto (based on compaction entry's `firstKeptEntryId`)
- `-1` — Load everything
- `N > 0` — Load at most N messages

## AgentState Persistence

The `AgentContextCheckpointManager` in `pkg/agent` persists `AgentState` (turn count, CWD, token usage, compaction counters) to `agent_state.json` in the session directory. This file is written after compaction events and loaded on session resume via `LoadResumeState()`.

## Compaction in Sessions

When context grows too large, the session system works with `pkg/compact` to:

1. Find the oldest cuttable messages (user messages are cuttable boundaries)
2. Summarize the cuttable portion via LLM
3. Append a `compaction` entry with the summary
4. Set `firstKeptEntryId` to the first message after the cut point

On replay, compaction entries are converted to a user message containing the summary wrapped in `<summary>` tags. All entries before `firstKeptEntryId` are skipped. The full post-compaction message list is loaded from the `snapshotRef` file.

## Key Files

| File | Description |
|------|-------------|
| `session.go` | Session struct, append/get/compact/fork operations |
| `entries.go` | Entry types, header parsing, entry-to-message conversion, replay |
| `manager.go` | SessionManager — CRUD, listing, forking across sessions |
| `lazy.go` | Lazy loading with LoadSessionLazy |