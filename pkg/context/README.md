# pkg/context

Agent execution context: messages, tools, skills, and compaction state.

## Overview

`AgentContext` is the central state holder for a running agent. It contains the conversation history, available tools, loaded skills, and all metadata needed for LLM calls and compaction. Every agent loop iteration reads from and writes to this struct.

## Core Types

### AgentContext

```go
type AgentContext struct {
    SystemPrompt          string            // System prompt for LLM calls
    AgentContextPrefix    string            // Skills + AGENTS.md prefix (rebuilt at startup)
    Tools                 []Tool            // Available tools
    Skills                []skill.Skill     // Loaded skills
    RecentMessages        []AgentMessage    // Current conversation (not full history)
    AgentState            *AgentState       // System-maintained metadata
    LastCompactionSummary string            // For incremental summary updates
    OnMessagesChanged     func() error      // Callback when messages are modified
    // allowedTools — nil means all allowed, non-nil is a whitelist
}
```

Key methods:
- `AddMessage(msg)` — Append a message, call OnMessagesChanged
- `EstimateTokens()` — Estimate total token count
- `SetAllowedTools(names)` / `IsToolAllowed(name)` — Tool whitelist management
- `EstimateTokenPercent()` — Context usage as percentage

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
    Role       string           `json:"role"`      // "user", "assistant", "toolResult"
    Content    []ContentBlock   `json:"content"`
    Timestamp  int64            `json:"timestamp"`
    Metadata   *MessageMetadata `json:"metadata,omitempty"`
    Model      string           `json:"model,omitempty"`
    Usage      *Usage           `json:"usage,omitempty"`
    StopReason string           `json:"stopReason,omitempty"`
    ToolCallID string           `json:"toolCallId,omitempty"`
    ToolName   string           `json:"toolName,omitempty"`
    IsError    bool             `json:"isError,omitempty"`
    EntryID    string           `json:"entryId,omitempty"`
    // ... truncation tracking fields
}
```

### ContentBlock

Discriminated union (interface) with implementations: `TextContent` (type `"text"`), `ImageContent` (type `"image"`), `ToolCallContent` (type `"toolCall"`), `ThinkingContent` (type `"thinking"`).

### AgentState

System-maintained metadata tracking:
- Tool call counts (per name and total)
- Current turn number
- `ToolCallsSinceLastTrigger` — counter for compaction interval logic
- Runtime state

Persisted to `agent_state.json` in the session directory via `SaveAgentState` / `LoadAgentState`.

## Compaction Result

```go
type CompactionResult struct {
    Summary        string
    TokensBefore   int
    TokensAfter    int
    MessagesBefore int
    MessagesAfter  int
    Type           string           // "major" or "mini"
    TruncatedCount int              // Number of messages truncated (mini only)
    ExecutedTools  []ToolCallRecord // Tools actually executed during compaction
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

## AgentState Persistence

`AgentState` is persisted as `agent_state.json` directly in the session directory.
This file is written after compaction events and loaded on session resume.
Messages are NOT stored here — they come from `sess.GetMessages()`.

## Token Estimation

```go
func (c *AgentContext) EstimateTokens() int
func (c *AgentContext) EstimateToolsTokens() int
func (c *AgentContext) EstimateTokenPercent() float64
```

Estimates use a simple heuristic (~4 characters per token). Used by the compactor to decide when to act.

## Key Files

| File | Description |
|------|-------------|
| `context.go` | `AgentContext`, `Tool` interface, message management, token estimation, tool whitelist |
| `message.go` | `AgentMessage`, `ContentBlock`, `TextContent`, `ImageContent`, `ToolCallContent`, `ThinkingContent` |
| `agent_state.go` | `AgentState` tracking metadata |
| `compactor.go` | `Compactor` interface, `CompactionResult`, `ToolCallRecord` |
| `checkpoint_io.go` | `SaveAgentState` / `LoadAgentState`, `SplitLines` |
| `conversion.go` | `ConvertMessagesToLLM`, `ConvertToolsToLLM` — agent-to-LLM type conversion |
| `token_estimation.go` | `EstimateTokens()` standalone function |
| `constants.go` | Package constants (`RecentMessagesKeep`) |

## Dependencies

- `pkg/skill` — Skill type definition