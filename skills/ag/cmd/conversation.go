package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/genius/ag/internal/conv"
	"github.com/genius/ag/internal/run"
)

// ConversationMessage represents a single message in a conversation.
type ConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Turn    int    `json:"turn,omitempty"`
}

// Conversation represents an entire conversation.
type Conversation struct {
	Messages []ConversationMessage `json:"messages"`
}

// GetConversation retrieves a structured conversation from an agent's run events.
func GetConversation(agentID string) (*Conversation, error) {
	runID, err := aiAdapter.getRunIDForAgent(agentID)
	if err != nil {
		return nil, fmt.Errorf("get run ID for agent %s: %w", agentID, err)
	}

	eventsPath, err := run.EventsPath(runID)
	if err != nil {
		return nil, fmt.Errorf("get events path: %w", err)
	}

	data, err := os.ReadFile(eventsPath)
	if err != nil {
		return nil, fmt.Errorf("read events.jsonl: %w", err)
	}

	// Parse events.jsonl into structured conversation using conv package
	msgs := conv.BuildConversation(data)
	result := &Conversation{Messages: make([]ConversationMessage, len(msgs))}
	for i, m := range msgs {
		result.Messages[i] = ConversationMessage{
			Role:    m.Role,
			Content: m.Content,
			Turn:    m.Turn,
		}
	}
	return result, nil
}

// FormatAsText formats the conversation as plain text.
func (c *Conversation) FormatAsText() string {
	var builder strings.Builder

	for _, msg := range c.Messages {
		if msg.Role == "user" {
			builder.WriteString(fmt.Sprintf("User %d:\n", msg.Turn))
		} else if msg.Role == "assistant" {
			builder.WriteString(fmt.Sprintf("Assistant %d:\n", msg.Turn))
		} else {
			builder.WriteString(fmt.Sprintf("%s:\n", msg.Role))
		}
		builder.WriteString(msg.Content)
		builder.WriteString("\n\n")
	}

	return builder.String()
}

// FormatAsMarkdown formats the conversation as Markdown.
func (c *Conversation) FormatAsMarkdown() string {
	var builder strings.Builder

	for _, msg := range c.Messages {
		if msg.Role == "user" {
			builder.WriteString(fmt.Sprintf("### User %d\n\n", msg.Turn))
		} else if msg.Role == "assistant" {
			builder.WriteString(fmt.Sprintf("### Assistant %d\n\n", msg.Turn))
		} else {
			builder.WriteString(fmt.Sprintf("### %s\n\n", msg.Role))
		}
		builder.WriteString(msg.Content)
		builder.WriteString("\n\n---\n\n")
	}

	return builder.String()
}

// GetLastAssistantResponse returns the last assistant response.
func (c *Conversation) GetLastAssistantResponse() string {
	return c.GetNthLastAssistantResponse(1)
}

// GetNthLastAssistantResponse returns the Nth-to-last assistant response (1=last).
func (c *Conversation) GetNthLastAssistantResponse(n int) string {
	count := 0
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == "assistant" {
			count++
			if count == n {
				return c.Messages[i].Content
			}
		}
	}
	return ""
}

// GetLastUserMessage returns the last user message.
func (c *Conversation) GetLastUserMessage() string {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == "user" {
			return c.Messages[i].Content
		}
	}
	return ""
}