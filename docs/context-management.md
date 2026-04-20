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
│  │  PostCompactRecovery: bool   — signal for next turn      │ │
│  └─────────────────────────────────────────────────────────┘ │
│                           │                                   │
│                           ▼                                   │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │          Persistence Layer                                │ │
│  │                                                          │ │
│  │  Journal (messages.jsonl) — append-only event log         │ │
│  │  Checkpoints — periodic snapshots (checkpoint + symlink)  │ │
│  └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## 3. The ContextManager: LLM as Decision-Maker

`pkg/compact/context_management.go`

### 3.1 Trigger Logic (System Decides *When*)

The system uses token percentage + tool-call interval to decide when to run a context management cycle:

| Token Usage  | Check Interval (tool calls) |
|--------------|-----------------------------|
| < 20%        | No checks                   |
| 20%–33%      | Every 15 tool calls         |
| 33%–50%      | Every 10 tool calls         |
| 50%+         | Every 7 tool calls          |

### 3.2 Decision Cycle (LLM Decides *What*)

When triggered, `ContextManager.CompactWithCtx()`:

1. **Builds an annotated conversation** — the full `RecentMessages` with truncation status, tool output previews, and the current LLM Context.
2. **Creates context management tools** — the LLM can choose from:
   - `truncate_messages` — replace specific old tool outputs with head/tail summary
   - `update_llm_context` — update the structured task state string
   - `compact` — full compaction via the heavyweight Compactor (summarize + trim messages)
   - `no_action` — context is healthy, do nothing
3. **Makes an independent LLM call** — with a dedicated system prompt (`pkg/prompt/context_management.md`), the conversation context, and the management tools.
4. **Executes the LLM's tool calls** — applies truncate/update/compact operations to `AgentContext`.

The main agent LLM is **never** involved in context management decisions. This separation ensures the management LLM focuses solely on context quality.

### 3.3 Why Tool-Based, Not Rule-Based

| Scenario | Rule-Based | LLM-Driven (our approach) |
|----------|-----------|--------------------------|
| Old grep output from a resolved bug | Truncate by age → might lose it | LLM reads it → "resolved, safe to truncate" |
| Critical error from 10 turns ago | Truncate by size → might remove it | LLM reads it → "still relevant, keep it" |
| Task phase completed | Cannot detect | LLM detects phase shift → compact |
| Context is healthy | Still runs checks | LLM returns `no_action` |

## 4. Core Data Structures

### 4.1 AgentContext (`pkg/context/context.go`)

The central in-memory state:

```go
type AgentContext struct {
    SystemPrompt   string
    Tools          []Tool
    Skills         []skill.Skill

    LLMContext     string         // Structured task state maintained by LLM
    RecentMessages []AgentMessage // Conversation history
    AgentState     *AgentState    // System-maintained metadata

    LastCompactionSummary string
    PostCompactRecovery   bool       // Inject LLMContext after compact

    OnCompactEvent func(*CompactEventDetail) error
}
```

Note: `AgentContext` contains the same fields as `ContextSnapshot` but does **not** embed it. They serve different purposes:
- `AgentContext` is the mutable, live state with callbacks and tool management
- `ContextSnapshot` is an immutable, serializable point-in-time capture for checkpoint persistence

### 4.2 AgentState (`pkg/context/agent_state.go`)

System-maintained tracking metadata (LLM never writes this directly):

```go
type AgentState struct {
    WorkspaceRoot     string
    CurrentWorkingDir string
    TotalTurns        int
    TokensUsed        int
    TokensLimit       int

    // Context management tracking
    LastLLMContextUpdate      int  // Turn when LLMContext was last updated
    LastTriggerTurn           int  // Turn when context management last ran
    TurnsSinceLastTrigger     int  // Turns since last trigger
    ToolCallsSinceLastTrigger int  // Tool calls since last trigger
    TotalTruncations          int
    TotalCompactions           int
    LastCompactTurn            int

    // Runtime telemetry cache
    RuntimeMetaSnapshot string
    RuntimeMetaBand     string
    RuntimeMetaTurns    int
}
```

### 4.3 AgentMessage (`pkg/context/message.go`)

```go
type AgentMessage struct {
    Role       string         // "user", "assistant", "toolResult"
    Content    []ContentBlock // TextContent, ImageContent, ToolCallContent, ThinkingContent
    Metadata   *MessageMetadata
    Truncated  bool           // Set by truncate_messages tool
    ToolCallID string         // For toolResult ↔ toolCall pairing
    // ...
}
```

Message visibility is controlled by `MessageMetadata`:

| Flag | Default | Effect |
|------|---------|--------|
| `AgentVisible` | true (nil = true) | If false, excluded from `ConvertMessagesToLLM` and token estimation |
| `UserVisible` | true (nil = true) | If false, excluded from UI display |
| `Kind` | "" | Semantic type: "tool_result", "tool_result_archived", etc. |

