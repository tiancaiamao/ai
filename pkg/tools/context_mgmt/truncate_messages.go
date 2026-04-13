package context_mgmt

import (
	"context"
	"fmt"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// TruncateMessagesTool truncates old tool outputs by replacing their content
// with a head/tail summary. It operates directly on AgentContext.RecentMessages.
type TruncateMessagesTool struct {
	agentCtx *agentctx.AgentContext
}

// NewTruncateMessagesTool creates a new TruncateMessagesTool.
func NewTruncateMessagesTool(agentCtx *agentctx.AgentContext) *TruncateMessagesTool {
	return &TruncateMessagesTool{agentCtx: agentCtx}
}

// Name returns the tool name.
func (t *TruncateMessagesTool) Name() string {
	return "truncate_messages"
}

// Description returns the tool description.
func (t *TruncateMessagesTool) Description() string {
	return "Remove low-value tool outputs by specifying their IDs. Use this when there are old, large tool outputs no longer needed for the current task."
}

// Parameters returns the JSON schema for parameters.
func (t *TruncateMessagesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message_ids": map[string]any{
				"type":        "string",
				"description": "Comma-separated tool call IDs to truncate. Only IDs shown with an \"id=\" field in the conversation are valid.",
			},
		},
		"required": []string{"message_ids"},
	}
}

// Execute truncates the specified messages.
// It appends a compact event to messages.jsonl (immutable log),
// then applies the truncation to the in-memory RecentMessages snapshot.
func (t *TruncateMessagesTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	idsRaw, ok := params["message_ids"].(string)
	if !ok || idsRaw == "" {
		return nil, fmt.Errorf("message_ids is required")
	}

	ids := strings.Split(idsRaw, ",")
	validIDs := t.filterValidIDs(ids)

	if len(validIDs) == 0 {
		return nil, fmt.Errorf("no valid tool call IDs provided")
	}

	// Append compact event to messages.jsonl (immutable, append-only)
	if t.agentCtx.OnCompactEvent != nil {
		if err := t.agentCtx.OnCompactEvent(&agentctx.CompactEventDetail{
			Action: agentctx.CompactActionTruncate,
			IDs:    validIDs,
		}); err != nil {
			return nil, fmt.Errorf("failed to persist compact event: %w", err)
		}
	}

	// Apply to in-memory snapshot
	count := t.applyTruncate(ctx, validIDs)

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_messages_truncated",
		traceevent.Field{Key: "count", Value: count},
		traceevent.Field{Key: "ids", Value: strings.Join(validIDs, ",")},
	)

	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: fmt.Sprintf("Truncated %d messages.", count)},
	}, nil
}

// filterValidIDs checks which IDs are truncatable (non-protected, non-already-truncated tool results).
func (t *TruncateMessagesTool) filterValidIDs(ids []string) []string {
	protectedStart := len(t.agentCtx.RecentMessages) - agentctx.RecentMessagesKeep
	if protectedStart < 0 {
		protectedStart = 0
	}

	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		for i, msg := range t.agentCtx.RecentMessages {
			if msg.ToolCallID == id && msg.Role == "toolResult" && !msg.Truncated {
				if i < protectedStart {
					valid = append(valid, id)
				}
				break
			}
		}
	}
	return valid
}

// applyTruncate marks messages as truncated and replaces content with head/tail summary.
func (t *TruncateMessagesTool) applyTruncate(ctx context.Context, ids []string) int {
	count := 0
	for _, id := range ids {
		for i := range t.agentCtx.RecentMessages {
			msg := &t.agentCtx.RecentMessages[i]
			if msg.ToolCallID != id || msg.Truncated {
				continue
			}

			originalText := msg.ExtractText()
			msg.Truncated = true
			msg.TruncatedAt = t.agentCtx.AgentState.TotalTurns
			msg.OriginalSize = len(originalText)
			msg.Content = []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: agentctx.TruncateWithHeadTail(originalText),
				},
			}
			count++
			break
		}
	}
	return count
}