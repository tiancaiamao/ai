# Context Management Design

This document describes how the agent manages its conversation context to stay within LLM window limits while preserving task continuity.

It is implementation-aligned with the current codebase. If behavior changes, update this document in the same PR.

## 1. Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                        Agent Loop                            │
│  (pkg/agent/loop.go, loop_state.go)                         │
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
│  │         sessionCompactor (thin wrapper)                  │ │
│  │         cmd/ai/session_writer.go                         │ │
│  │         delegates to ↓                                   │ │
│  │                                                          │ │
│  │         compact.Compactor                                │ │
│  │         pkg/compact/compact.go                           │ │
│  │                                                          │ │
│  │         • ShouldCompact: LLMDecide threshold check       │ │
│  │         • Compact: LLM summarization + tool pairing      │ │
│  └─────────────────────────────────────────────────────────┘ │
│                           │                                   │
│                           ▼                                   │
│  ┌─────────────────────────────────────────────────────────┐ │
│  │         AgentContext (in-memory state)                    │ │
│  │         pkg/context/context.go                           │ │
│  │                                                          │ │
│  │  RecentMessages: []AgentMessage                          │ │
│  │  AgentState: *AgentState (system metadata)               │ │
│  │  SystemPrompt, Tools, ...                                │ │
│  └─────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

The `sessionCompactor` is a thin thread-safe wrapper holding a `*compact.Compactor` reference, allowing model/session swaps without rebuilding agent config.

## 2. Compaction Decision: LLMDecide Mode

**File:** `pkg/compact/compact.go` — `shouldCompactLLMDecide()`

When the `LLMDecide` config is set, the compactor uses a tiered threshold system instead of a single hard limit:

### Config (`LLMDecideConfig`)

```go
type LLMDecideConfig struct {
    SoftThreshold  int  // Below this: never compact
    HardLimit      int  // At or above: compact immediately (no LLM ask)
    TierMedium     int  // Token count for "medium" tier
    TierHigh       int  // Token count for "high" tier
    IntervalLow    int  // Tool calls between asks (low tier)
    IntervalMedium int  // Tool calls between asks (medium tier)
    IntervalHigh   int  // Tool calls between asks (high tier)
}
```

`DefaultLLMDecideConfig(contextWindow)` returns tuned thresholds for the given context window size.

### Decision Flow

```
tokens >= HardLimit?           → YES: compact immediately
tokens < SoftThreshold?         → YES: skip (not enough context pressure)
                                 NO:  enter tiered LLM-ask flow ↓

Determine tier (low/medium/high) from token count
Check interval: enough tool calls elapsed since last ask?
  NO  → skip (don't re-ask too frequently)
  YES → ask LLM: "compact now?" (askLLM)

LLM says YES → compact
LLM says NO  → skip, wait for next interval
LLM error    → compact (safe fallback)
```

### `askLLM` — Lightweight Yes/No Gate

**File:** `pkg/compact/compact.go` — `askLLM()`

The LLM ask is a cache-friendly request that mirrors a normal agent turn:

```
[system_prompt]           (cached)
[contextPrefix as user]   ← skills + AGENTS.md (cached)
[...conversation messages...] (cached)
[trailing question]       ← "You are at X% of compaction limit... Reply ONLY 'yes' or 'no'."
```

Since all but the trailing question is a prefix of the normal conversation, provider prefix-cache hits are maximized.

#### Context Retention Check (Canary)

The `askLLM` call also runs a context retention check using a canary value:

1. **Planting**: On each `askLLM` call, an agent-visible `<agent:canary value="..."/>` message is **appended** to `RecentMessages` (after cleaning old canaries). The expected value is stored in `Compactor.canaryValue`.
2. **Recall test**: On the next `askLLM` call, the LLM is asked to report the canary value from the conversation. The canary message still exists in `RecentMessages`, having naturally sunk toward the middle as new tool call/result messages accumulated.
3. **Result**: If the LLM answers correctly → continue with normal confirm/reject logic. If incorrectly → context is degraded → **force compaction** (overrides LLM decision).
4. **Cache impact**: The canary is appended (never inserted mid-list), so provider prefix-cache for earlier messages is unaffected.
5. **Compaction**: `Compact()` calls `RemoveAllCanaries()` to clean canary messages, and resets `canaryValue` to start a fresh cycle.

