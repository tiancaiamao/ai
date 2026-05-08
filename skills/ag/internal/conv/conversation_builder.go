package conv

import (
	"encoding/json"
	"strings"
)

// Message represents a single message in a conversation.
type Message struct {
	Role    string // "user", "assistant", "system"
	Content string
	Turn    int
}

// ConversationBuilder accumulates streaming events into structured messages.
// It handles text_delta accumulation, turn tracking, and message boundary
// detection, replacing the manual map[string]any parsing that was duplicated
// across cmd/agent_client.go and cmd/conversation.go.
type ConversationBuilder struct {
	messages   []Message
	curText    strings.Builder
	curRole    string
	curTurn    int
	lastTurn   int
	inStream   bool // true if we're accumulating text_delta chunks
}

// NewConversationBuilder creates a builder ready to process events.
func NewConversationBuilder() *ConversationBuilder {
	return &ConversationBuilder{}
}

// Messages returns the accumulated messages.
func (b *ConversationBuilder) Messages() []Message {
	return b.messages
}

// AssistantTexts returns all assistant message contents as a flat slice.
func (b *ConversationBuilder) AssistantTexts() []string {
	out := make([]string, 0, len(b.messages))
	for _, m := range b.messages {
		if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
			out = append(out, strings.TrimSpace(m.Content))
		}
	}
	return out
}

// flushCurrent saves the current accumulated text as a message if non-empty.
func (b *ConversationBuilder) flushCurrent() {
	if b.curText.Len() == 0 {
		return
	}
	content := strings.TrimSpace(b.curText.String())
	if content == "" {
		b.curText.Reset()
		b.inStream = false
		return
	}
	b.messages = append(b.messages, Message{
		Role:    b.curRole,
		Content: content,
		Turn:    b.curTurn,
	})
	b.curText.Reset()
	b.inStream = false
}

// ProcessEvent handles a single raw JSON event line.
// It uses FormattedEvent for metadata and falls back to raw map parsing
// for content extraction (since ParseEvent strips content details).
func (b *ConversationBuilder) ProcessEvent(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}

	var evt map[string]any
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return
	}

	etype, _ := evt["type"].(string)
	switch etype {
	case "message_start", "message_update":
		b.handleMessageEvent(evt)
	case "message_end":
		b.flushCurrent()
	case "turn_start":
		if b.curRole == "assistant" && b.curTurn == 0 {
			b.curTurn = b.lastTurn + 1
		}
	case "turn_end":
		if b.inStream {
			b.flushCurrent()
		}
	case "agent_end":
		b.handleAgentEnd(evt)
	}
}

// handleMessageEvent processes message_start and message_update events.
func (b *ConversationBuilder) handleMessageEvent(evt map[string]any) {
	// Check for role in top-level message field
	msg, _ := evt["message"].(map[string]any)
	if msg != nil {
		if role, ok := msg["role"].(string); ok {
			if role != b.curRole && b.curText.Len() > 0 {
				b.flushCurrent()
			}
			b.curRole = role
			if role == "user" && b.curTurn > 0 {
				b.lastTurn = b.curTurn
				b.curTurn = 0
			}
			// Extract content from message field
			b.extractContent(msg["content"])
		}
	}

	// Check for assistantMessageEvent (streaming delta)
	assistantEvent, _ := evt["assistantMessageEvent"].(map[string]any)
	if assistantEvent != nil {
		b.curRole = "assistant"
		evType, _ := assistantEvent["type"].(string)
		if evType == "text_delta" {
			delta, _ := assistantEvent["delta"].(string)
			b.curText.WriteString(delta)
			b.inStream = true
		}
	}
}

// extractContent extracts text from a content array, overwriting (not appending)
// to avoid duplication from streaming deltas.
func (b *ConversationBuilder) extractContent(raw any) {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return
	}
	var text strings.Builder
	for _, item := range items {
		obj, _ := item.(map[string]any)
		if obj == nil {
			continue
		}
		typ, _ := obj["type"].(string)
		if typ == "text" {
			t, _ := obj["text"].(string)
			text.WriteString(t)
		}
	}
	if text.Len() > 0 {
		b.curText.Reset()
		b.curText.WriteString(text.String())
		b.inStream = true
	}
}

// handleAgentEnd extracts assistant messages from the agent_end messages array.
func (b *ConversationBuilder) handleAgentEnd(evt map[string]any) {
	b.flushCurrent()
	rawMessages, _ := evt["messages"].([]any)
	if len(rawMessages) == 0 {
		return
	}
	for _, raw := range rawMessages {
		msg, _ := raw.(map[string]any)
		if msg == nil {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "assistant" {
			continue
		}
		content := extractText(msg["content"])
		if content != "" {
			b.messages = append(b.messages, Message{
				Role:    "assistant",
				Content: content,
				Turn:    b.curTurn,
			})
		}
	}
}

// extractText extracts plain text from a content array.
func extractText(raw any) string {
	items, _ := raw.([]any)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	for _, item := range items {
		obj, _ := item.(map[string]any)
		if obj == nil {
			continue
		}
		if typ, _ := obj["type"].(string); typ == "text" {
			text, _ := obj["text"].(string)
			b.WriteString(text)
		}
	}
	return strings.TrimSpace(b.String())
}

// BuildConversation processes all events in a byte slice and returns messages.
// This is a convenience function for one-shot parsing.
func BuildConversation(data []byte) []Message {
	builder := NewConversationBuilder()
	for _, line := range strings.Split(string(data), "\n") {
		builder.ProcessEvent(line)
	}
	return builder.Messages()
}

// BuildAssistantTexts processes all events and returns assistant message texts.
// This is a convenience function for one-shot parsing.
func BuildAssistantTexts(data []byte) []string {
	builder := NewConversationBuilder()
	for _, line := range strings.Split(string(data), "\n") {
		builder.ProcessEvent(line)
	}
	return builder.AssistantTexts()
}