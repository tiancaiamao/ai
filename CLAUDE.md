# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`ai` is a Go-based RPC-first Agent Core designed for editor integration via stdin/stdout communication. It implements a sophisticated AI agent system supporting multiple interaction modes (RPC, Win, JSON) with streaming capabilities, tool execution, and session management.

**Language:** Go 1.24.0
**Primary API:** ZAI API (OpenAI-compatible)
**Integration Point:** stdin/stdout JSON-RPC protocol

## Build and Development Commands

```bash
# Build the binary
go build -o bin/ai ./cmd/ai

# Run all tests
go test ./... -v

# Run tests for specific package
go test ./pkg/agent -v

# Run a single test
go test ./pkg/rpc -run TestServer -v

# Run with coverage
go test ./... -cover

# Build and run RPC mode
go build -o bin/ai ./cmd/ai && ./bin/ai --mode rpc

# Build and run Win mode (default)
go build -o bin/ai ./cmd/ai && ./bin/ai
```

### Test Scripts

The project includes several test scripts that source `scripts/test-common.sh`:
- `test-rpc.sh` - RPC mode testing
- `test-tools.sh` - Tool execution testing
- `test-connection.sh` - API connectivity testing
- `test-live.sh` - Live integration testing

## Architecture Overview

The codebase follows a clean separation of concerns with clear package boundaries:

### Core Components

1. **`pkg/agent/`** - Agent system
   - `agent.go` - Main agent orchestration
   - `loop.go` - Conversation loop with retry logic and context cancellation
   - `context.go` - Message and tool context management
   - `event.go` - Event definitions (AgentStart, TurnStart, MessageStart, TextDelta, ToolCallDelta, etc.)
   - `executor.go` - Concurrent tool execution with thread-safe pool
   - `metrics.go` - Performance tracking

2. **`pkg/rpc/`** - JSON-RPC server
   - `server.go` - RPC server implementation (stdin/stdout)
   - `types.go` - Shared type definitions used across the project
   - 25+ RPC commands including: prompt, steer, follow_up, abort, session management, model control

3. **`pkg/llm/`** - LLM client
   - OpenAI-compatible API client with streaming support
   - Event types: LLMStart, TextDelta, ToolCallDelta, LLMDone, LLMError

4. **`cmd/ai/`** - Application entry points
   - `main.go` - 30-line main dispatcher
   - `rpc_handlers.go` - RPC command handlers (920 lines)
   - `win_handlers.go` - Win mode handlers
   - `helpers.go` - Utility functions
   - `session_writer.go` - Session persistence

5. **`internal/winai/`** - Win-specific implementation
   - `interpreter.go` - Win interpreter (2,707 lines) - REPL for ad editor integration

6. **Supporting packages:**
   - `pkg/tools/` - Built-in tools (read, write, edit, bash, grep, subagent)
   - `pkg/skill/` - Skills system (Markdown-based, extensible)
   - `pkg/config/` - Configuration management
   - `pkg/compact/` - Context compression
   - `pkg/session/` - Session management

### Built-in Tools

The agent has access to the following tools:

| Tool | Description |
|------|-------------|
| `read` | Read file contents |
| `write` | Write/create files |
| `edit` | Make precise edits to files |
| `bash` | Execute shell commands |
| `grep` | Search file contents |
| `subagent` | Spawn isolated subagents for delegated tasks |

### Subagent Tool

The `subagent` tool allows spawning child agents with isolated context:

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

**Parallel execution:**
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

Key features:
- Isolated message context (no parent history)
- Configurable tool whitelist
- Turn and timeout limits
- Parallel task execution
- No nesting (subagent cannot spawn subagent)

## Interaction Modes

```bash
# RPC Mode (JSON-RPC over stdin/stdout)
./bin/ai --mode rpc

# Win Mode (REPL for ad editor)
./bin/ai  # or ./bin/ai --mode win

# JSON Mode (streaming JSON Lines output)
./bin/ai --mode json "prompt"

# Headless Mode (single JSON output - ideal for subagent calls)
./bin/ai --mode headless "prompt"
```

### Headless Mode

Headless mode outputs only the final result as a single JSON line, making it ideal for programmatic use and subagent calls:

```bash
./bin/ai --mode headless "Read README.md and summarize it"
```

Output format:
```json
{
  "text": "The README describes...",
  "usage": {"input_tokens": 150, "output_tokens": 50, "total_tokens": 200},
  "exit_code": 0
}
```

Error case:
```json
{
  "text": "",
  "usage": {"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
  "error": "timeout exceeded",
  "exit_code": 1
}
```

