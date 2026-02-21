# Architecture Overview

This file provides detailed architecture information for the `ai` codebase.

## Package Structure

```
ai/
├── cmd/ai/                 # Application entry points
│   ├── main.go             # Main dispatcher (30 lines)
│   ├── rpc_handlers.go     # RPC command handlers (920 lines)
│   ├── win_handlers.go     # Win mode handlers
│   ├── helpers.go          # Utility functions
│   └── session_writer.go   # Async session persistence
│
├── pkg/agent/              # Agent system
│   ├── agent.go            # Main agent orchestration
│   ├── loop.go             # Conversation loop with retry logic
│   ├── context.go          # Message and tool context management
│   ├── event.go            # Event definitions
│   ├── executor.go         # Concurrent tool execution pool
│   ├── metrics.go          # Performance tracking
│   ├── conversion.go       # Message format conversion
│   └── tool_summary.go     # Tool output summarization
│
├── pkg/rpc/                # JSON-RPC server
│   ├── server.go           # RPC server (stdin/stdout)
│   └── types.go            # Shared type definitions
│
├── pkg/llm/                # LLM client
│   └── OpenAI-compatible API client with streaming
│
├── pkg/tools/              # Built-in tools
│   ├── read.go             # File reading
│   ├── write.go            # File writing
│   ├── edit.go             # File editing
│   ├── bash.go             # Shell command execution
│   ├── grep.go             # Content search
│   └── subagent.go         # Subagent spawning
│
├── pkg/skill/              # Skills system
│   ├── loader.go           # Skill loading from directories
│   ├── formatter.go        # Skill-to-prompt formatting
│   ├── parser.go           # Markdown/frontmatter parser
│   └── expander.go         # /skill: command expansion
│
├── pkg/config/             # Configuration management
├── pkg/compact/            # Context compression
├── pkg/session/            # Session management (JSONL)
└── internal/winai/         # Win-specific implementation
    └── interpreter.go      # REPL for ad editor (2707 lines)
```

## Core Components

### 1. Agent System (`pkg/agent/`)

**Event-Driven Architecture:**
- `AgentStart`, `AgentEnd` - Session lifecycle
- `TurnStart`, `TurnEnd` - Conversation turns
- `MessageStart`, `MessageUpdate`, `MessageEnd` - Message streaming
- `TextDelta`, `ToolCallDelta` - Incremental content
- `ToolExecutionStart`, `ToolExecutionEnd` - Tool lifecycle

**Agent Loop (`loop.go`):**
1. Receive user prompt
2. Call LLM with current context
3. Stream events (text deltas, tool calls)
4. Execute tools concurrently
5. Handle tool outputs and retry logic
6. Compress context when needed
7. Continue until no more tool calls

**Message Flow:**
```
RunLoop → streamAssistantResponse → LLM
                ↓
           Events (TextDelta, ToolCallDelta)
                ↓
           Tool Execution → Tool Results
                ↓
           Context Update → Next Turn
```

### 2. RPC Server (`pkg/rpc/`)

**Protocol:** JSON-RPC over stdin/stdout

**Key Commands (25+):**
- **Interaction:** `prompt`, `steer`, `follow_up`, `abort`
- **Sessions:** `new_session`, `switch_session`, `delete_session`, `list_sessions`
- **State:** `get_state`, `get_messages`, `get_session_stats`
- **Models:** `set_model`, `cycle_model`, `get_available_models`
- **Compression:** `compact`, `set_auto_compaction`, `set_thinking_level`
- **Forking:** `fork`, `get_tree`, `resume_on_branch`

### 3. Session Management (`pkg/session/`)

**Storage Format:** JSONL (one JSON object per line)

**Entry Types:**
- `session` - Session header with metadata
- `message` - Agent message (user/assistant/tool)
- `compaction` - Context compression event
- `branch_summary` - Branch fork summary
- `session_info` - Session metadata updates

**Tree Structure:** Supports branching and resuming from any point in conversation history.

### 4. Skills System (`pkg/skill/`)

**Skill Format:** Markdown with YAML frontmatter

**Locations:**
- Global: `~/.ai/skills/`
- Project: `.ai/skills/`

**Discovery:**
- Root `.md` files
- Recursive `SKILL.md` in subdirectories

**Loading:** Automatically added to system prompt (limited to 24 skills)

### 5. Context Compression (`pkg/compact/`)

**Triggers:**
- Message count threshold (default: 50)
- Token count threshold (default: 8000)
- Context limit recovery

**Strategy:**
- Keep recent messages (configurable)
- Summarize old messages into compact form
- Preserve thread structure for recovery

## Key Design Patterns

### 1. Streaming with Backpressure
All operations emit events through `llm.EventStream` with bounded channels.

### 2. Context Cancellation Propagation
- RPC server uses `context.WithCancel`
- Agent loop checks `ctx.Done()` each iteration
- Tool execution respects cancellation
- New operations use `context.Background()`

### 3. Type Sharing via `types.go`
Shared RPC types defined once in `pkg/rpc/types.go` to avoid duplication.

### 4. Async Session Writing
Session persistence happens in background goroutine via `sessionWriter` to avoid blocking.

## Interaction Modes

| Mode | Description | Output |
|------|-------------|--------|
| `rpc` | JSON-RPC over stdin/stdout | Event stream |
| `win` | REPL for ad editor | Interactive |
| `json` | Streaming JSON Lines | JSON objects |
| `headless` | Single JSON result | Final output only |

## Concurrency Model

**Tool Execution:** Thread-safe executor pool with configurable concurrency.

**Async Operations:**
- Session writing (buffered channel)
- Tool summarization (background goroutines)
- Event emission (non-blocking)

## Metrics and Observability

**Tracked Metrics (`pkg/agent/metrics.go`):**
- LLM calls (success/failure/retry)
- Token usage (input/output/total)
- Tool execution (timing/errors)
- Event counts by type

**Trace Events:** Structured logging with traceevent package.
