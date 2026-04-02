# Session Operations: resume, fork, rewind

## Core Principle

**Each session maintains linear history. Branching is implemented by creating new sessions.**

```
session_abc/ (original session)
├── messages.jsonl        (append-only, linear)
├── checkpoints/          (linear sequence)
│   ├── checkpoint_00010/
│   ├── checkpoint_00020/
│   └── checkpoint_00030/
└── current/ → checkpoint_00030/

session_xyz/ (created via fork or rewind)
├── messages.jsonl        (new linear history starting from fork point)
├── checkpoints/
│   ├── checkpoint_00020/  (copied from session_abc)
│   ├── checkpoint_00025/
│   └── checkpoint_00035/
└── current/ → checkpoint_00035/
```

## Session Registry

To support session discovery and navigation, a global session registry is maintained:

**Location**: `~/.ai/sessions/registry.json`

```json
{
  "sessions": [
    {
      "id": "session_abc",
      "created_at": "2024-03-31T10:00:00Z",
      "updated_at": "2024-03-31T10:30:00Z",
      "parent_session": null,
      "forked_from_session": null,
      "forked_from_checkpoint": null,
      "forked_from_turn": null,
      "branch_name": "main",
      "working_dir": "/path/to/project",
      "status": "active"
    },
    {
      "id": "session_xyz",
      "created_at": "2024-03-31T11:00:00Z",
      "updated_at": "2024-03-31T11:15:00Z",
      "parent_session": "session_abc",
      "forked_from_session": "session_abc",
      "forked_from_checkpoint": "checkpoint_00020",
      "forked_from_turn": 20,
      "branch_name": "experiment",
      "working_dir": "/path/to/project",
      "status": "active"
    }
  ]
}
```

## Operations

### /resume

Resume current session from latest state.

**Implementation**:
1. Load `current/` symlink → points to latest checkpoint
2. Load `llm_context.txt` → LLMContext
3. Load `agent_state.json` → AgentState
4. Read `messages.jsonl` from `checkpoint.message_index` to end → RecentMessages
5. Reconstruct ContextSnapshot

**Usage**:
```
/resume                              # Resume latest
/resume --turn 30                    # Resume from checkpoint at turn 30
```

### /fork

Create a new session starting from current state or a historical checkpoint.

**Implementation**:
1. Determine source checkpoint (current or specified turn)
2. Create new session directory: `session_{generated_id}/`
3. Copy source checkpoint data:
   - `llm_context.txt` (copy)
   - `agent_state.json` (copy with reset metadata)
   - Create initial `checkpoint_XXXXX/` directory
4. Create new `messages.jsonl` (starts with copied checkpoint messages)
5. Update `checkpoint_index.json`
6. Update `current/` symlink
7. Register in `registry.json`
8. Switch to new session

**Usage**:
```
/fork --name experiment              # Fork from current state
/fork --turn 20 --name alt-route    # Fork from historical checkpoint
```

### /rewind (formerly /resume-on-branch)

Rewind to a historical checkpoint and continue from there, creating a new session.

**Key difference from /fork**:
- `/fork` creates a parallel branch from any point (past or future)
- `/rewind` specifically goes **back in time** to explore alternative paths

**Implementation**:
1. Validate target turn < current turn (can only rewind to past)
2. Find checkpoint at target turn in current session
3. Create new session (similar to /fork)
4. Copy checkpoint data
5. Register in `registry.json` with parent_session reference
6. Switch to new session

**Usage**:
```
/rewind --turn 20 --name alt-choice  # Go back to turn 20, explore alternative
```

## Flow: Going "Back to the Future"

To return from a rewound/branched session back to the original future:

1. **Find original session**:
   ```
   /list-sessions --parent session_abc
   ```
   Or check registry for `parent_session` field

2. **Switch to original session**:
   ```
   /switch-session session_abc
   /resume
   ```

**Note**: There is no direct "merge" operation. Each session maintains independent linear history.

## Directory Structure

```
~/.ai/sessions/
├── registry.json                           # Global session index
├── session_{id}/
│   ├── messages.jsonl                      # Append-only event log
│   ├── checkpoint_index.json               # Checkpoint manifest
│   ├── current/                            # Symlink to latest checkpoint
│   │   ├── llm_context.txt
│   │   └── agent_state.json
│   └── checkpoints/
│       ├── checkpoint_00010/
│       │   ├── llm_context.txt
│       │   └── agent_state.json
│       ├── checkpoint_00020/
│       └── checkpoint_00030/
└── session_{another_id}/
    └── ...
```

## Registry Operations

### Listing Sessions
```
/list-sessions                           # List all sessions
/list-sessions --working-dir .           # List sessions for current project
/list-sessions --parent session_abc      # List child sessions
```

### Switching Sessions
```
/switch-session session_xyz              # Switch active session
```

### Session Metadata
```json
{
  "id": "string (unique identifier)",
  "created_at": "ISO8601 timestamp",
  "updated_at": "ISO8601 timestamp",
  "parent_session": "session_id or null",
  "forked_from_session": "session_id or null",
  "forked_from_checkpoint": "checkpoint_name or null",
  "forked_from_turn": "int or null",
  "branch_name": "string (user-defined or auto-generated)",
  "working_dir": "absolute path",
  "status": "active|archived"
}
```

## Implementation Notes

1. **Session ID Generation**: Use UUID or timestamp-based unique identifier
2. **Checkpoint Copying**: Deep copy llm_context.txt and agent_state.json to ensure independence
3. **Messages Copying**: Copy messages.jsonl entries up to the fork point to new session
4. **Registry Updates**: Atomic file operations to prevent corruption
5. **Current Working Directory**: Multiple sessions can share the same working_dir

## Removed Concepts

- ❌ **DAG-based message history**: No more parent_id per message
- ❌ **In-session branching**: Each session is purely linear
- ❌ **Merge operations**: No way to merge branches back together
- ❌ **Complex navigation**: Simple session switch instead of branch traversal

## Summary Table

| Operation | Creates New Session? | Direction | Registry Update |
|-----------|---------------------|-----------|-----------------|
| resume | No | Forward (latest) | No |
| fork | Yes | Any (past/current) | Yes (new entry) |
| rewind | Yes | Backward (past only) | Yes (with parent) |
| switch-session | No | N/A | No |
