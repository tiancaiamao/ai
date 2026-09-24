# pkg/compact

LLM-driven context compaction with cache-friendly summarization.

## Overview

The `compact` package manages conversation context size within LLM window limits. It provides a single `Compactor` that handles both the compaction decision and execution.

### Compaction Decision: LLMDecide Mode

The compactor automatically uses LLM-decides compaction with thresholds selected from the model context window. `LLMDecideConfig` is internal and is not user-configurable.

1. **Hard limit**: At or above → compact immediately
2. **Soft threshold**: Below → skip (not enough pressure)
3. **Tiered ask**: Between soft and hard → ask LLM "compact now?" at intervals

The LLM ask is a cache-friendly request that mirrors a normal agent turn prefix, maximizing provider prefix-cache hits.

### Context Retention Check (Canary)

The `askLLM` call also performs a context retention check when enabled:

1. On the first `askLLM` call, a random canary value (e.g. `<agent:canary value="a1b2c3d4e5f6"/>`) is **appended** to `RecentMessages` as an agent-visible user message. The expected value is stored in `Compactor.canaryValue`.
2. On subsequent `askLLM` calls, the LLM is asked to report the canary value from the conversation.
3. If the LLM answers correctly → proceed with normal confirm/reject logic.
4. If the LLM answers incorrectly → context is likely degraded → **force compaction** (overrides LLM decision).
5. After each `askLLM` call, old canary messages are cleaned and a new canary is appended for the next round.

The canary is appended (never inserted mid-list), so the provider prefix-cache for earlier messages is unaffected. As new tool call/result messages accumulate, the canary naturally sinks to the "lost in the middle" zone.

When compaction triggers (`Compact`), all canary messages are removed and `canaryValue` is reset, starting a fresh cycle on the next `askLLM`.

### Compaction Execution

`Compact()` performs:

1. Split messages into "old" (summarize) and "recent" (keep) by token budget or count
2. Generate LLM summary of old messages (with previous summary for incremental update)
3. Fix tool-call/result pairing across the split boundary
4. Archive old messages to `compactions/archived_*.jsonl` (pages of the session); the summary is followed by a `<critical>` note that steers the agent to the `ai history` CLI (`windows`/`search`/`read`) with the run ID inlined (`SetRunID`) so compacted history stays retrievable without raw file access
5. Archive excess visible tool results (beyond `ToolCallCutoff`)
6. Clean stale runtime_state messages
7. Return `CompactionResult` with before/after token counts

## Config

```go
type Config struct {
    MaxTokens             int              // Compress when estimated tokens exceed this
    KeepRecentTokens      int              // Token budget for recent messages
    ReserveTokens         int              // Tokens to reserve from context window
    ToolCallCutoff        int              // Archive tool results when visible count exceeds this
    ToolSummaryAutomation string           // "off", "fallback", or "always"
    AutoCompact           bool             // Enable automatic compaction
    GracePeriod           int              // Protect N most recent tool results from archiving
    LLMDecide             *LLMDecideConfig // Internal; auto-configured from model context window
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

The compactor automatically selects thresholds from the model context window via `DefaultLLMDecideConfig(contextWindow)`. `LLMDecideConfig` is internal and is not user-configurable or read from `config.json`.

## Core Methods

```go
func (c *Compactor) ShouldCompact(ctx, agentCtx) bool
func (c *Compactor) Compact(ctx, agentCtx) (*CompactionResult, error)
```

`ShouldCompact`:
- Tiered threshold + LLM yes/no gate (`shouldCompactLLMDecide`)

`Compact`:
1. Splits messages by token budget (`splitMessagesByTokenBudget`)
2. Summarizes old messages via LLM (`GenerateSummary`)
3. Fixes tool-call/result pairing (`ensureToolCallPairing` / `ensureToolCallPairingWithGrace`)
4. Compacts excess tool results (`compactToolResultsInRecent`)
5. Cleans stale runtime_state (`cleanOldRuntimeState`)
6. Updates `AgentContext` in place

### Token Estimation

```go
func EstimateMessageTokens(msg AgentMessage) int  // Per-message estimation
func estimateMessageTokens(msg AgentMessage) int  // Unexported internal helper
```

Note: The standalone `EstimateTokens()` function lives in `pkg/context`.

## Cache-Friendly Design

Both `askLLM` and `GenerateSummary` build requests whose prefix matches a normal agent turn:

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
| `canary.go` | Canary context retention check — `AppendCanary`, `FindCanaryValue`, `RemoveAllCanaries` |

## Dependencies

- `pkg/context` — `AgentContext`, `CompactionResult`, `AgentMessage`
- `pkg/llm` — LLM streaming for summarization and yes/no asks
- `pkg/prompt` — Compaction prompts, LLM-decide check prompt
- `pkg/traceevent` — Tracing