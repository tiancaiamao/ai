package context_mgmt

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// TruncateMessagesTool truncates old tool outputs.
//
// Thread safety: The Execute() method should be called with the snapshot lock held
// to ensure safe concurrent access to the ContextSnapshot.
type TruncateMessagesTool struct {
	snapshot *agentctx.ContextSnapshot
	journal  *agentctx.Journal
}

// NewTruncateMessagesTool creates a new TruncateMessagesTool.
func NewTruncateMessagesTool(snapshot *agentctx.ContextSnapshot, journal *agentctx.Journal) *TruncateMessagesTool {
	return &TruncateMessagesTool{
		snapshot: snapshot,
		journal:  journal,
	}
}

// Name returns the tool name.
func (t *TruncateMessagesTool) Name() string {
	return "truncate_messages"
}

// Description returns the tool description.
func (t *TruncateMessagesTool) Description() string {
	return "Truncate old tool outputs to save context space. Specify message IDs to truncate."
}

// Parameters returns the JSON schema for parameters.
func (t *TruncateMessagesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message_ids": map[string]any{
				"type":        "string",
				"description": "Comma-separated tool call IDs to truncate",
			},
		},
		"required": []string{"message_ids"},
	}
}

// Execute truncates the specified messages.
func (t *TruncateMessagesTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	idsRaw, ok := params["message_ids"].(string)
	if !ok || idsRaw == "" {
		return nil, fmt.Errorf("message_ids is required")
	}

	// Parse and validate IDs
	ids := strings.Split(idsRaw, ",")
	var validIDs []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !t.isValidToolCallID(id) {
			traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_invalid_id",
				traceevent.Field{Key: "id", Value: id},
			)
			continue
		}
		validIDs = append(validIDs, id)
	}

	if len(validIDs) == 0 {
		return nil, fmt.Errorf("no valid tool call IDs provided")
	}

	// Apply truncate
	count := t.applyTruncate(ctx, validIDs)

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_messages_truncated",
		traceevent.Field{Key: "count", Value: count},
		traceevent.Field{Key: "ids", Value: strings.Join(validIDs, ",")})

	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: fmt.Sprintf("Truncated %d messages.", count)},
	}, nil
}

// isValidToolCallID checks if the ID is a valid tool call ID.
func (t *TruncateMessagesTool) isValidToolCallID(id string) bool {
	protectedStart := len(t.snapshot.RecentMessages) - agentctx.RecentMessagesKeep
	if protectedStart < 0 {
		protectedStart = 0
	}

	// Check if there's a message with this ToolCallID that is a tool result
	for i, msg := range t.snapshot.RecentMessages {
		if msg.ToolCallID == id && msg.Role == "toolResult" && !msg.Truncated {
			// The most recent N messages are protected and can never be truncated.
			if i >= protectedStart {
				return false
			}
			return true
		}
	}
	return false
}

// applyTruncate marks messages as truncated and records to journal.
// Instead of completely removing content, it preserves head/tail with a truncation marker.
func (t *TruncateMessagesTool) applyTruncate(ctx context.Context, ids []string) int {
	count := 0
	for _, id := range ids {
		// Mark as truncated in snapshot
		for i := range t.snapshot.RecentMessages {
			if t.snapshot.RecentMessages[i].ToolCallID == id {
				msg := &t.snapshot.RecentMessages[i]
				originalText := msg.ExtractText()
				originalSize := len(originalText)

				msg.Truncated = true
				msg.TruncatedAt = t.snapshot.AgentState.TotalTurns
				msg.OriginalSize = originalSize

				// Replace content with head/tail preserved summary
				msg.Content = []agentctx.ContentBlock{
					agentctx.TextContent{
						Type: "text",
						Text: agentctx.TruncateWithHeadTail(originalText),
					},
				}

				// Record to journal
				if err := t.journal.AppendTruncate(agentctx.TruncateEvent{
					ToolCallID: id,
					Turn:       t.snapshot.AgentState.TotalTurns,
					Trigger:    "context_management",
					Timestamp:  time.Now().Format(time.RFC3339),
				}); err != nil {
					traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_journal_append_failed",
						traceevent.Field{Key: "tool_call_id", Value: id},
						traceevent.Field{Key: "error", Value: err.Error()},
					)
					continue
				}

				count++
				break
			}
		}
	}
	return count
}