# Context Management

This document describes the current context management behavior in the codebase.
It is implementation-aligned with:

- `pkg/compact/compact.go`
- `pkg/agent/tool_summary.go`
- `pkg/agent/tool_output.go`
- `pkg/agent/message.go`
- `pkg/agent/conversion.go`
- `pkg/agent/agent.go`
- `pkg/agent/loop.go`

## Core Configuration

Context compaction is configured by `compact.Config` in `pkg/compact/compact.go`:

```go
type Config struct {
    MaxMessages         int
    MaxTokens           int
    KeepRecent          int
    KeepRecentTokens    int
    ReserveTokens       int
    ToolCallCutoff      int
    ToolSummaryStrategy string // llm, heuristic, off
    AutoCompact         bool
}
```

Default values:

```go
MaxMessages:         50
MaxTokens:           8000
KeepRecent:          5
KeepRecentTokens:    20000
ReserveTokens:       16384
ToolCallCutoff:      10
ToolSummaryStrategy: "llm"
AutoCompact:         true
```

## Triggering Rules

`Compactor.ShouldCompact()` follows this order:

1. If `AutoCompact == false`: do not compact.
2. If an effective token limit exists: compact when estimated tokens reach that limit.
3. Otherwise: fall back to `MaxMessages`.

Effective token limit is resolved by `Compactor.EffectiveTokenLimit()`:

1. `contextWindow - ReserveTokens` (when context window is set and positive)
2. else `MaxTokens`
3. else no token threshold

`KeepRecentTokens` is also clamped to at most half of the effective token limit.

## Message Visibility

`MessageMetadata` controls routing visibility:

```go
type MessageMetadata struct {
    AgentVisible *bool
    UserVisible  *bool
    Kind         string
}
```

Behavior:

- If visibility flags are unset, visibility defaults to `true`.
- Messages with `AgentVisible=false` are not sent to the LLM (`ConvertMessagesToLLM`) and are skipped by token estimation.

Important `Kind` values used by context management:

- `tool_result` (normal tool output)
- `tool_result_archived` (hidden old tool output)
- `tool_summary` (summary/digest message)

## Tool Output Processing (Per Tool Execution)

Tool results are normalized in `truncateToolContent()` before being stored.
This is called for both successful output and error output in `executeToolCalls()`.

`ToolOutputLimits` (`pkg/agent/tool_output.go`):

```go
type ToolOutputLimits struct {
    MaxLines             int
    MaxBytes             int
    MaxChars             int
    LargeOutputThreshold int
    TruncateMode         string // "head" or "head_tail"
}
```

Defaults:

```go
MaxLines:             2000
MaxBytes:             50 * 1024
MaxChars:             200 * 1024
LargeOutputThreshold: 200 * 1024
TruncateMode:         "head_tail"
```

Processing order:

1. If rune count exceeds `LargeOutputThreshold`, output is spilled to a temp file and replaced with a notice message:
   - Directory: `os.TempDir()/ai_tool_outputs`
   - Includes saved path and SHA256 checksum
2. Otherwise, truncation is applied by:
   - lines (with `head_tail` marker when enabled and limit >= 4)
   - then rune count
   - then byte count
3. When truncation happens, a stats notice is appended.

## Tool Result Summarization During Loops

After tool execution in each turn, `maybeSummarizeToolResults()` can summarize old tool results.

Trigger condition:

- visible `toolResult` count > `ToolCallCutoff`

Behavior:

- Repeats until visible tool results are at or below cutoff.
- Each iteration archives the oldest visible tool result:
  - `AgentVisible=false`
  - `UserVisible` preserved from the original message
  - `Kind="tool_result_archived"`
- Appends one summary message:
  - `Role="user"`
  - `AgentVisible=true`
  - `UserVisible=false`
  - `Kind="tool_summary"`

Strategies:

- `llm` (default): summarize with LLM
- `heuristic`: local fallback summary
- `off`: disabled

LLM summary limits:

- input clipped to `12000` chars
- output clipped to `1200` runes

Heuristic fallback output is clipped to `800` runes (head-tail style).

## Session Compaction

Compaction is implemented in `Compactor.Compact()`.

### Split logic

Messages are split into `oldMessages` + `recentMessages`:

- Prefer token-budget split when `KeepRecentTokens > 0`
- Otherwise split by `KeepRecent` message count

### Old message summary

`oldMessages` are summarized with an LLM prompt into a structured checkpoint.
Before summary generation:

- only `AgentVisible` messages are projected
- `toolResult` text is reduced to a max of `1800` runes for summary input

### Rebuild context

The resulting message list is:

1. one synthetic user message:
   - prefix: `[Previous conversation summary]`
2. `recentMessages` (after additional tool-result compaction in recent window)

Recent-window tool compaction (`compactToolResultsInRecent`):

- if visible recent tool results exceed cutoff, oldest excess entries are archived
- one digest message is appended with prefix `[Compaction tool digest]`
- digest message metadata:
  - `Role="user"`
  - `AgentVisible=true`
  - `UserVisible=false`
  - `Kind="tool_summary"`

## Automatic and Recovery Compaction Paths

There are two runtime compaction paths:

1. **Auto compaction after turns** (`Agent.tryAutoCompact`)
   - Runs after each `TurnEnd` event.
   - Uses `ShouldCompact()` thresholds.
2. **Context-limit recovery in loop** (`runInnerLoop`)
   - On `IsContextLengthExceeded` error, compacts and retries once (`maxCompactionRecoveries = 1`).

Both paths emit compaction start/end events with before/after counts and optional error.

## Token Estimation

`Compactor.EstimateContextTokens()` uses:

1. last assistant usage totals (if available and valid), plus
2. estimated tokens for trailing messages

Fallback is full heuristic estimation across visible messages.

Per-message heuristic (`estimateMessageTokens`):

- text/thinking/tool-call payload counted by character length
- images approximated as `~1200` tokens
- converted with `ceil(chars / 4)`

## Notes for Maintainers

- Treat this document as implementation documentation, not design intent.
- If behavior changes in `pkg/compact`, `pkg/agent/tool_summary.go`, or `pkg/agent/tool_output.go`, update this file in the same PR.