### Idempotency

The LLM decision is cached per tool-call counter value (`ToolCallsSinceLastTrigger`). This makes `ShouldCompact` safe to call multiple times per turn. The cache is cleared after a successful `Compact()`.

## 3. Compaction Execution

**File:** `pkg/compact/compact.go` — `Compact()`

### Flow

1. **Split messages**: Divide `RecentMessages` into `oldMessages` (to summarize) and `recentMessages` (to keep intact), using a token budget (`KeepRecentTokens`). If all messages fit within the budget but the message count exceeds 50, a forced split keeps the last 30%.
2. **Generate summary**: LLM summarizes `oldMessages` (which may include the previous compaction summary message)
3. **Fix tool-call pairing**: Ensure `tool_call` / `tool_result` pairs are not split across the boundary
4. **Compact tool results**: If visible tool results exceed `ToolCallCutoff`, hide oldest ones from agent (keep visible to user)
5. **Clean runtime state**: Remove stale `runtime_state` messages (keep only the latest)
6. **Update context**: Replace `RecentMessages` with `[compactionSummary, ...recentMessages]`

### Token Budget Split

**File:** `pkg/compact/compact_summary.go` — `splitMessagesByTokenBudget()`

When `KeepRecentTokens > 0`: walks backwards from the latest message, accumulating token estimates until budget is reached. Compaction summary messages are always included in the "recent" set.

**Force-split fallback**: If token estimation says all messages fit within budget but message count > 50, a forced 30/70 split is applied (estimation is a rough `chars/4` heuristic).

### Tool-Call Pairing

**File:** `pkg/compact/compact_tools.go`

After splitting, some `tool_result` messages in `recentMessages` may have their corresponding `tool_call` in `oldMessages` (or vice versa). Unpaired tool results cause API errors.

- `ensureToolCallPairing`: archive orphaned tool results (set `agentVisible=false`)
- `ensureToolCallPairingWithGrace`: same, but protects the N most recent tool results (grace period) from archiving
- Empty assistant messages (all tool calls stripped) are hidden entirely

### Tool Result Compaction

**File:** `pkg/compact/compact_tools.go` — `compactToolResultsInRecent()`

When visible tool results exceed `ToolCallCutoff` (default 10):
1. Excess tool results are set `agentVisible=false` (archived)
2. Corresponding `tool_call` blocks are removed from assistant messages
3. This keeps the agent's context lean while preserving full history for the user

## 4. Summary Generation

**File:** `pkg/compact/compact_summary.go` — `GenerateSummary()`

Generates an LLM summary of old messages. Cache-friendly request structure (same as `askLLM`):
- System prompt + tools + context prefix + old messages + summarization instruction
- Only the trailing instruction is new; everything else is a prefix of the original conversation
- The previous compaction summary (if any) is included in `oldMessages`, so the LLM can see it without a separate prompt

**Fallback**: If the summary text is empty but `reasoning_content` (thinking) is non-empty, falls back to using thinking output (some models put everything in thinking).

**Retry**: Up to 3 attempts with exponential backoff on retryable errors.

## 5. Token Estimation

**File:** `pkg/context/context.go` — `EstimateTokens()`

```
tokens = lastAPIUsageTotalTokens (if available)
       + heuristicEstimate(trailing messages since last usage)
```

Per-message heuristic: `ceil(visible_chars / 4)`, images ~1200 tokens each.

**File:** `pkg/compact/compact.go` — `EstimateMessageTokens()`

Same `chars/4` heuristic for individual messages, used by the split logic.

## 6. Session Persistence

**File:** `pkg/session/session.go` — `AppendCompaction()`

Compaction is persisted as a session entry (not inline rewrite):

1. Post-compaction messages are saved to `compactions/compaction_NNNNN.jsonl` (snapshot file)
2. A `compaction` entry is appended to `messages.jsonl` with `snapshotRef` pointing to the snapshot file
3. `messages.jsonl` is never rewritten — it's append-only

