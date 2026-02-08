package agent

import (
	"encoding/json"
	"strings"
	"time"
)

// ContentBlock represents a block of content in a message.
// Different content types implement this interface.
type ContentBlock interface {
	isContentBlock()
}

// TextContent represents plain text content.
type TextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (t TextContent) isContentBlock() {}

// ImageContent represents image content (base64 encoded).
type ImageContent struct {
	Type     string `json:"type"`
	Data     string `json:"data"` // base64 encoded
	MimeType string `json:"mimeType"`
}

func (i ImageContent) isContentBlock() {}

// ToolCallContent represents a tool call from the assistant.
type ToolCallContent struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (t ToolCallContent) isContentBlock() {}

// ThinkingContent represents thinking content (for reasoning models).
type ThinkingContent struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking"`
}

func (t ThinkingContent) isContentBlock() {}

// Usage represents token usage statistics.
type Usage struct {
	InputTokens  int  `json:"input"`
	OutputTokens int  `json:"output"`
	CacheRead    int  `json:"cacheRead"`
	CacheWrite   int  `json:"cacheWrite"`
	TotalTokens  int  `json:"totalTokens"`
	Cost         Cost `json:"cost"`
}

// Cost represents the cost breakdown.
type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// AgentMessage represents a message in the conversation.
type AgentMessage struct {
	// Common fields
	Role      string         `json:"role"` // "user", "assistant", "toolResult"
	Content   []ContentBlock `json:"content"`
	Timestamp int64          `json:"timestamp"`

	// AssistantMessage fields
	API        string `json:"api,omitempty"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Usage      *Usage `json:"usage,omitempty"`
	StopReason string `json:"stopReason,omitempty"`

	// ToolResultMessage fields
	ToolCallID string `json:"toolCallId,omitempty"`
	ToolName   string `json:"toolName,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
}

// NewUserMessage creates a new user message with text content.
func NewUserMessage(text string) AgentMessage {
	return AgentMessage{
		Role:      "user",
		Content:   []ContentBlock{TextContent{Type: "text", Text: text}},
		Timestamp: time.Now().UnixMilli(),
	}
}

// NewAssistantMessage creates a new assistant message placeholder.
func NewAssistantMessage() AgentMessage {
	return AgentMessage{
		Role:      "assistant",
		Content:   []ContentBlock{},
		Timestamp: time.Now().UnixMilli(),
	}
}

// NewToolResultMessage creates a new tool result message.
func NewToolResultMessage(toolCallID, toolName string, content []ContentBlock, isError bool) AgentMessage {
	return AgentMessage{
		Role:       "toolResult",
		Content:    content,
		Timestamp:  time.Now().UnixMilli(),
		ToolCallID: toolCallID,
		ToolName:   toolName,
		IsError:    isError,
	}
}

// ExtractText extracts all text content from a message.
func (m *AgentMessage) ExtractText() string {
	var b strings.Builder
	for _, block := range m.Content {
		if tc, ok := block.(TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// ExtractToolCalls extracts all tool calls from an assistant message.
func (m *AgentMessage) ExtractToolCalls() []ToolCallContent {
	calls := make([]ToolCallContent, 0)
	for _, block := range m.Content {
		if tc, ok := block.(ToolCallContent); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}

// UnmarshalJSON custom unmarshaling for AgentMessage to handle ContentBlock interface.
func (m *AgentMessage) UnmarshalJSON(data []byte) error {
	// Define a raw type for unmarshaling
	type rawMessage struct {
		Role       string            `json:"role"`
		Content    []json.RawMessage `json:"content"`
		Timestamp  int64             `json:"timestamp"`
		API        string            `json:"api,omitempty"`
		Provider   string            `json:"provider,omitempty"`
		Model      string            `json:"model,omitempty"`
		Usage      *Usage            `json:"usage,omitempty"`
		StopReason string            `json:"stopReason,omitempty"`
		ToolCallID string            `json:"toolCallId,omitempty"`
		ToolName   string            `json:"toolName,omitempty"`
		IsError    bool              `json:"isError,omitempty"`
	}

	var raw rawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Copy simple fields
	m.Role = raw.Role
	m.Timestamp = raw.Timestamp
	m.API = raw.API
	m.Provider = raw.Provider
	m.Model = raw.Model
	m.Usage = raw.Usage
	m.StopReason = raw.StopReason
	m.ToolCallID = raw.ToolCallID
	m.ToolName = raw.ToolName
	m.IsError = raw.IsError

	// Parse content blocks
	m.Content = make([]ContentBlock, 0, len(raw.Content))
	for _, rawBlock := range raw.Content {
		// Unmarshal into a map to check the "type" field
		var typeCheck struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(rawBlock, &typeCheck); err != nil {
			continue
		}

		// Unmarshal based on type
		switch typeCheck.Type {
		case "text":
			var tc TextContent
			if err := json.Unmarshal(rawBlock, &tc); err == nil {
				m.Content = append(m.Content, tc)
			}
		case "image":
			var ic ImageContent
			if err := json.Unmarshal(rawBlock, &ic); err == nil {
				m.Content = append(m.Content, ic)
			}
		case "toolCall":
			var tcc ToolCallContent
			if err := json.Unmarshal(rawBlock, &tcc); err == nil {
				m.Content = append(m.Content, tcc)
			}
		case "thinking":
			var thc ThinkingContent
			if err := json.Unmarshal(rawBlock, &thc); err == nil {
				m.Content = append(m.Content, thc)
			}
		}
	}

	return nil
}
