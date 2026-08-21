package llm

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Model represents an LLM model configuration.
type Model struct {
	ID             string `json:"id"`            // e.g., "gpt-4", "gpt-3.5-turbo"
	Provider       string `json:"provider"`      // e.g., "zai", "openai"
	BaseURL        string `json:"baseUrl"`       // e.g., "https://api.openai.com/v1"
	API            string `json:"api"`           // e.g., "openai-completions"
	ContextWindow  int    `json:"contextWindow"` // e.g., 128000, 0 means unknown
	MaxTokens      int    `json:"maxTokens,omitempty"`
	Reasoning      bool   `json:"reasoning,omitempty"` // model supports thinking/reasoning control via API
	SupportsVision bool   `json:"-"`                   // model supports image input (from models.json "input")
}

// LLMContext represents the context for an LLM request.
type LLMContext struct {
	SystemPrompt  string       `json:"systemPrompt,omitempty"`
	Messages      []LLMMessage `json:"messages"`
	Tools         []LLMTool    `json:"tools,omitempty"`
	ThinkingLevel string       `json:"thinkingLevel,omitempty"` // normalized: off/minimal/low/medium/high/xhigh
}

// LLMMessage represents a message in the LLM conversation.
type LLMMessage struct {
	Role         string        `json:"role"` // "system", "user", "assistant", "tool"
	Content      string        `json:"-"`    // Use custom marshaling
	ContentParts []ContentPart `json:"-"`    // Use custom marshaling
	Thinking     string        `json:"-"`    // Thinking/reasoning content (separate from main content)
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
}

// MarshalJSON custom marshaling for LLMMessage to handle both Content and ContentParts.
// For reasoning models (e.g. DeepSeek), the Thinking field is serialized as
// "reasoning_content" at the same level as "content", so providers that require
// it can receive it in the format they expect. Providers that don't understand
// this field will simply ignore it.
func (m LLMMessage) MarshalJSON() ([]byte, error) {
	// Build a map for JSON serialization
	type Alias LLMMessage
	tmp := struct {
		Content          interface{} `json:"content,omitempty"`
		ReasoningContent string      `json:"reasoning_content,omitempty"`
		Alias
	}{
		Alias: (Alias)(m),
	}

	// If ContentParts is present and non-empty, use it
	if len(m.ContentParts) > 0 {
		if m.Content != "" {
			// Both text content and content parts exist (e.g. tool result with
			// text description + image). Merge them into a single array with
			// the text part first, then the image/content parts.
			parts := make([]ContentPart, 0, len(m.ContentParts)+1)
			parts = append(parts, ContentPart{Type: "text", Text: m.Content})
			parts = append(parts, m.ContentParts...)
			tmp.Content = parts
		} else {
			tmp.Content = m.ContentParts
		}
	} else {
		// Otherwise use Content string
		tmp.Content = m.Content
	}

	// Serialize thinking content as reasoning_content (required by DeepSeek
	// and other reasoning models for tool-call rounds).
	tmp.ReasoningContent = m.Thinking

	return json.Marshal(tmp)
}

