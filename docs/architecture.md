# AI Agent System Architecture

## Overview

The `ai` project is a Go-based AI coding agent with RPC-first design, optimized for editor integration and multi-agent orchestration. The system uses a subcommand-based CLI, code-driven task scheduling, and focused agent workers.

## Architecture Philosophy

**Core Principle:** Code-driven infrastructure, focused agent workers

- **Control flow is in code** (not LLM-driven)
- **Agents execute tasks** (not orchestrate workflows)
- **Deterministic scheduling** (not agent-driven state machines)
- **Human-in-the-loop checkpoints** (explicit in workflow templates)

## System Architecture

### Component Diagram

```
┌────────────────────────────────────────────────────────────────┐
│                    CLI / Editor / TUI Client                    │
│  ai run (TUI)  |  ai serve + ai watch  |  ai rpc (stdin/stdout)│
└──────────────────────────┬─────────────────────────────────────┘
                           │ JSON-RPC over stdin/stdout
                           │ Unix socket (run/serve)
                           ▼
┌────────────────────────────────────────────────────────────────┐
│               cmd/ai — CLI Entry Point                          │
│                                                                 │
│  main.go          — Flag parsing, calls app.RunRPC()         │
│                   Subcommand dispatch (run/serve/rpc/ls/...)    │
│                   via pkg/cli                                   │
└──────────────────────────┬─────────────────────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────────────┐
│                pkg/rpc — RPC Server                             │
│  server.go  — JSON-RPC read/write loop                          │
│  types.go   — Shared RPC types (commands, responses, events)    │
└──────────────────────────┬─────────────────────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────────────┐
│                pkg/agent — Agent Core                           │
│                                                                 │
│  ┌──────────────────┐    ┌──────────────────────────────────┐  │
│  │ Agent (agent.go) │────│ Loop (loop.go)                    │  │
│  │ - Lifecycle mgmt │    │ - Turn execution                  │  │
│  │ - Event emission │    │ - Tool call routing               │  │
│  │ - Stream control │    │ - LLM retry + error recovery      │  │
│  │ - Config mgmt    │    │ - Runtime telemetry injection      │  │
│  └──────────────────┘    └──────────────────────────────────┘  │
│         │                          │                            │
│         ▼                          ▼                            │
│  ┌──────────────────┐    ┌──────────────────────────────────┐  │
│  │ Executor Pool    │    │ Context Management                │  │
│  │ (executor.go)    │    │ (via pkg/compact/)                │  │
│  │ - Concurrency    │    │ - LLM-driven compaction           │  │
│  │ - Tool dispatch  │    │ - Auto-compact on thresholds      │  │
│  │ - Timeout guard  │    │ - Truncate / update / compact     │  │
│  └──────────────────┘    └──────────────────────────────────┘  │
│                                                                 │
│  ┌──────────────────┐    ┌──────────────────────────────────┐  │
│  │ Metrics          │    │ Tool Guards (tool_guard.go)        │  │
│  │ (metrics.go)     │    │ - Max consecutive calls            │  │
│  │ - Token rates    │    │ - Max calls per tool name          │  │
│  │ - Turn tracking  │    │ - Malformed call recovery          │  │
│  └──────────────────┘    └──────────────────────────────────┘  │
└──────────────────────────┬─────────────────────────────────────┘
                           │
              ┌────────────┼────────────┐
              ▼            ▼            ▼
┌──────────────────┐ ┌──────────┐ ┌──────────────────┐
│ pkg/tools        │ │ pkg/llm  │ │ pkg/context       │
│ - bash           │ │ - OpenAI │ │ - AgentContext    │
│ - read/write     │ │   compat │ │ - Messages        │
│ - edit           │ │ - Stream │ │ - Checkpoint      │
│ - grep           │ │ - Retry  │ │ - Journal         │
│ - change_workspace│ │          │ │ - Compaction      │
│                  │ │          │ │ - Reconstruction  │
└──────────────────┘ └──────────┘ └──────────────────┘
```

### Supporting Packages

