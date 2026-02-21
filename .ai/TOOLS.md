# Tools Reference

This file documents the built-in tools available to the agent.

## Built-in Tools

| Tool | Description | Usage Notes |
|------|-------------|-------------|
| `read` | Read file contents | Returns full file content |
| `write` | Write/create files | Overwrites existing files |
| `edit` | Make precise edits to files | Uses old_text/new_text replacement |
| `bash` | Execute shell commands | Runs in project directory, respects timeout |
| `grep` | Search file contents | Pattern-based search |
| `subagent` | Spawn isolated subagents | For delegated tasks |

## Subagent Tool

The `subagent` tool creates child agents with isolated context:

### Single Task
```json
{
  "tool": "subagent",
  "task": "Analyze src/auth for security issues",
  "config": {
    "tools": ["read", "grep"],
    "max_turns": 10,
    "timeout": 120
  }
}
```

### Parallel Tasks
```json
{
  "tool": "subagent",
  "tasks": [
    "Analyze authentication flow",
    "Check database queries",
    "Review access control"
  ]
}
```

### Subagent Features
- **Isolation:** No parent message history
- **Tool whitelist:** Configurable tool access
- **Limits:** Turn count and timeout constraints
- **Parallel:** Multiple tasks run concurrently
- **No nesting:** Subagents cannot spawn subagents

## Tool Execution

Tools run concurrently with the following defaults:
- **Max concurrent:** 3 (configurable via `concurrency.maxConcurrentTools`)
- **Timeout:** 30 seconds per tool (configurable via `concurrency.toolTimeout`)
- **Queue timeout:** 60 seconds (configurable via `concurrency.queueTimeout`)

## Tool Output Truncation

To prevent token bloat, tool output is truncated at:
- **Max lines:** 2000 (configurable via `toolOutput.maxLines`)
- **Max bytes:** 51200 (configurable via `toolOutput.maxBytes`)