## 5. Compaction Triggers

### 5.1 Pre-LLM Threshold Compaction

Before each LLM call, the loop iterates through the Compactor chain:

```go
for _, c := range config.Compactors {
    if c.ShouldCompact(ctx, agentCtx) {
        compacted, compactErr = c.Compact(agentCtx)
        if compactErr == nil {
            break // First successful compaction wins
        }
    }
}
```

The chain has two entries (in `cmd/ai/rpc_handlers.go`):

1. **ContextManager** — lightweight LLM-driven (see §3)
2. **sessionCompactor** — heavyweight; wraps `compact.Compactor` for full summarization and persists via `Session.Compact()`

### 5.2 Context-Limit Recovery

After an `IsContextLengthExceeded` API error, the loop attempts compaction once (`maxCompactionRecoveries = 1`), then retries the LLM call.

### 5.3 The Compactor Interface

```go
type Compactor interface {
    ShouldCompact(ctx context.Context, agentCtx *AgentContext) bool
    Compact(ctx *AgentContext) (*CompactionResult, error)
    CalculateDynamicThreshold() int
}
```

## 6. Compaction Strategies in Detail

### 6.1 truncate_messages (`pkg/tools/context_mgmt/truncate_messages.go`)

The lightest action. The LLM selects specific tool result messages by ID, and the tool:

1. Validates IDs exist and are not in the protected zone (last `RecentMessagesKeep=5` messages)
2. Marks each message `Truncated=true`
3. Replaces content with `TruncateWithHeadTail()` output: first 1000 chars + last 1000 chars with a truncation marker

This is the primary tool for reclaiming tokens — most tool outputs are large and become irrelevant after a few turns.

### 6.2 update_llm_context (`pkg/tools/context_mgmt/update_llm_context.go`)

Updates the in-memory `AgentContext.LLMContext` string. This is the LLM's mechanism for maintaining a structured summary of:

- Current task and progress
- Key files and code elements
- Errors encountered
- Decisions made
- Next steps

After compaction, `LLMContext` is the **only** source of task continuity — the old messages are gone. This makes updating LLM Context before or during compaction critical.

### 6.3 compact (`pkg/compact/compact_tool.go`)

The heavy action. Delegates to the full `compact.Compactor` which:

1. Finds the cut point using `KeepRecentTokens` (default 20,000) from the tail
2. Summarizes removed messages via a separate LLM call (prompts: `compact_system.md`, `compact_summarize.md`)
3. Replaces `RecentMessages` with: summary message + kept recent messages
4. Preserves `LLMContext` — never overwritten (it is managed by the LLM via `update_llm_context`)

After compact, `PostCompactRecovery=true` is set, causing the next turn to inject `LLMContext` content into the LLM messages so the agent recovers task continuity.

### 6.4 no_action (`pkg/tools/context_mgmt/no_action.go`)

The LLM explicitly declines action. This is a valid outcome — the LLM judges the context is healthy and no management is needed.

## 7. Persistence and Recovery

### 7.1 Journal (`messages.jsonl`)

Append-only event log. Entry types:

| Type | Purpose |
|------|---------|
| `message` | Normal conversation message |
| `truncate` | Records which tool output was truncated (ID + turn) |
| `compact`  | Records summary + how many messages were kept |

Truncate and compact entries enable **reconstruction**: when replaying journal entries, truncate events are re-applied to messages and compact events reset the message list.

### 7.2 Checkpoints

Periodic snapshots stored under `sessionDir/checkpoints/` with a `current` symlink to the latest. Managed by `AgentContextCheckpointManager` (`pkg/agent/checkpoint_manager.go`).

**When created**: After compaction when `LLMContextUpdated == true` (meaningful state change). Skipped when only truncation occurred (resume can replay from last checkpoint).

**What's stored**: Full `ContextSnapshot` — `LLMContext`, `RecentMessages`, `AgentState`.

### 7.3 Reconstruction (`pkg/context/reconstruction.go`)

On resume, `ReconstructSnapshotWithCheckpoint`:

1. Load latest checkpoint
2. Replay journal entries after checkpoint's `MessageIndex`
3. Apply truncate events to replayed messages
4. Apply compact events (reset messages, set summary as LLMContext)
5. Recalculate runtime counters from replayed messages

## 8. Runtime Telemetry

### 8.1 runtime_state Snapshot

Injected as a user message in every LLM call (from turn 1). Contains:

- Token usage band and percentage
- Message count bucket
- LLM context size bucket
- Workspace paths
- Stale/large tool output counts
- Compaction decision signals

**Known issue with injection position**: Currently inserted before the last user message via `insertBeforeLastUserMessage`. In single-turn long-task scenarios, this position shifts on every turn, breaking the LLM provider's KV cache mechanism. In multi-turn conversations the position is fine — the earlier messages stay stable.