| Package | Responsibility |
|---------|---------------|
| `pkg/config` | Configuration loading, API key resolution, model specs |
| `pkg/session` | Append-only JSONL session storage, fork support, lazy loading |
| `pkg/prompt` | System prompt construction, skill expansion, thinking instructions |
| `pkg/skill` | Skill discovery, loading (frontmatter parsing), formatting |
| `pkg/compact` | LLM-driven compaction with cache-friendly summarization (LLMDecide mode) |
| `pkg/traceevent` | Perfetto-compatible trace event recording and export |
| `pkg/truncate` | Tool output truncation with head/tail preservation |
| `pkg/modelselect` | Model selection and spec resolution |
| `pkg/command` | Slash command registry |
| `pkg/run` | Run metadata, socket server for `ai serve`/`ai run` |
| `pkg/logger` | Structured logging with file rotation |
| `pkg/version` | Version information |

### Agent Orchestration (`ag`)

```
┌────────────────────────────────────────────────────────────────┐
│  Agent Orchestration (bridge-per-agent via skills)              │
│  Multi-agent orchestration via skill-defined workflows          │
│                                                                 │
│  Skills define orchestration patterns using `ai` CLI:           │
│  - spawn/kill sub-agents via ai serve/send/watch               │
│  - steer/abort running agents                                   │
│  - Bridge-per-agent: detached process with socket control      │
│                                                                 │
│  Storage: .ag/ directory (CWD-scoped)                           │
│  Backends: ai (json-rpc), codex (raw), pluggable via config     │
└────────────────────────────────────────────────────────────────┘
```

## Data Flow

### Typical Turn (RPC mode)

```
1. Client sends: {"type":"prompt","message":"fix the bug"}
2. RPC Server receives → Agent.Prompt()
3. Agent acquires lock → Appends user message to context
4. Agent.RunLoop():
   a. Build system prompt (Builder: tools + skills + project context + telemetry)
   b. Convert context to LLM messages (with visibility filtering)
   c. Call LLM API (streaming, with retry on rate limit)
   d. Stream response: emit text_delta / toolcall_delta events
   e. If tool calls: execute via ExecutorPool → append results → repeat from (a)
   f. Emit turn_end event
5. Auto-compact check: if token threshold exceeded, trigger compaction
6. Session persistence: append entries to messages.jsonl
7. Checkpoint: periodic snapshot for fast recovery
```

### Context Management Flow

```
1. Turn completes → check token usage (EstimateTokens)
2. If SoftThreshold < tokens < HardLimit:
   a. At interval boundary → askLLM: "compact now?" (cache-friendly yes/no)
   b. LLM says yes → compact; LLM says no → skip until next interval
   c. On ask error → compact as fallback
3. If tokens >= HardLimit: compact immediately (no ask)
4. Compact(): split messages by token budget, summarize old via LLM,
   fix tool-call pairing, archive excess tool results, clean stale state
5. Persist: AppendCompaction → snapshot file + compaction entry in messages.jsonl
```

See [context-management.md](context-management.md) for full details.

### Session Persistence

```
Session Directory (~/.ai/sessions/--<git-root>--/)
├── messages.jsonl          # Append-only entries
│   ├── {"type":"session","id":"...","cwd":"..."}   # Header
│   ├── {"type":"message","id":"abc1","parentId":null,...}
│   ├── {"type":"message","id":"abc2","parentId":"abc1",...}
│   ├── {"type":"truncate","id":"abc3",...}
│   └── {"type":"compact","id":"abc4",...}
├── agent_state.json        # Persisted AgentState (turn, CWD, etc.)
└── compactions/            # Compaction snapshot files
```

Recovery: load messages from session JSONL (handles compaction snapshots) → load `agent_state.json` for AgentState → rebuild in-memory state.

## Key Design Decisions

### Decision 1: RPC-First Architecture

**Context:** How to integrate with editors and external tools.

**Decision:** JSON-RPC over stdin/stdout as the primary interface.

**Rationale:**
- Universal integration (any language, any editor)
- Clean process boundary (crash isolation)
- Streaming support via event protocol
- Subcommands (run/serve) build on top of rpc

### Decision 2: Subcommand-Based CLI

**Context:** How to expose different operational modes.

