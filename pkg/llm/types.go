package llm

import (
	"encoding/json"
	"strings"
	"sync"
)

// Model represents an LLM model configuration.
type Model struct {
	ID       string `json:"id"`       // e.g., "gpt-4", "gpt-3.5-turbo"
	Provider string `json:"provider"` // e.g., "zai", "openai"
	BaseURL  string `json:"baseUrl"`  // e.g., "https://api.openai.com/v1"
	API      string `json:"api"`      // e.g., "openai-completions"
}

// LLMContext represents the context for an LLM request.
type LLMContext struct {
	SystemPrompt string       `json:"systemPrompt,omitempty"`
	Messages     []LLMMessage `json:"messages"`
	Tools        []LLMTool    `json:"tools,omitempty"`
}

// LLMMessage represents a message in the LLM conversation.
type LLMMessage struct {
	Role         string        `json:"role"` // "system", "user", "assistant", "tool"
	Content      string        `json:"-"`    // Use custom marshaling
	ContentParts []ContentPart `json:"-"`    // Use custom marshaling
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
}

// MarshalJSON custom marshaling for LLMMessage to handle both Content and ContentParts
func (m LLMMessage) MarshalJSON() ([]byte, error) {
	// Build a map for JSON serialization
	type Alias LLMMessage
	tmp := struct {
		Content interface{} `json:"content,omitempty"`
		Alias
	}{
		Alias: (Alias)(m),
	}

	// If ContentParts is present and non-empty, use it
	if len(m.ContentParts) > 0 {
		tmp.Content = m.ContentParts
	} else {
		// Otherwise use Content string
		tmp.Content = m.Content
	}

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
	InputTokens  int `json:"prompt_tokens"`
	OutputTokens int `json:"completion_tokens"`
	TotalTokens  int `json:"total_tokens"`
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

	if len(pm.ToolCalls) > 0 {
		toolCalls := make([]ToolCall, 0, len(pm.ToolCalls))
		for i := 0; i < len(pm.ToolCalls); i++ {
			if tc, ok := pm.ToolCalls[i]; ok {
				toolCalls = append(toolCalls, *tc)
			}
		}
		msg.ToolCalls = toolCalls
	}

	return msg
}
