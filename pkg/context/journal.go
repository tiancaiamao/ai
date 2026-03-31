package context

import (
	"time"
)

// EntryType represents the type of journal entry.
type EntryType string

const (
	// EntryTypeMessage represents a message entry.
	EntryTypeMessage EntryType = "message"

	// EntryTypeTruncate represents a truncation event entry.
	EntryTypeTruncate EntryType = "truncate"
)

// JournalEntry represents a line in messages.jsonl
type JournalEntry struct {
	Type     string        `json:"type"` // "message" | "truncate"
	Message  *AgentMessage `json:"message,omitempty"`
	Truncate *TruncateEvent `json:"truncate,omitempty"`
}

// TruncateEvent represents a truncate operation
type TruncateEvent struct {
	ToolCallID string `json:"tool_call_id"`
	Turn       int    `json:"turn"`
	Trigger    string `json:"trigger"` // "context_management" | "manual"
	Timestamp  string `json:"timestamp"`
}

// NewMessageEntry creates a journal entry for a message
func NewMessageEntry(msg AgentMessage) JournalEntry {
	return JournalEntry{
		Type:    "message",
		Message: &msg,
	}
}

// NewTruncateEntry creates a journal entry for a truncate operation
func NewTruncateEntry(toolCallID string, turn int, trigger string) JournalEntry {
	return JournalEntry{
		Type: "truncate",
		Truncate: &TruncateEvent{
			ToolCallID: toolCallID,
			Turn:       turn,
			Trigger:    trigger,
			Timestamp:  time.Now().Format(time.RFC3339),
		},
	}
}
