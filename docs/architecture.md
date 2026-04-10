# AI Agent System Architecture

## Overview

The `ai` project is a Go-based AI agent system with RPC-first design, optimized for editor integration. The system uses a code-driven workflow engine for reliable task scheduling, with focused agent workers executing specific tasks.

## Architecture Philosophy

**Core Principle:** Code-driven infrastructure, focused agent workers

- **Control flow is in code** (not LLM-driven)
- **Agents execute tasks** (not orchestrate workflows)
- **Deterministic scheduling** (not agent-driven state machines)
- **Human-in-the-loop checkpoints** (explicit in workflow templates)

### Why Code-Driven?

1. **Reliability**: Deterministic state machine, no LLM hallucinations in control flow
2. **Performance**: No extra LLM calls for workflow decisions
3. **Debuggability**: Clear code paths, easy to trace execution
4. **Testability**: Can unit test workflow logic without LLM

## System Architecture

### Component Diagram

```
┌─────────────────────────────────────────┐
│              Editor / CLI Client        │
└───────────────────┬─────────────────────┘
                    │ JSON-RPC over stdin/stdout
                    ▼
┌─────────────────────────────────────────┐
│          RPC Handlers (pkg/rpc/)        │
│  - Request parsing                      │
│  - Agent lifecycle management           │
│  - Session persistence                 │
└───────────────────┬─────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────┐
│         Agent Core (pkg/agent/)         │
│                                         │
│  ┌───────────────┐    ┌──────────────┐ │
│  │ Agent Loop    │────│ Context      │ │
│  │ (RunLoop)     │    │ Management   │ │
│  └───────────────┘    └──────────────┘ │
│         │                   │          │
│         ▼                   ▼          │
│  ┌───────────────┐    ┌──────────────┐ │
│  │ Tool Executor │────│ Compactor    │ │
│  │ Pool         │    │ (auto-compact)│ │
│  └───────────────┘    └──────────────┘ │
└───────────────────┬─────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────┐
│         Skills (~/.ai/skills/)          │
│                                         │
│  workflow/         orchestrate/          │
│  subagent/         review/              │
│  systematic-                             │
│  debugging/                              │
└─────────────────────────────────────────┘
```

### Workflow & Orchestrate Architecture

```
┌─────────────────────────────────────────┐
│         Workflow Skill (Frontend)      │
│  - User commands: /workflow start, auto │
│  - Template selection                   │
│  - Status display                       │
└────────────┬────────────────────────┘
             │ Calls CLI
             ▼
┌─────────────────────────────────────────┐
│    Orchestrate Runtime (Backend)       │
│  - Code-driven state machine (Go)       │
│  - Task scheduling from YAML templates  │
│  - Dependency enforcement              │
│  - Human checkpoint handling            │
└────────────┬────────────────────────┘
             │ Creates tasks
             ▼
┌─────────────────────────────────────────┐
│         Agent Workers (Executors)       │
│  - Receive task assignment              │
│  - Execute task (may use tools/subagents)│
│  - Output to .ai/team/outbox/           │
│  - Report completion/failure            │
└─────────────────────────────────────────┘
```

## Component Responsibilities

### RPC Handlers (`pkg/rpc/`)

**Purpose:** Interface between external clients and agent core.

**Key Files:**
- `rpc_handlers.go`: Main RPC handler functions
- `types.go`: Shared RPC types

**Key Handlers:**
- `handlePrompt`: Main interaction entry point
- `handleGetState`: Query agent state
- `handleCompact`: Manual context compaction
- `handleGetSessionStats`: Session metrics

### Agent Loop (`pkg/agent/loop.go`)

**Purpose:** Main agent execution loop.

**Key Phases:**
1. Build prompt (with context + tools)
2. Call LLM (streaming)
3. Execute tools (concurrent)
4. Update context
5. Auto-compact (if needed)
6. Emit events

**Guardrails:**
- `MaxConsecutiveToolCalls`: Prevent infinite loops
- `MaxToolCallsPerName`: Prevent tool abuse
- `MaxTurns`: Prevent runaway conversations
- `TaskTracking`: Remind agent of current task
- `ContextManagement`: Remind agent to compact
- `LLMTimeout`: Timeout LLM calls
- `LLMRetries`: Retry on rate limits

### Context Management (`pkg/context/`)

**Purpose:** Maintain healthy conversation context.

**Strategies:**
- Priority-based compaction (compact oldest first)
- Mini-compact for quick summaries
- Dynamic cheatsheet for code-heavy conversations
- ToolOutput truncation for large outputs

