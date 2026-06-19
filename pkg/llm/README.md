# pkg/llm

LLM client abstraction with streaming support for OpenAI-compatible and Anthropic APIs.

## Overview

Provides a unified interface for streaming LLM completions. Routes to Anthropic or OpenAI based on the model's `API` field. All responses are delivered as push-based event streams.

## Core Types

### Model

```go
type Model struct {
    ID            string `json:"id"`                       // e.g., "gpt-4o", "claude-sonnet-4-20250514"
    Provider      string `json:"provider"`                 // e.g., "zai", "openai"
    BaseURL       string `json:"baseUrl"`                  // API base URL
    API           string `json:"api"`                      // "openai-completions" or "anthropic-messages"
    ContextWindow int    `json:"contextWindow"`            // Token limit (0 = unknown)
    MaxTokens     int    `json:"maxTokens,omitempty"`
    Reasoning     bool   `json:"reasoning,omitempty"`      // Model supports thinking/reasoning control
}
```

### LLMContext

```go
type LLMContext struct {
    SystemPrompt  string       `json:"systemPrompt,omitempty"`
    Messages      []LLMMessage `json:"messages"`
    Tools         []LLMTool    `json:"tools,omitempty"`
    ThinkingLevel string       `json:"thinkingLevel,omitempty"` // off/minimal/low/medium/high/xhigh
}
```

### LLMMessage

```go
type LLMMessage struct {
    Role         string        `json:"role"`                  // "system", "user", "assistant", "tool"
    Content      string        `json:"-"`                     // Custom marshaling
    ContentParts []ContentPart `json:"-"`                     // Multimodal content (custom marshaling)
    Thinking     string        `json:"-"`                     // Serialized as reasoning_content
    ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
    ToolCallID   string        `json:"tool_call_id,omitempty"`
}
```

Custom `MarshalJSON`: serializes `Content` or `ContentParts` as `content`, and `Thinking` as `reasoning_content`.

### ContentPart

```go
type ContentPart struct {
    Type     string `json:"type"` // "text" or "image_url"
    Text     string `json:"text,omitempty"`
    ImageURL *struct {
        URL string `json:"url"`
    } `json:"image_url,omitempty"`
}
```

### LLMTool / ToolFunction

```go
type LLMTool struct {
    Type     string       `json:"type"` // "function"
    Function ToolFunction `json:"function"`
}

type ToolFunction struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    Parameters  map[string]any `json:"parameters"`
}
```

### ToolCall / FunctionCall

```go
type ToolCall struct {
    ID       string       `json:"id"`
    Type     string       `json:"type"` // "function"
    Function FunctionCall `json:"function"`
}

type FunctionCall struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON string
}
```

### Usage

```go
type Usage struct {
    InputTokens         int                  `json:"prompt_tokens"`
    OutputTokens        int                  `json:"completion_tokens"`
    TotalTokens         int                  `json:"total_tokens"`
    PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
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

Generic push-based stream. Consumers iterate via `stream.Iterator(ctx)` channel. The final result is available via `stream.Result()` channel after the events stream closes.

### LLMEvent (interface)

```go
type LLMEvent interface {
    GetEventType() string
}
```

Implementations:

| Type | Event | Description |
|------|-------|-------------|
| `LLMStartEvent` | `start` | Stream started, provides `PartialMessage` |
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

## Error Handling

Typed errors in `errors.go`:

```go
type APIError struct { ... }                   // Generic non-200 response
type ContextLengthExceededError struct { ... } // Token/context limit
type RateLimitError struct { ... }             // 429 throttling with Retry-After

func ClassifyAPIError(statusCode int, payload string) error
func IsContextLengthExceeded(err error) bool
func IsRateLimit(err error) bool
func IsRetryableError(err error) bool
func RetryAfter(err error) time.Duration
```

## Thinking / Reasoning

`thinking.go` provides `buildThinkingParams` which injects `reasoning_effort` and/or `thinking` object parameters into the request body for reasoning-capable models (ZAI, DeepSeek).

## Anthropic Support

`StreamAnthropic()` handles the Anthropic Messages API specifics:
- `Authorization: Bearer` header
- `anthropic-version` header
- Different SSE event format (`message_start`, `content_block_start`, `content_block_delta`, `message_delta`)
- Thinking/reasoning block support
- Token usage tracking

## Key Files

| File | Description |
|------|-------------|
| `client.go` | `StreamLLM()` — OpenAI-compatible streaming, SSE parsing, error handling |
| `types.go` | `Model`, `LLMContext`, `LLMMessage`, `ToolCall`, `LLMTool`, `Usage`, `LLMEvent` types, `PartialMessage` |
| `anthropic.go` | `StreamAnthropic()` — Anthropic Messages API streaming |
| `errors.go` | `APIError`, `ContextLengthExceededError`, `RateLimitError`, error classification |
| `eventstream.go` | `EventStream` — generic push-based async event stream |
| `thinking.go` | `buildThinkingParams` — reasoning/thinking parameter injection |

## Dependencies

- `pkg/traceevent` — LLM call tracing