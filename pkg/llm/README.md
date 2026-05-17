# pkg/llm

LLM client abstraction with streaming support for OpenAI-compatible and Anthropic APIs.

## Overview

Provides a unified interface for streaming LLM completions. Routes to Anthropic or OpenAI based on the model's `API` field. All responses are delivered as push-based event streams.

## Core Types

### Model

```go
type Model struct {
    ID            string `json:"id"`            // e.g., "gpt-4o", "claude-sonnet-4-20250514"
    Provider      string `json:"provider"`      // e.g., "zai", "openai"
    BaseURL       string `json:"baseUrl"`       // API base URL
    API           string `json:"api"`           // "openai-completions" or "anthropic-messages"
    ContextWindow int    `json:"contextWindow"` // Token limit (0 = unknown)
    MaxTokens     int    `json:"maxTokens,omitempty"`
}
```

### LLMContext

```go
type LLMContext struct {
    SystemPrompt string       `json:"systemPrompt,omitempty"`
    Messages     []LLMMessage `json:"messages"`
    Tools        []LLMTool    `json:"tools,omitempty"`
}
```

### LLMMessage

```go
type LLMMessage struct {
    Role      string     `json:"role"` // "system", "user", "assistant", "tool"
    Content   string     `json:"content,omitempty"`
    ToolCalls []ToolCall `json:"toolCalls,omitempty"`
    Thinking  string    `json:"thinking,omitempty"` // For reasoning models
    // ...
}
```

### LLMTool / ToolCall / ToolCallFunction

```go
type LLMTool struct {
    Type     string   `json:"type"`     // "function"
    Function Function `json:"function"`
}

type ToolCall struct {
    ID       string          `json:"id,omitempty"`
    Type     string          `json:"type"`
    Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON string
}
```

## Streaming

### StreamLLM

```go
func StreamLLM(
    ctx context.Context,
    model Model,
    llmCtx LLMContext,
    apiKey string,
    chunkIntervalTimeout time.Duration,
) *EventStream[LLMEvent, LLMMessage]
```

Routes to the correct provider based on `model.API`:
- `"anthropic-messages"` → `StreamAnthropic()`
- All others → OpenAI-compatible SSE streaming

Returns an `EventStream` that emits `LLMEvent` values. The stream ends with either `LLMDoneEvent` (success) or `LLMErrorEvent` (failure).

### EventStream

```go
type EventStream[T any, R any] struct { ... }
```

Generic push-based stream. Consumers iterate via `stream.Events()` channel. The final result is available via `stream.Result()` after the events channel closes.

### LLMEvent (interface)

```go
type LLMEvent interface {
    GetEventType() string
}
```

Implementations:

| Type | Event | Description |
|------|-------|-------------|
| `LLMTextDeltaEvent` | `text_delta` | Text content chunk |
| `LLMThinkingDeltaEvent` | `thinking_delta` | Thinking/reasoning chunk |
| `LLMToolCallDeltaEvent` | `tool_call_delta` | Partial tool call |
| `LLMDoneEvent` | `done` | Stream complete with final message |
| `LLMErrorEvent` | `error` | Error during streaming |

### PartialMessage

Accumulates streaming deltas into a complete `LLMMessage`. Handles:
- Text content accumulation
- Thinking content accumulation
- Tool call merging by index (arguments are appended incrementally)

## Anthropic Support

`StreamAnthropic()` handles the Anthropic Messages API specifics:
- `x-api-key` header instead of `Bearer` token
- `anthropic-version` header
- Different SSE event format (`message_start`, `content_block_start`, `content_block_delta`, `message_delta`)
- Thinking/reasoning block support
- Token usage tracking

## Error Handling

The client handles:
- HTTP status codes (429 rate limit with `Retry-After`, 5xx server errors)
- Connection timeouts between chunks
- Premature connection close (no data chunks received)
- Truncated streams (chunks received but no finish_reason)

## Key Files

| File | Description |
|------|-------------|
| `client.go` | `StreamLLM()` — OpenAI-compatible streaming, SSE parsing, error handling |
| `types.go` | `Model`, `LLMContext`, `LLMMessage`, `EventStream`, `PartialMessage` |
| `anthropic.go` | `StreamAnthropic()` — Anthropic Messages API streaming |

## Dependencies

- `pkg/traceevent` — LLM call tracing