On session reload, the loader follows `snapshotRef` to restore the post-compaction message set.

See [session-format.md](./session-format.md) for the full session format specification.

## 7. Compaction Flow in the Agent Loop

**File:** `pkg/agent/loop_state.go` — `performCompaction()`

```
1. Pre-LLM check: ShouldCompact() returns true
   → Call Compact()

2. Context-limit recovery: API returns context-length error
   → Force Compact() (max once per session)
```

After successful compaction:
- `AgentState.ToolCallsSinceLastTrigger` is reset to 0
- `llmDecideAnswer` cache is cleared
- Compaction result is recorded in trace events

## 8. Tool Output Management

### Tool Output Normalization

**File:** `pkg/agent/tool_output.go`

Every tool output is normalized before being added to context:
- **Text output**: Truncated to 10,000 chars (head+tail preservation)
- **Error patterns**: Detected and preserved with higher priority
- **Images**: Preserved completely

### Tool Call Cutoff

During compaction, when the number of visible tool results exceeds `ToolCallCutoff` (default 10), the oldest tool outputs are archived (hidden from agent, visible to user). This prevents context bloat from accumulated tool results.

## 9. Key File Index

| File | Responsibility |
|------|---------------|
| `pkg/compact/compact.go` | `Compactor` — `ShouldCompact`, `Compact`, `askLLM`, LLMDecide logic |
| `pkg/compact/compact_summary.go` | Summary generation (`GenerateSummary`), message splitting |
| `pkg/compact/compact_tools.go` | Tool-call pairing, tool result compaction |
| `pkg/compact/canary.go` | Canary context retention check — `AppendCanary`, `FindCanaryValue`, `RemoveAllCanaries` |
| `pkg/context/context.go` | `AgentContext`, `EstimateTokens` |
| `pkg/context/message.go` | `AgentMessage`, `ContentBlock` types |
| `pkg/context/agent_state.go` | `AgentState` tracking metadata |
| `pkg/context/compactor.go` | `Compactor` interface, `CompactionResult` |
| `pkg/context/checkpoint_io.go` | `SaveAgentState` / `LoadAgentState` |
| `pkg/agent/loop.go` | Agent loop, compaction trigger orchestration |
| `pkg/agent/loop_state.go` | `performCompaction`, pre-LLM + recovery paths |
| `pkg/agent/tool_output.go` | Tool output normalization |
| `pkg/agent/executor.go` | Tool execution with concurrency control |
| `pkg/agent/checkpoint_manager.go` | AgentState persistence lifecycle |
| `pkg/session/session.go` | Session persistence, `AppendCompaction` |
| `pkg/session/entries.go` | `SessionEntry`, `SessionHeader`, entry types |
| `cmd/ai/session_writer.go` | `sessionCompactor`: thread-safe compactor wrapper |
| `pkg/prompt/builder.go` | System prompt construction |
| `pkg/prompt/compact_check.md` | LLM ask prompt template |
| `pkg/prompt/compact_summarize.md` | Summarization prompt (initial) |
| `pkg/prompt/compact_summarize.md` | Summarization prompt |

## 10. Design Principles

1. **System controls timing, LLM controls decision** — The system decides *when* to ask (tiered thresholds + intervals). The LLM decides *whether* to compact (yes/no gate). This replaces deterministic single-threshold rules with adaptive, context-aware decisions.

2. **Cache-friendly LLM asks** — Both `askLLM` and `GenerateSummary` build requests whose prefix matches a normal agent turn, maximizing provider prefix-cache hits. Only the trailing instruction is new.

3. **Append-only session log** — `messages.jsonl` is never rewritten. Compaction saves post-compaction messages to a snapshot file and appends a `compaction` entry referencing it.

4. **Protected recent messages** — The last N messages (by token budget or count) are always preserved in full. Only older messages are summarized.

5. **Tool-call pairing integrity** — After compaction, `tool_call` / `tool_result` pairs are never split. Orphaned results are archived, empty assistant shells are hidden.

6. **Graceful degradation** — On LLM ask failure, the system compacts as a safe fallback rather than letting context overflow.