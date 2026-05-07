# Context Management Design

This document describes the context management architecture — how the agent manages its conversation context to stay within LLM window limits while preserving task continuity.

It is implementation-aligned with the current codebase. If behavior changes, update this document in the same PR.

## 1. Design Philosophy: LLM-Driven Context Management

**The core idea: context management decisions are made by LLM, not by rules.**

Traditional context management uses deterministic rules: "when tokens > X, summarize the oldest Y messages". This approach is brittle — it cannot distinguish between a stale debug log and a critical error message.

Our architecture delegates the decision to a **separate LLM call**:

1. **When to act** — The system decides *when* to trigger a context management cycle (based on token thresholds and tool-call intervals), but it does **not** decide *what to do*.
2. **What to do** — A dedicated LLM call receives the full conversation context (with annotations) and a set of context management tools. It decides: truncate specific messages? Update the LLM context summary? Perform a full compact? Or take no action?
3. **Tool-driven execution** — The LLM's decisions are executed through tool calls (`truncate_messages`, `update_llm_context`, `compact`, `no_action`), each operating on the shared `AgentContext`.

This means:
- The LLM can read a tool output and judge "this grep result is still relevant to the current task" vs "this is stale scaffolding output" — something rules cannot do.
- The LLM can update the structured LLM Context to reflect the *current* task state, preserving continuity after compaction.
- The system controls *timing*; the LLM controls *strategy*.

## 2. Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                        Agent Loop                            │
│  (pkg/agent/loop.go)                                        │
│                                                              │
│  Two compaction triggers:                                    │
│                                                              │
│  ┌───────────────────┐    ┌───────────────────────────┐     │
│  │  Pre-LLM          │    │  Context-Limit Recovery    │     │
│  │  Threshold Check  │    │  (on API error)            │     │
│  │  (every turn)     │    │  (max 1 per session)       │     │
│  └────────┬──────────┘    └──────────┬────────────────┘     │
│           │                           │                       │
│           ▼                           ▼                       │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │           Compactor Chain (priority order)               │ │
│  │                                                          │ │
│  │  1. ContextManager (LLM-driven lightweight)              │ │
│  │     pkg/compact/context_management.go                    │ │
│  │     - Makes independent LLM call with mgmt tools         │ │
│  │     - LLM decides: truncate / update / compact / skip    │ │
│  │                                                          │ │
│  │  2. sessionCompactor (heavyweight LLM summarization)     │ │
│  │     cmd/ai/session_writer.go                             │ │
│  │     - Delegates to compact.Compactor for full summary     │ │
│  │     - Persists via session + journal                     │ │
│  └─────────────────────────────────────────────────────────┘ │
│                           │                                   │
│                           ▼                                   │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │         AgentContext (in-memory state)                    │ │
│  │                                                          │ │
│  │  LLMContext: string          — task state (LLM-managed)  │ │
│  │  RecentMessages: []AgentMessage                          │ │
│  │  AgentState: *AgentState     — system metadata           │ │
│  └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## 3. Compaction Triggers

### 3.1 Pre-LLM Threshold Check

Before every LLM call, the agent loop checks:

```
estimatedTokens > dynamicThreshold
```

Where `dynamicThreshold` is calculated by `Compactor.CalculateDynamicThreshold()`:

```
threshold = contextWindow - systemPromptTokens - 3000(tool defs) - ReserveTokens(16384)
```

If exceeded, compactors are tried in priority order (array order in `LoopConfig.Compactors`). The first compactor whose `ShouldCompact()` returns true is used.

### 3.2 Context-Limit Recovery

If the LLM API returns a context-length-exceeded error:

1. Compact the context using the heavyweight compactor
2. Retry the LLM call
3. Maximum one recovery attempt per session to prevent infinite loops

This is the safety net for cases where the threshold check underestimated token usage.

## 4. Compactor Implementations

### 4.1 ContextManager (Lightweight, LLM-Driven)

**File:** `pkg/compact/context_management.go`

This is the primary compaction strategy. It makes a **separate LLM call** with:
- The current conversation as context (with stale annotations)
- Context management tools: `truncate_messages`, `update_llm_context`, `compact`, `no_action`
- A specialized system prompt (`pkg/prompt/context_management.md`)