**Key Files:**
- `compactor.go`: Compaction logic
- `checkpoint.go`: Checkpoint management
- `reconstruction.go`: Context reconstruction

### Tool Executor (`pkg/tools/`)

**Purpose:** Execute tools with concurrency control.

**Features:**
- `MaxConcurrentTools`: Limit parallel tool execution
- `QueueTimeout`: Timeout queued tools
- Tool-specific timeouts
- Output size limits

**Key Tools:**
- `bash`: Execute shell commands
- `read`: Read file contents
- `write`: Write files
- `edit`: Edit files (fuzzy matching)
- `grep`: Search file contents
- `llm_context_recall`: Search external memory
- And more...

### Workflow System (`skills/workflow/`)

**Purpose:** Multi-phase development workflows.

**Templates:**
- `feature`: Feature development with approval checkpoints
- `bugfix`: Bug fix with verification
- `refactor`: Code refactoring
- `spike`: Exploratory research
- `hotfix`: Urgent bug fix
- `security`: Security issue handling

**Workflow Phases:**
- `explore`: Explore codebase
- `plan`: Create implementation plan
- `implement`: Implement changes
- `test`: Test implementation
- `review`: Review and approve
- `ship`: Deploy

### Orchestrate Runtime (`skills/workflow/orchestrate/`)

**Purpose:** Code-driven task scheduling runtime.

**Key Components:**
- `runtime.go`: Task scheduling and state management
- `api.go`: API for workers to interact with tasks
- `storage.go`: File-based task state persistence
- `types.go`: Task and state types

**Task States:**
- `pending`: Created, waiting to be claimed
- `claimed`: Claimed by a worker
- `in_progress`: Worker started execution
- `completed`: Task completed successfully
- `failed`: Task failed with error
- `blocked`: Dependencies not met

### Skills (`skills/`)

**Purpose:** Specialized workflows and expert prompts.

**Key Skills:**
- `workflow`: Multi-phase development workflows
- `orchestrate`: Multi-agent coordination runtime
- `subagent`: Isolated expert execution
- `systematic-debugging`: Four-phase bug fixing
- `review`: Code review with codex-rs methodology
- `tmux`: Tmux session management
- `using-git-worktrees`: Git worktree management
- `explore`: Codebase exploration
- And more...

## Data Flow

### Request Flow

```
User Request
  → RPC Handler
  → Agent.Start (spawn goroutine)
  → Loop.Run
  → Prompt Builder (build system prompt)
  → LLM API call (streaming)
  → Tool Execution (if tool calls)
  → Context Update
  → Auto-Compact (if threshold)
  → Emit Events
  → RPC Response
```

### Event Flow

```
Agent Loop
  → emitEvent(EventMessageStart)
  → emitEvent(EventToolCall)
  → emitEvent(EventToolResult)
  → emitEvent(EventTextDelta)
  → ...
  → Trace Event Store (~/.ai/traces/)
```

### Session Persistence

```
Messages (AgentMessage[])
  → Save to messages.jsonl
  → Load on restart
  → Compact (periodically)
```

### Workflow Execution

```
User: /workflow start feature "User auth"
  → Workflow skill parses command
  → Orchestrate runtime loads feature.yaml
  → Create tasks from phases (explore, plan, implement, test, review)
  → Workers claim and execute tasks
  → Output to .ai/team/outbox/
  → Human approves at checkpoints
  → Next task (if dependencies satisfied)
  → All complete → Cleanup
```

## Key Design Decisions

### Decision 1: RPC-First Design

**Context:** Initial design considered CLI vs RPC vs HTTP.

**Decision:** RPC over stdin/stdout.

**Rationale:**
- Standard interface for editor integration
- Language-agnostic
- Easier to test (mock RPC, no subprocess)
- Cleaner separation of concerns

**Trade-offs:**
- + Clean protocol (JSON-RPC)
- + Easy to add new RPC commands
- + Better testability
- - Requires external client
- - Mixed stderr/stdout with RPC output

### Decision 2: Stateful Agent with Sessions

**Context:** Stateless vs stateful agent.

**Decision:** Stateful with persistent sessions.

**Rationale:**
- Enable context preservation across restarts
- Support long-running workflows
- Resume interrupted work

**Trade-offs:**
- + Better user experience
- + Context preservation
- - More complex state management
- - Disk I/O overhead

### Decision 3: Code-Driven State Machine for Orchestrate

**Context:** Prompt-driven vs code-driven workflow engine.

**Decision:** Code-driven (Go state machine).

**Rationale:**
- More reliable (no LLM hallucinations in control flow)
- Better performance (no extra LLM calls)
- Easier to debug (deterministic)
- Testable without LLM