### 8.2 Telemetry Refresh Policy

`RuntimeMetaSnapshot` is cached and refreshed when:

- First time (empty cache)
- Token usage band changes
- Every `defaultRuntimeMetaHeartbeatTurns = 6` turns

Values are bucketized for privacy and to reduce cache invalidation:
- `tokens_band`: 0-20, 20-40, 40-60, 60-80, 80-100
- `messages_in_history_bucket`: 0-20, 20-50, 50-100, 100+
- `llm_context_size_bucket`: 0-1KB, 1-4KB, 4-16KB, 16KB+

## 9. Tool Output Processing

### 9.1 Normalization (`pkg/agent/tool_output.go`)

Every tool output is normalized before being added to `RecentMessages`:

- **Text output**: Truncated to 10,000 chars (head+tail preservation) if it exceeds the limit
- **Error patterns**: Detected and preserved with higher priority
- **Images**: Preserved completely

### 9.2 Stale Output Detection

For runtime telemetry (not for automatic action), stale tool outputs are counted:
- Tool results within the last 10 messages are "fresh"
- Beyond that: "stale" and reported as truncation candidates to the context management LLM

## 10. Token Estimation

`AgentContext.EstimateTokens()` uses:

1. Last assistant usage totals (from API response) if available
2. Plus heuristic estimation for trailing messages since last usage report

Per-message heuristic: `ceil(len(text) / 4)`, images ~1200 tokens each.

The `Compactor.CalculateDynamicThreshold()` computes:

```
threshold = contextWindow - systemPromptTokens - 3000(tool defs) - ReserveTokens(16384)
```

## 11. Key File Index

| File | Responsibility |
|------|---------------|
| `pkg/context/context.go` | AgentContext, core data types |
| `pkg/context/message.go` | AgentMessage, ContentBlock types |
| `pkg/context/agent_state.go` | AgentState tracking metadata |
| `pkg/context/snapshot.go` | ContextSnapshot for checkpoint persistence |
| `pkg/context/compactor.go` | Compactor interface, CompactionResult |
| `pkg/context/journal.go` | JournalEntry types (message/truncate/compact) |
| `pkg/context/checkpoint.go` | Checkpoint save/load, symlink management |
| `pkg/context/reconstruction.go` | Snapshot reconstruction from checkpoint + journal replay |
| `pkg/compact/compact.go` | Heavyweight Compactor (LLM summarization) |
| `pkg/compact/compact_tool.go` | Compact tool (exposed to context management LLM) |
| `pkg/compact/context_management.go` | ContextManager: LLM-driven context management cycle |
| `pkg/tools/context_mgmt/truncate_messages.go` | Truncate tool implementation |
| `pkg/tools/context_mgmt/update_llm_context.go` | LLM Context update tool |
| `pkg/tools/context_mgmt/no_action.go` | No-action tool |
| `pkg/tools/context_mgmt/registry.go` | Tool registry |
| `pkg/agent/loop.go` | Agent loop, compaction triggers, telemetry injection |
| `pkg/agent/conversion.go` | Message visibility filtering, LLM message conversion |
| `pkg/agent/tool_output.go` | Tool output normalization |
| `pkg/agent/checkpoint_manager.go` | Checkpoint lifecycle management |
| `pkg/session/compaction.go` | Session-level compaction cut-point logic |
| `cmd/ai/session_writer.go` | sessionCompactor: session-aware compaction wrapper |
| `pkg/prompt/context_management.md` | System prompt for context management LLM |
| `pkg/prompt/compact_system.md` | Summarization assistant system prompt |
| `pkg/prompt/compact_summarize.md` | Summarization template |
| `pkg/prompt/compact_update.md` | Incremental summary update template |

## 12. Design Principles

1. **LLM decides strategy, system decides timing** — The system controls *when* to trigger context management (token thresholds, intervals). The LLM controls *what* to do (truncate, update context, compact, or skip).

2. **LLM Context is the task state source of truth** — After compaction, `LLMContext` (maintained by the LLM via `update_llm_context`) is the only source of task continuity. Old messages are gone.

3. **Separation of LLMs** — Context management uses a completely separate LLM call with a dedicated system prompt. The main agent LLM is never involved in context management decisions.

4. **Append-only journal, periodic snapshots** — `messages.jsonl` is append-only. Checkpoints provide efficient recovery points. Reconstruction replays journal entries after checkpoint.

5. **Post-compact recovery** — After compaction, `PostCompactRecovery=true` causes the next LLM call to inject `LLMContext` content so the agent recovers task continuity immediately.

6. **Protected recent messages** — The last 5 messages are never truncated, ensuring the agent always has its most recent context.

7. **Telemetry, not directives** — Runtime state is injected as informational telemetry (not commands), keeping the main agent informed about context pressure without compelling it to act directly.