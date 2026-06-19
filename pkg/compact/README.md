# pkg/compact

LLM-driven context compaction with cache-friendly summarization.

## Overview

The `compact` package manages conversation context size within LLM window limits. It provides a single `Compactor` that handles both the compaction decision and execution.

### Compaction Decision: LLMDecide Mode

When `LLMDecideConfig` is set, the compactor uses a tiered threshold system:

1. **Hard limit**: At or above → compact immediately
2. **Soft threshold**: Below → skip (not enough pressure)
3. **Tiered ask**: Between soft and hard → ask LLM "compact now?" at intervals

The LLM ask is a cache-friendly request that mirrors a normal agent turn prefix, maximizing provider prefix-cache hits.

### Compaction Execution

`Compact()` performs:

1. Split messages into "old" (summarize) and "recent" (keep) by token budget or count
2. Generate LLM summary of old messages (with previous summary for incremental update)
3. Fix tool-call/result pairing across the split boundary
4. Archive excess visible tool results (beyond `ToolCallCutoff`)
5. Clean stale runtime_state messages
6. Return `CompactionResult` with before/after token counts

## Config

```go
type Config struct {
    MaxMessages         int    // Compress when messages exceed this
    MaxTokens           int    // Compress when estimated tokens exceed this
    KeepRecent          int    // Always keep this many recent messages
    KeepRecentTokens    int    // Token budget for recent messages
    ReserveTokens       int    // Tokens to reserve from context window
    ToolCallCutoff      int    // Archive tool results when visible count exceeds this
    ToolSummaryStrategy string // "llm", "heuristic", or "off"
    AutoCompact         bool   // Enable automatic compaction
    GracePeriod         int    // Protect N most recent tool results from archiving
    LLMDecide           *LLMDecideConfig // Enable LLM-decides mode
}

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

## Core Methods

```go
func (c *Compactor) ShouldCompact(ctx, agentCtx) bool
func (c *Compactor) Compact(ctx, agentCtx) (*CompactionResult, error)
```

`ShouldCompact`:
- If `LLMDecide` is set: tiered threshold + LLM yes/no gate (`shouldCompactLLMDecide`)
- Otherwise: dynamic threshold based on `MaxTokens` or `MaxMessages`

`Compact`:
1. Splits messages by token budget (`splitMessagesByTokenBudget`) or count
2. Summarizes old messages via LLM (`GenerateSummaryWithPrevious`)
3. Fixes tool-call/result pairing (`ensureToolCallPairing` / `ensureToolCallPairingWithGrace`)
4. Compacts excess tool results (`compactToolResultsInRecent`)
5. Cleans stale runtime_state (`cleanOldRuntimeState`)
6. Updates `AgentContext` in place

### Token Estimation

```go
func EstimateTokens(text string) int            // ~4 chars per token
func EstimateMessageTokens(msg AgentMessage) int // Per-message estimation
```

## Cache-Friendly Design

Both `askLLM` and `GenerateSummaryWithPrevious` build requests whose prefix matches a normal agent turn:

```
[system_prompt]           (cached)
[contextPrefix as user]   ← skills + AGENTS.md (cached)
[...conversation messages...] (cached)
[trailing instruction]    ← only this is new
```

This maximizes provider prefix-cache hits, reducing latency and cost.

## Key Files

| File | Description |
|------|-------------|
| `compact.go` | `Compactor` — `ShouldCompact`, `Compact`, `askLLM`, LLMDecide logic |
| `compact_summary.go` | Summary generation, message splitting (`splitMessagesByTokenBudget`) |
| `compact_tools.go` | Tool-call pairing, tool result compaction |

## Dependencies

- `pkg/context` — `AgentContext`, `CompactionResult`, `AgentMessage`
- `pkg/llm` — LLM streaming for summarization and yes/no asks
- `pkg/prompt` — Compaction prompts, LLM-decide check prompt
- `pkg/traceevent` — Tracing