// ContentPart represents a part of multimodal content.
type ContentPart struct {
	Type     string `json:"type"` // "text" or "image_url"
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

// ToolCall represents a tool call from the LLM.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// LLMTool represents a tool available to the LLM.
type LLMTool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction represents a tool function definition.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Usage represents token usage information.
type Usage struct {
	InputTokens         int                  `json:"prompt_tokens"`
	OutputTokens        int                  `json:"completion_tokens"`
	TotalTokens         int                  `json:"total_tokens"`
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// PromptTokensDetails holds the breakdown of prompt tokens, including cache hits.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// Timings holds llama.cpp-specific timing and cache statistics (from the "timings" field).
// This is an extension field not present in the standard OpenAI API, but returned by
// llama.cpp and other OpenAI-compatible servers to provide performance metrics.
type Timings struct {
	CacheN           int     `json:"cache_n"`             // Number of cached tokens
	PromptN          int     `json:"prompt_n"`            // Number of processed tokens
	PromptMS         float64 `json:"prompt_ms"`           // Prompt processing time in ms
	PromptPerTokenMS float64 `json:"prompt_per_token_ms"` // Average time per token
	PromptPerSecond  float64 `json:"prompt_per_second"`   // Tokens per second
	PredictedN       int     `json:"predicted_n"`         // Number of predicted tokens
	PredictedMS      float64 `json:"predicted_ms"`        // Prediction time in ms
	DraftN           int     `json:"draft_n"`             // Draft tokens count
	DraftNAccepted   int     `json:"draft_n_accepted"`    // Draft tokens accepted
}

// LLMEvent represents an event from the LLM stream.
type LLMEvent interface {
	GetEventType() string
}

// LLMStartEvent is emitted when the LLM starts generating.
type LLMStartEvent struct {
	Partial *PartialMessage
}

func (e LLMStartEvent) GetEventType() string { return "start" }

// LLMTextDeltaEvent is emitted for each text delta.
type LLMTextDeltaEvent struct {
	Delta string
	Index int
}

func (e LLMTextDeltaEvent) GetEventType() string { return "text_delta" }

// LLMThinkingDeltaEvent is emitted for thinking content deltas.
type LLMThinkingDeltaEvent struct {
	Delta string
	Index int
}

func (e LLMThinkingDeltaEvent) GetEventType() string { return "thinking_delta" }

// LLMToolCallDeltaEvent is emitted for tool call deltas.
type LLMToolCallDeltaEvent struct {
	Index    int
	ToolCall *ToolCall
}

func (e LLMToolCallDeltaEvent) GetEventType() string { return "tool_call_delta" }

// LLMDoneEvent is emitted when the LLM finishes.
type LLMDoneEvent struct {
	Message    *LLMMessage
	Usage      Usage
	StopReason string
	Timings    *Timings // llama.cpp timing extension (optional)
}

func (e LLMDoneEvent) GetEventType() string { return "done" }

// LLMErrorEvent is emitted on error.
type LLMErrorEvent struct {
	Error error
}

func (e LLMErrorEvent) GetEventType() string { return "error" }

// PartialMessage represents a message being built incrementally.
type PartialMessage struct {
	mu          sync.Mutex
	Role        string
	Content     strings.Builder
	Thinking    strings.Builder
	ToolCalls   map[int]*ToolCall
	CurrentTool *ToolCall
}

// NewPartialMessage creates a new partial message.
func NewPartialMessage() *PartialMessage {
	return &PartialMessage{
		Role:      "assistant",
		ToolCalls: make(map[int]*ToolCall),
	}
}

// AppendText appends text to the message content.
func (pm *PartialMessage) AppendText(delta string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.Content.WriteString(delta)
}

// AppendThinking appends thinking content to the message.
func (pm *PartialMessage) AppendThinking(delta string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.Thinking.WriteString(delta)
}

// AppendToolCall appends or updates a tool call.
func (pm *PartialMessage) AppendToolCall(index int, toolCall *ToolCall) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if existing, ok := pm.ToolCalls[index]; ok {
		// Merge with existing tool call
		if toolCall.ID != "" {
			existing.ID = toolCall.ID
		}
		if toolCall.Type != "" {
			existing.Type = toolCall.Type
		}
		if toolCall.Function.Name != "" {
			existing.Function.Name = toolCall.Function.Name
		}
		if toolCall.Function.Arguments != "" {
			existing.Function.Arguments += toolCall.Function.Arguments
		}
	} else {
		pm.ToolCalls[index] = toolCall
	}
}

// ToLLMMessage converts the partial message to an LLMMessage.
func (pm *PartialMessage) ToLLMMessage() LLMMessage {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	msg := LLMMessage{
		Role:    pm.Role,
		Content: pm.Content.String(),
	}

	// Include thinking content if present (for reasoning models)
	if pm.Thinking.Len() > 0 {
		msg.Thinking = pm.Thinking.String()
	}

	if len(pm.ToolCalls) > 0 {
		indices := make([]int, 0, len(pm.ToolCalls))
		for index := range pm.ToolCalls {
			indices = append(indices, index)
		}
		sort.Ints(indices)

		toolCalls := make([]ToolCall, 0, len(indices))
		for _, index := range indices {
			toolCalls = append(toolCalls, *pm.ToolCalls[index])
		}
		msg.ToolCalls = toolCalls
	}

	return msg
}
