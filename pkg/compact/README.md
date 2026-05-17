# pkg/compact

LLM-driven context compaction with configurable strategies and context management.

## Overview

The `compact` package manages conversation context size within LLM window limits. It provides two complementary mechanisms:

1. **Compactor** — Heavyweight LLM summarization when context is too large
2. **ContextManager** — Lightweight LLM-driven decisions (truncate, update, compact) at periodic intervals

Both delegate decisions to the LLM rather than using deterministic rules, producing higher-quality context management.

## Compactor

### Config

```go
type Config struct {
    MaxMessages         int    // Compress when messages exceed this
    MaxTokens           int    // Compress when estimated tokens exceed this
    KeepRecent          int    // Always keep this many recent messages
    KeepRecentTokens    int    // Token budget for recent messages
    ReserveTokens       int    // Tokens to reserve from context window
    ToolCallCutoff      int    // Summarize tool outputs when visible results exceed this
    ToolSummaryStrategy string // "llm", "heuristic", or "off"
    AutoCompact         bool   // Enable automatic compaction
}
```

### Core Methods

```go
func (c *Compactor) Compress(ctx, agentCtx) (*CompactionResult, error)
func (c *Compactor) ShouldCompact(ctx, agentCtx) bool
```

`Compress` performs the compaction:
1. Splits messages into "old" and "recent" based on token budget
2. Summarizes old messages via LLM (initial summary or incremental update)
3. Returns a `CompactionResult` with the summary, before/after token counts

`ShouldCompact` checks if compaction is needed using a dynamic threshold:
- If `MaxTokens > 0`: triggers when estimated tokens exceed the limit
- If `AutoCompact` is false: never triggers

### Token Estimation

```go
func EstimateTokens(text string) int            // ~4 chars per token
func EstimateMessageTokens(msg AgentMessage) int // Per-message estimation
func EstimateMessagesTokens(msgs []AgentMessage) int
```

## ContextManager

Lightweight LLM-driven context management that runs periodically during agent execution.

### Trigger Thresholds

```go
const (
    MgmtTokenLow    = 0.20  // 20% of context: start periodic checks
    MgmtTokenMedium = 0.33  // 33%: more aggressive checks
    MgmtTokenHigh   = 0.50  // 50%: frequent checks
)
```

### Check Intervals

Token usage determines how often the context manager runs:

| Token Usage | Interval (tool calls) |
|-------------|----------------------|
| 20-33% | Every 15 calls |
| 33-50% | Every 10 calls |
| 50%+ | Every 7 calls |

### LLM-Driven Decisions

The context manager provides the LLM with tools to manage context:

| Tool | Action | Description |
|------|--------|-------------|
| `truncate_messages` | `truncate` | Trim long tool outputs in-place |
| `update_llm_context` | `update_llm_context` | Update structured LLM context |
| `compact_messages` | `compact` | Trigger major compaction |

The LLM analyzes the conversation and decides which action(s) to take. The system does **not** decide what to compact — it only decides **when** to ask.

### Compact Event Recording

Each context management action is recorded as a `compact_event` in the session:

```go
type CompactEventDetail struct {
    Action CompactAction  // "truncate", "update_llm_context", "compact"
    IDs    []string       // Target message/tool-call IDs
}
```

These events are replayed deterministically when loading sessions.

## Key Files

| File | Description |
|------|-------------|
| `compact.go` | `Compactor` — heavyweight summarization, token estimation, compression |
| `context_management.go` | `ContextManager` — lightweight periodic LLM-driven decisions |
| `compact_tool.go` | Tool implementations for context management actions |
| `compact_visibility_test.go` | Visibility tests for compaction |

## Dependencies

- `pkg/context` — `AgentContext`, `CompactionResult`, `CompactEventDetail`
- `pkg/llm` — LLM streaming for summarization calls
- `pkg/prompt` — Compaction system prompts
- `pkg/traceevent` — Tracing