**Trade-offs:**
- + Reliable control flow
- + Better performance
- + Easier debugging
- - Less flexible (can't self-modify at runtime)
- - Requires code changes to modify workflow

### Decision 4: Tmux for Subagent Isolation

**Context:** Process isolation strategy for subagents.

**Options:** Goroutines (in-process), Docker containers, Tmux sessions, Separate processes (no terminal).

**Decision:** Tmux sessions.

**Rationale:**
- Easy to inspect/debug (tmux attach)
- Captures full output (including colors)
- Works on Linux/macOS
- Can kill entire session tree

**Trade-offs:**
- + Great debugging experience
- + Full output capture
- + Automatic cleanup on completion
- - Requires tmux installation
- - Slight startup overhead (2-3s)
- - Windows support limited

## Performance Characteristics

| Operation | Latency | Notes |
|-----------|---------|-------|
| Agent turn (no tools) | 1-3s | LLM streaming |
| Agent turn (with tools) | 3-30s | Depends on tool speed |
| Subagent startup | 2-3s | Tmux session creation |
| Auto-compact | 2-5s | LLM summarization |
| Context restoration | 1-2s | Load from disk |
| Task claim | <1s | File I/O |
| Task completion | <1s | File I/O |

## Security Considerations

- **Tool output sanitization**: Prevent prompt injection from malicious tool outputs
- **Path traversal protection**: In file tools (read/write/edit)
- **Execution timeout**: Prevent infinite loops in tools
- **Resource limits**: Max tokens, max turns, tool call limits
- **RPC isolation**: Client runs in separate process
- **Session isolation**: Sessions isolated by working directory
- **Subagent isolation**: Tmux provides process-level isolation

## Testing Strategy

### Test Pyramid

```
        E2E Tests (74 benchmark tasks)
              ↓
      Integration Tests
              ↓
        Unit Tests
```

### Layer 1: Unit Tests (Fast, Focused)

**Target:** 80% coverage of business logic.

**Examples:**
- `pkg/context/compactor_test.go`: Test compaction logic
- `pkg/agent/loop_test.go`: Test loop behavior
- `pkg/tools/bash_test.go`: Test bash tool timeout

### Layer 2: Integration Tests (Medium, Realistic)

**Target:** Cover all major integration points.

**Examples:**
- `pkg/agent/agent_integration_test.go`: Agent + tools
- `pkg/rpc/`: RPC handlers + agent
- Workflow execution end-to-end

### Layer 3: E2E Tests (Slow, Realistic)

**Target:** 74 benchmark tasks covering:
- Bug fixing (agent_001-010)
- Code generation (001-013)
- Context management (004, 010, 011)
- Tool usage (006)
- Budget management (008)

## Multi-Agent Patterns (Supported)

### Pattern 1: Worker Uses Subagent

```bash
# Inside a worker task
SESSION=$(start_subagent @reviewer.md "Review these changes")
result=$(tmux_wait.sh $SESSION /tmp/output.txt)
```

**Use Case:** Need expert agent for focused task.

**Rules:**
- ✅ Use focused system prompts
- ✅ Clear success criteria
- ❌ Don't use generic prompts
- ❌ Don't reuse subagent across tasks

### Pattern 2: Parallel Tasks

```yaml
phases:
  - id: task1
    subject: "Task 1"
  - id: task2
    subject: "Task 2"
  # task1 and task2 can run in parallel (no dependencies)
```

**Use Case:** Independent tasks that can run concurrently.

**Rules:**
- ✅ Parallelize independent tasks
- ✅ Use multiple workers
- ❌ Don't share state between tasks

## Anti-Patterns (Avoided)

❌ **Orchestrator Agent** - LLM doesn't control workflow (code-driven instead)

❌ **Worker Orchestrating** - Workers don't spawn workers (runtime does)

❌ **Nested Subagents** - Subagent spawning subagent (not supported)

❌ **State Sharing** - Workers sharing global state (should be isolated)

❌ **Agent as Hammer** - Using subagent for everything (use for focused tasks only)

## Future Directions

### Potential Improvements

1. **Workflow Optimization**: Use session-analyzer offline to improve templates
2. **Test Coverage**: Increase coverage for pkg/tools and pkg/rpc
3. **Performance Metrics**: Add detailed performance tracking
4. **Documentation**: More detailed usage examples
5. **Error Recovery**: Better error recovery and retry logic

### Out of Scope

- Multi-agent orchestration (LLM-driven)
- Automatic system prompt optimization (manual instead)
- Real-time workflow self-modification (offline optimization instead)
- Distributed execution (single-machine focus)