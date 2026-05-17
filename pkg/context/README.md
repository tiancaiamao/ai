# pkg/context

Agent execution context: messages, tools, skills, and compaction state.

## Overview

`AgentContext` is the central state holder for a running agent. It contains the conversation history, available tools, loaded skills, and all metadata needed for LLM calls and compaction. Every agent loop iteration reads from and writes to this struct.

## Core Types

### AgentContext

```go
type AgentContext struct {
    SystemPrompt         string
    Tools                []Tool
    Skills               []skill.Skill
    LLMContext           string            // Structured context content (managed by ContextManager)
    RecentMessages       []AgentMessage    // Current conversation (not full history)
    AgentState           *AgentState       // System-maintained metadata
    LastCompactionSummary string           // For incremental summary updates
    // ... callbacks, tool whitelist, context management lock
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
    Role      string         `json:"role"`      // "user", "assistant", "tool"
    Content   []ContentBlock `json:"content"`
    ToolCalls []ToolCall     `json:"toolCalls,omitempty"`
    ToolCallID string        `json:"toolCallId,omitempty"`
    // ... metadata fields
}
```

### ContentBlock

Discriminated union with types: `text`, `image`, `tool_use`, `tool_result`.

### AgentState

System-maintained metadata tracking:
- Tool call counts (per name and total)
- Current turn number
- Compaction history
- Runtime state

## Compaction Actions

```go
type CompactAction string

const (
    CompactActionTruncate         CompactAction = "truncate"           // Trim tool output
    CompactActionUpdateLLMContext CompactAction = "update_llm_context" // Update structured context
    CompactActionCompact          CompactAction = "compact"            // Major compaction
)
```

### CompactEventDetail

```go
type CompactEventDetail struct {
    Action CompactAction
    IDs    []string // Target message/tool-call IDs
}
```

Records individual compaction actions for session persistence and replay.

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

## Journal

`Journal` provides append-only file I/O for incremental message logging, used by the checkpoint manager for crash recovery.

## Token Estimation

```go
func (c *AgentContext) EstimateTokens() int
func (c *AgentContext) EstimateToolsTokens() int
func (c *AgentContext) EstimateTokenPercent(windowSize int) float64
```

Estimates use a simple heuristic (~4 characters per token). Used by both the compactor and context manager to decide when to act.

## Key Files

| File | Description |
|------|-------------|
| `context.go` | `AgentContext`, message management, token estimation, tool whitelist |
| `journal.go` | Append-only journal for checkpoint recovery |
| `checkpoint.go` | Checkpoint save/load for session persistence |

## Dependencies

- `pkg/skill` — Skill type definition