## Configuration

### Environment Variables (priority: highest)
- `ZAI_API_KEY` - API key (required)
- `ZAI_BASE_URL` - API endpoint (default: `https://api.z.ai/api/coding/paas/v4`)
- `ZAI_MODEL` - Model identifier (default: `glm-4.5-air`)
- `AI_LOG_STREAM_EVENTS` - Enable verbose event logging

### Config File (~/.ai/config.json)
```json
{
  "model": {
    "id": "glm-4.5-air",
    "provider": "zai",
    "baseUrl": "https://api.z.ai/api/coding/paas/v4",
    "api": "openai-completions"
  },
  "compactor": {
    "maxMessages": 50,
    "maxTokens": 8000,
    "autoCompact": true
  },
  "concurrency": {
    "maxConcurrentTools": 3,
    "toolTimeout": 30,
    "queueTimeout": 60
  },
  "toolOutput": {
    "maxLines": 2000,
    "maxBytes": 51200
  },
  "log": {
    "level": "info",
    "file": "~/.ai/ai.log",
    "prefix": "[ai] "
  }
}
```

### Auth File (~/.ai/auth.json)
```json
{
  "zai": {
    "type": "api_key",
    "key": "your-zai-api-key"
  }
}
```

## Data Storage

- **Sessions:** `~/.ai/sessions/--<cwd>--/*.jsonl` (isolated by working directory)
- **Global skills:** `~/.ai/skills/`
- **Project skills:** `.ai/skills/`
- **Logs:** `~/.ai/ai-{pid}.log`

## Key Architecture Patterns

### 1. Streaming Event System
The agent uses an event-driven architecture where all operations emit events:
- AgentStart, AgentEnd
- TurnStart, TurnEnd
- MessageStart, MessageUpdate, MessageEnd
- TextDelta, ToolCallDelta
- ToolExecutionStart, ToolExecutionEnd

Events are streamed in real-time via `llm.EventStream` with backpressure handling.

### 2. Context Cancellation
Context cancellation is propagated throughout:
- RPC server uses `context.WithCancel`
- Agent loop checks `ctx.Done()` on each iteration
- Tool execution respects context cancellation
- Use `context.Background()` for new operations, not the agent context

### 3. Concurrent Tool Execution
- Thread-safe executor pool with configurable concurrency (default: 3)
- Timeout and queue management
- Tool output truncation to prevent token bloat

### 4. Agent Loop
Located in `pkg/agent/loop.go`, the core loop:
1. Receives user prompts
2. Calls LLM with current context
3. Streams events (text deltas, tool calls)
4. Executes tools concurrently
5. Handles tool outputs and retry logic
6. Compresses context when needed
7. Continues until no more tool calls

### 5. Type Sharing
**Important:** `pkg/rpc/types.go` contains shared type definitions used across multiple packages. When adding new RPC-related types, define them here rather than duplicating in other files (see PLAN.md Phase 1 for context).

## RPC Commands Reference

Basic interaction:
- `prompt` - Send a user message
- `steer` - Steer the current response
- `follow_up` - Add a follow-up message
- `abort` - Abort current operation

Sessions:
- `new_session` - Create new session
- `switch_session` - Switch to existing session
- `delete_session` - Delete a session
- `list_sessions` - List all sessions
- `clear_session` - Clear current session

State:
- `get_state` - Get session state
- `get_messages` - Get conversation messages
- `get_session_stats` - Get session statistics
- `get_last_assistant_text` - Get last assistant response

Models:
- `get_available_models` - List available models
- `set_model` - Set current model
- `cycle_model` - Cycle through models

Compression:
- `compact` - Manual context compaction
- `set_auto_compaction` - Enable/disable auto-compaction
- `set_thinking_level` - Set thinking level
- `cycle_thinking_level` - Cycle through thinking levels

Forking:
- `get_fork_messages` - Get fork messages
- `fork` - Create a fork
- `get_tree` - Get conversation tree
- `resume_on_branch` - Resume on a branch

## Skills System

Skills are Markdown files with YAML frontmatter, loaded from:
- `~/.ai/skills/` (global)
- `.ai/skills/` (project-specific)

Skills are automatically added to system prompt and can be invoked by name. See `docs/skills-example.md` for details.

## Important Notes

1. **No Makefile** - Build directly with `go build`
2. **Testing** - Some tests may be flaky; see PLAN.md Phase 5 for known issues
3. **Logging** - Uses `log/slog` for structured logging
4. **Session isolation** - Sessions are isolated by working directory
5. **Tool execution** - Tools run concurrently with timeout and cancellation support
