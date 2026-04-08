package context

import (
	"time"
)

// EntryType represents the type of journal entry.
type EntryType string

const (
	EntryTypeMessage  EntryType = "message"
	EntryTypeTruncate EntryType = "truncate"
	EntryTypeCompact  EntryType = "compact"
)

// JournalEntry represents a line in messages.jsonl.
type JournalEntry struct {
	Type     string         `json:"type"` // "message" | "truncate" | "compact"
	Message  *AgentMessage  `json:"message,omitempty"`
	Truncate *TruncateEvent `json:"truncate,omitempty"`
	Compact  *CompactEvent  `json:"compact,omitempty"`
}

// TruncateEvent represents a truncate operation.
type TruncateEvent struct {
	ToolCallID string `json:"tool_call_id"`
	Turn       int    `json:"turn"`
	Trigger    string `json:"trigger"` // "context_management" | "manual"
	Timestamp  string `json:"timestamp"`
}

// CompactEvent represents a compact operation.
type CompactEvent struct {
	Summary          string `json:"summary"`
	KeptMessageCount int    `json:"kept_message_count"`
	Turn             int    `json:"turn"`
	Timestamp        string `json:"timestamp"`
}

// NewMessageEntry creates a journal entry for a message.
func NewMessageEntry(msg AgentMessage) JournalEntry {
	return JournalEntry{
		Type:    "message",
		Message: &msg,
	}
}

// NewTruncateEntry creates a journal entry for a truncate operation.
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

// NewCompactEntry creates a journal entry for a compact operation.
func NewCompactEntry(summary string, keptMessageCount int, turn int) JournalEntry {
	return JournalEntry{
		Type: "compact",
		Compact: &CompactEvent{
			Summary:          summary,
			KeptMessageCount: keptMessageCount,
			Turn:             turn,
			Timestamp:        time.Now().Format(time.RFC3339),
		},
	}
}