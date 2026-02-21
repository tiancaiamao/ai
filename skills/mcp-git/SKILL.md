---
name: mcp-git
description: Advanced Git operations using MCP git server. Provides structured access to Git repository data including history, diffs, blame information, and complex queries beyond basic git commands.
allowed-tools: [bash]
disable-model-invocation: false
---

# MCP Git Skill

This skill provides advanced Git operations using the Model Context Protocol (MCP) git server, offering structured access to repository data.

## What This Skill Does

When you need advanced Git operations:
1. Connects to the MCP git server via stdio
2. Performs complex Git queries and operations
3. Returns structured, parsed results instead of raw git output
4. Handles error cases and provides clear diagnostics

## When to Use This Skill

Use this skill when:
- You need complex Git history analysis
- You need to query commit metadata programmatically
- You need to perform blame annotations with structured output
- You need to diff commits or branches with detailed context
- You want to avoid parsing raw git output manually

**Basic Git operations** (clone, add, commit, push, pull) should still use the bash tool directly with git commands.

## How It Works

The MCP git server provides:
- **Repository reading**: Access to Git repository data
- **History queries**: Commit logs, author statistics, time-based queries
- **Diff operations**: Compare commits, branches, files
- **Blame information**: Annotate files with commit metadata
- **Search capabilities**: Find commits by content, author, or time

## Usage Examples

### Example 1: Get Commit History
```bash
mcp-git.sh log --max-count 10
```

### Example 2: Get Diff Between Commits
```bash
mcp-git.sh diff HEAD~5 HEAD
```

### Example 3: Blame a File
```bash
mcp-git.sh blame src/main.go
```

### Example 4: Search Commits by Author
```bash
mcp-git.sh log --author "John Doe" --since "2025-01-01"
```

### Example 5: Get Repository Status
```bash
mcp-git.sh status
```

## Available Operations

### Repository Information
- `status` - Get repository status (modified, staged, untracked files)
- `revparse` - Parse revision names and resolve to commit hashes

### History Operations
- `log` - Get commit history with metadata
  - `--max-count <n>` - Limit number of commits
  - `--author <name>` - Filter by author
  - `--since <date>` - Commits since date
  - `--until <date>` - Commits until date
  - `--grep <pattern>` - Search commit messages

### Diff Operations
- `diff` - Compare commits, branches, or files
  - Accepts commit ranges, trees, or file paths
  - Returns unified diff with context

### Blame Operations
- `blame` - Annotate each line of a file with commit information
  - Shows commit hash, author, date for each line
  - Useful for understanding code provenance

### Show Operations
- `show` - Display commit details
  - Full commit message
  - Changed files
  - Diff stats

## Command Syntax

```bash
mcp-git.sh <operation> [options] [arguments]

Operations:
  status                    Show repository status
  log [options]             Show commit history
  diff <revision> [path]    Show diff
  blame <file>              Annotate file with commit info
  show <revision>           Show commit details
  revparse <revision>       Resolve revision to hash

Log Options:
  --max-count <n>           Limit commits
  --author <name>           Filter by author
  --since <date>            Start date
  --until <date>            End date
  --grep <pattern>          Search messages
```

## Implementation Details

The skill communicates with the MCP git server using JSON-RPC 2.0 over stdio:
1. Spawns the git MCP server process (Python-based)
2. Sends initialization handshake
3. Sends tool call requests with operation and parameters
4. Receives structured JSON responses
5. Formats and returns results

## Error Handling

The script will:
- Check if we're in a Git repository
- Return clear error messages for invalid operations
- Handle missing or ambiguous references
- Report authentication/permission issues for remote operations

## Comparison with Bash Git Commands

### Use MCP Git When:
- You need structured, parsed output
- You're performing complex queries
- You want to avoid manual parsing of git output
- You need metadata (author, date) alongside changes

### Use Bash Git When:
- Performing simple operations (status, add, commit)
- Interactive operations (rebase, merge)
- Remote operations (push, pull, fetch)
- One-off commands where structured output isn't needed

## Benefits Over Direct Git Commands

- **Structured Output**: Returns JSON, not text that needs parsing
- **Error Handling**: Provides clear error messages and diagnostics
- **Advanced Queries**: Supports complex filtering and metadata queries
- **Consistency**: Standardized output format across operations

## Limitations

- Each invocation spawns a new Python process (performance overhead)
- Read-only operations (doesn't modify the repository)
- Requires Python and the git MCP server package
- No interactive operations (rebase, merge conflicts)
- No remote operations (push, pull, fetch)

## Prerequisites

The git MCP server requires:
- Python 3.8 or higher
- Git installed on the system
- Current directory must be within a Git repository

## Notes

- The git MCP server is read-only for safety
- All operations are relative to the current working directory
- Complex queries may take longer on large repositories
- Consider using native git commands for simple operations to avoid overhead

## Example Use Cases

1. **Code Review**: Get detailed blame information for a file
2. **Release Notes**: Generate changelog from commit history
3. **Debugging**: Find when a specific line was introduced
4. **Analytics**: Get commit statistics by author or time period
5. **Comparison**: Diff branches or commits with structured output