**Decision:** Subcommands (`ai rpc`, `ai run`, `ai serve`, `ai ls`, `ai watch`, `ai send`, `ai kill`) instead of `--mode` flags.

**Rationale:**
- Clearer semantics (each command does one thing)
- Independent flag sets per subcommand
- Subcommands fully replace the old `--mode` flag
- `ai run` = subprocess rpc + TUI in one process
- `ai serve` = daemon mode with socket control

### Decision 3: LLM-Driven Context Management

**Context:** How to manage context within LLM window limits.

**Decision:** Delegate context management decisions to a separate LLM call with dedicated tools.

**Rationale:**
- LLM can distinguish relevant vs stale content (rules cannot)
- Flexible strategy (truncate, summarize, or skip)
- System controls timing; LLM controls strategy
- See [context-management.md](context-management.md) for full details

### Decision 4: Bridge-Per-Agent Architecture

**Context:** Process isolation for agent orchestration.

**Decision:** Each agent gets its own bridge process (not tmux, not in-process).

**Rationale:**
- No central daemon (one crash doesn't take down others)
- No tmux dependency
- Direct process control via Unix socket
- Backend-agnostic (any CLI-based agent)

### Decision 5: Append-Only Session Storage

**Context:** How to persist conversation state.

**Decision:** Append-only JSONL with AgentState persistence.

**Rationale:**
- Crash-safe (partial writes don't corrupt)
- Efficient (no rewriting)
- Fork support (tree structure via parent IDs)
- Fast recovery (AgentState + compaction snapshots)

## Performance Characteristics

| Operation | Latency | Notes |
|-----------|---------|-------|
| Agent turn (no tools) | 1-3s | LLM streaming |
| Agent turn (with tools) | 3-30s | Depends on tool speed |
| Auto-compact | 2-5s | LLM summarization |
| Context restoration | <1s | Checkpoint + journal replay |
| Agent spawn (ag) | 2-3s | Bridge + subprocess startup |
| Session load (lazy) | <100ms | Lazy loading from JSONL |

## Security Considerations

- **Tool output truncation**: Prevents context overflow from large tool outputs
- **Execution timeout**: Configurable per-tool and per-turn timeouts
- **Resource limits**: Max consecutive tool calls, max turns, token limits
- **Session isolation**: Sessions scoped by git repository root
- **Agent isolation**: Each sub-agent runs as an independent process
- **Path protection**: Tools validate file paths within workspace
- **API key storage**: Support for both env vars and file-based credentials

## Testing Strategy

See [test-strategy.md](test-strategy.md) for detailed testing approach.

## Package Structure

```
ai/
├── cmd/ai/           # CLI entry point
│   └── main.go       # Flag parsing → app.RunRPC()
├── pkg/
│   ├── agent/        # Core agent loop, execution, metrics
│   ├── agentconfig/  # Agent configuration types
│   ├── cli/          # CLI subcommands (run/serve/ls/send/kill/watch)
│   ├── middlewares/  # RPC middleware
│   ├── compact/      # LLM-driven compaction (LLMDecide mode)
│   ├── command/      # Slash command registry
│   ├── config/       # Configuration, auth, model specs
│   ├── context/      # Agent context, messages, AgentState persistence
│   ├── llm/          # LLM client (OpenAI-compatible)
│   ├── logger/       # Structured logging
│   ├── modelselect/  # Model selection logic
│   ├── prompt/       # System prompt builder
│   ├── rpc/          # RPC server, types
│   ├── app/       # RPC application (handlers, setup, session writer)
│   ├── run/          # Run metadata, socket server
│   ├── session/      # Session persistence (JSONL)
│   ├── skill/        # Skill loading and formatting
│   ├── testutil/     # Test utilities
│   ├── tools/        # Tool implementations (bash, read, write, edit, grep, etc.)
│   ├── traceevent/   # Perfetto-compatible tracing
│   ├── truncate/     # Output truncation
│   └── version/      # Version info
├── skills/           # Skill definitions (user-installed and project-level)
├── docs/             # Documentation
├── benchmark/        # E2E benchmark tasks
└── tests/            # Integration test scripts
```