The LLM decides what action to take. Common outcomes:
- **Truncate**: Remove stale tool outputs from early in the conversation
- **Update LLM Context**: Update the task state summary for continuity
- **Compact**: Trigger full summarization (falls through to heavyweight compactor)
- **No Action**: Context is healthy, do nothing

### 4.2 Compact (Heavyweight Summarization)

**File:** `pkg/compact/compact.go`

Full conversation summarization via LLM:
1. Sends the entire conversation to a summarization LLM
2. Generates a summary of the conversation so far
3. Replaces old messages with the summary
4. Preserves recent messages (configurable `keepRecent`)

### 4.3 Session Compaction Bridge

**File:** `cmd/ai/session_writer.go`

Wraps the heavyweight compactor with session-aware persistence:
- Records compaction as a journal entry
- Manages the compaction cut-point in the session file
- Ensures session file consistency after compaction

## 5. Context Management Tools

These tools are provided to the context management LLM:

### 5.1 `truncate_messages`

**File:** `pkg/tools/context_mgmt/truncate_messages.go`

Removes specific messages from the conversation. The LLM specifies which message IDs to truncate based on its analysis of staleness.

### 5.2 `update_llm_context`

**File:** `pkg/tools/context_mgmt/update_llm_context.go`

Updates the `LLMContext` string — the structured task state that persists across compactions. This is the primary mechanism for task continuity.

### 5.3 `no_action`

**File:** `pkg/tools/context_mgmt/no_action.go`

Signals that no context management is needed. The LLM uses this when the context is healthy.

### 5.4 `compact`

**File:** `pkg/compact/compact_tool.go`

Triggers full heavyweight compaction. Used when the LLM determines that truncation alone is insufficient.

## 6. LLM Context (Task State)

`AgentContext.LLMContext` is a string maintained by the context management LLM. It serves as the **source of truth for task continuity** after compaction.

### 6.1 How It Works

1. Before compaction, the context management LLM reads the full conversation
2. It uses `update_llm_context` to write a structured summary of current task state
3. After compaction, `LLMContext` is injected into every future LLM request
4. The agent continues with full awareness of what happened before compaction

### 6.2 Injection Point

`LLMContext` content is injected into the system prompt via `insertBeforeLastUserMessage`. This ensures:
- The LLM always sees current task state
- KV cache efficiency for earlier messages is preserved in multi-turn conversations

### 6.3 Runtime Telemetry

The agent loop injects runtime state as informational telemetry into the system prompt:

```yaml
context_meta:
  tokens_band: 20-40
  action_hint: light_compression
  tokens_used_approx: 40000
  tokens_max: 200000
```

This keeps the agent informed about context pressure without compelling it to act directly (the context management LLM handles that).

## 7. Session Persistence & Recovery

### 7.1 Journal Format

Session entries in `messages.jsonl`:

| Type | Description |
|------|-------------|
| `session` | Header with session ID and metadata |
| `message` | User/assistant/tool message |
| `truncate` | Record of truncated messages |
| `compact` | Compaction cut-point marker |

### 7.2 Checkpoint System

**File:** `pkg/context/checkpoint.go`

Periodic snapshots for fast recovery:
- Checkpoint: Full state serialized to `checkpoint.jsonl`
- Checkpoint index: Maps entry IDs to checkpoint positions
- Recovery: Load checkpoint → replay journal entries after checkpoint

### 7.3 Reconstruction

**File:** `pkg/context/reconstruction.go`

Rebuilds in-memory state from checkpoint + journal:
1. Load checkpoint file
2. Find all journal entries after the checkpoint
3. Replay entries to rebuild `AgentContext`
4. Track reconstruction counters for diagnostics

## 8. Tool Output Processing

### 8.1 Normalization

**File:** `pkg/agent/tool_output.go`

Every tool output is normalized before being added to context:
- **Text output**: Truncated to 10,000 chars (head+tail preservation) if it exceeds the limit
- **Error patterns**: Detected and preserved with higher priority
- **Images**: Preserved completely

### 8.2 Stale Output Detection

For runtime telemetry (not automatic action), stale tool outputs are counted:
- Tool results within the last 10 messages are "fresh"
- Beyond that: "stale" and reported as truncation candidates

### 8.3 Tool Call Cutoff

When the number of visible tool results exceeds `ToolCallCutoff` (default: 10), the oldest tool outputs are summarized. This prevents context bloat from accumulated tool results.

