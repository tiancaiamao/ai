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
	// Check if there's a message with this ToolCallID that is a tool result
	for _, msg := range t.snapshot.RecentMessages {
		if msg.ToolCallID == id && msg.Role == "toolResult" {
			return true
		}
	}
	return false
}

// applyTruncate marks messages as truncated and records to journal.
func (t *TruncateMessagesTool) applyTruncate(ctx context.Context, ids []string) int {
	count := 0
	for _, id := range ids {
		// Mark as truncated in snapshot
		for i := range t.snapshot.RecentMessages {
			if t.snapshot.RecentMessages[i].ToolCallID == id {
				t.snapshot.RecentMessages[i].Truncated = true
				t.snapshot.RecentMessages[i].TruncatedAt = t.snapshot.AgentState.TotalTurns
				t.snapshot.RecentMessages[i].OriginalSize = len(t.snapshot.RecentMessages[i].ExtractText())

				// Record to journal
				t.journal.AppendTruncate(agentctx.TruncateEvent{
					ToolCallID: id,
					Turn:       t.snapshot.AgentState.TotalTurns,
					Trigger:    "context_management",
					Timestamp:  time.Now().Format(time.RFC3339),
				})

				count++
				break
			}
		}
	}
	return count
}
