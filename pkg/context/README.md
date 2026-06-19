# pkg/context

Agent execution context: messages, tools, skills, and compaction state.

## Overview

`AgentContext` is the central state holder for a running agent. It contains the conversation history, available tools, loaded skills, and all metadata needed for LLM calls and compaction. Every agent loop iteration reads from and writes to this struct.

## Core Types

### AgentContext

```go
type AgentContext struct {
    SystemPrompt          string            // System prompt for LLM calls
    Tools                 []Tool            // Available tools
    Skills                []skill.Skill     // Loaded skills
    RecentMessages        []AgentMessage    // Current conversation (not full history)
    AgentState            *AgentState       // System-maintained metadata
    LastCompactionSummary string            // For incremental summary updates
    // ... callbacks, tool whitelist
}
```

Key methods:
- `AddMessage(msg)` — Append a message, call OnMessagesChanged
- `GetMessages()` — Return recent messages
- `EstimateTokens()` — Estimate total token count
- `SetAllowedTools(names)` / `IsToolAllowed(name)` — Tool whitelist management
- `EstimateTokenPercent(windowSize)` — Context usage as percentage

### Tool

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any  // JSON Schema
    Execute(ctx, args) ([]ContentBlock, error)
}
```

Interface that all agent tools must implement.

### AgentMessage

```go
type AgentMessage struct {
    Role       string         `json:"role"`      // "user", "assistant", "tool"
    Content    []ContentBlock `json:"content"`
    ToolCalls  []ToolCall     `json:"toolCalls,omitempty"`
    ToolCallID string         `json:"toolCallId,omitempty"`
    // ... metadata fields (visibility, kind, timestamp, entry ID)
}
```

### ContentBlock

Discriminated union with types: `text`, `image`, `tool_use`, `tool_result`.

### AgentState

System-maintained metadata tracking:
- Tool call counts (per name and total)
- Current turn number
- `ToolCallsSinceLastTrigger` — counter for compaction interval logic
- Runtime state

## Compaction Result

```go
type CompactionResult struct {
    Summary        string
    TokensBefore   int
    TokensAfter    int
    MessagesBefore int
    MessagesAfter  int
    Type           string // "major"
}
```

Returned by compactors after performing compression.

## Compactor Interface

```go
type Compactor interface {
    ShouldCompact(ctx, agentCtx) bool
    Compact(ctx, agentCtx) (*CompactionResult, error)
    CalculateDynamicThreshold() int
}
```

Implemented by `pkg/compact.Compactor`. See [docs/context-management.md](../../docs/context-management.md) for the full compaction architecture.

## Journal

`Journal` provides append-only file I/O for incremental message logging, used by the checkpoint manager for crash recovery. Entry types: `message`, `truncate`, `compact`.

## Token Estimation

```go
func (c *AgentContext) EstimateTokens() int
func (c *AgentContext) EstimateToolsTokens() int
func (c *AgentContext) EstimateTokenPercent(windowSize int) float64
```

Estimates use a simple heuristic (~4 characters per token). Used by the compactor to decide when to act.

## Key Files

| File | Description |
|------|-------------|
| `context.go` | `AgentContext`, message management, token estimation, tool whitelist |
| `message.go` | `AgentMessage`, `ContentBlock` types |
| `agent_state.go` | `AgentState` tracking metadata |
| `compactor.go` | `Compactor` interface, `CompactionResult` |
| `journal.go` | `JournalEntry` types (message/truncate/compact) |
| `journal_io.go` | Journal I/O operations |
| `snapshot.go` | `ContextSnapshot` for checkpoint persistence |
| `checkpoint.go` | Checkpoint save/load, symlink management |
| `checkpoint_index.go` | Checkpoint index for fast lookup |
| `checkpoint_io.go` | Checkpoint I/O operations |
| `reconstruction.go` | Snapshot reconstruction from checkpoint + journal replay |

## Dependencies

- `pkg/skill` — Skill type definition