## 9. Token Estimation

**File:** `pkg/context/context.go`

`AgentContext.EstimateTokens()` uses:
1. Last assistant usage totals (from API response) if available
2. Plus heuristic estimation for trailing messages since last usage report
3. Per-message heuristic: `ceil(len(text) / 4)`, images ~1200 tokens each

## 10. Key File Index

| File | Responsibility |
|------|---------------|
| `pkg/context/context.go` | AgentContext, core data types |
| `pkg/context/message.go` | AgentMessage, ContentBlock types |
| `pkg/context/agent_state.go` | AgentState tracking metadata |
| `pkg/context/snapshot.go` | ContextSnapshot for checkpoint persistence |
| `pkg/context/compactor.go` | Compactor interface, CompactionResult |
| `pkg/context/journal.go` | JournalEntry types (message/truncate/compact) |
| `pkg/context/journal_io.go` | Journal I/O operations |
| `pkg/context/checkpoint.go` | Checkpoint save/load, symlink management |
| `pkg/context/checkpoint_index.go` | Checkpoint index for fast lookup |
| `pkg/context/checkpoint_io.go` | Checkpoint I/O operations |
| `pkg/context/reconstruction.go` | Snapshot reconstruction from checkpoint + journal replay |
| `pkg/compact/compact.go` | Heavyweight Compactor (LLM summarization) |
| `pkg/compact/compact_tool.go` | Compact tool (exposed to context management LLM) |
| `pkg/compact/context_management.go` | ContextManager: LLM-driven context management cycle |
| `pkg/tools/context_mgmt/truncate_messages.go` | Truncate tool implementation |
| `pkg/tools/context_mgmt/update_llm_context.go` | LLM Context update tool |
| `pkg/tools/context_mgmt/no_action.go` | No-action tool |
| `pkg/tools/context_mgmt/registry.go` | Context management tool registry |
| `pkg/agent/loop.go` | Agent loop, compaction triggers, telemetry injection |
| `pkg/agent/agent.go` | Agent lifecycle, auto-compact, config management |
| `pkg/agent/conversion.go` | Message visibility filtering, LLM message conversion |
| `pkg/agent/tool_output.go` | Tool output normalization |
| `pkg/agent/tool_call_normalize.go` | Tool call normalization |
| `pkg/agent/checkpoint_manager.go` | Checkpoint lifecycle management |
| `pkg/agent/metrics.go` | Metrics collection (token rates, turn tracking) |
| `pkg/agent/executor.go` | Tool execution with concurrency control |
| `pkg/session/compaction.go` | Session-level compaction cut-point logic |
| `pkg/session/session.go` | Session persistence (JSONL, fork support) |
| `pkg/session/lazy.go` | Lazy session loading |
| `cmd/ai/session_writer.go` | sessionCompactor: session-aware compaction wrapper |
| `pkg/prompt/builder.go` | System prompt construction |
| `pkg/prompt/context_management.md` | System prompt for context management LLM |
| `pkg/prompt/compact_system.md` | Summarization assistant system prompt |
| `pkg/prompt/compact_summarize.md` | Summarization template |
| `pkg/prompt/compact_update.md` | Incremental summary update template |

## 11. Design Principles

1. **LLM decides strategy, system decides timing** — The system controls *when* to trigger context management (token thresholds, intervals). The LLM controls *what to do* (truncate, update context, compact, or skip).

2. **LLM Context is the task state source of truth** — After compaction, `LLMContext` (maintained by the LLM via `update_llm_context`) is the only source of task continuity. Old messages are gone.

3. **Separation of LLMs** — Context management uses a completely separate LLM call with a dedicated system prompt. The main agent LLM is never involved in context management decisions.

4. **Append-only journal, periodic snapshots** — `messages.jsonl` is append-only. Checkpoints provide efficient recovery points. Reconstruction replays journal entries after checkpoint.

5. **LLM Context injection** — `LLMContext` content is always injected into LLM requests when non-empty, so the agent maintains task continuity after compaction or context updates.

6. **Protected recent messages** — The last few messages are never truncated, ensuring the agent always has its most recent context.

7. **Telemetry, not directives** — Runtime state is injected as informational telemetry (not commands), keeping the main agent informed about context pressure without compelling it to act directly.