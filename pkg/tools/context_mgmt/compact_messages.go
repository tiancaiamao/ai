package context_mgmt

import (
	"context"
	"fmt"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// CompactMessagesTool compacts the message history using the compactor.
// The actual compaction is performed by the agent layer, this tool indicates the intent.
type CompactMessagesTool struct {
	snapshot *agentctx.ContextSnapshot
}

// NewCompactMessagesTool creates a new CompactMessagesTool.
func NewCompactMessagesTool(snapshot *agentctx.ContextSnapshot) *CompactMessagesTool {
	return &CompactMessagesTool{
		snapshot: snapshot,
	}
}

// Name returns the tool name.
func (t *CompactMessagesTool) Name() string {
	return "compact_messages"
}

// Description returns the tool description.
func (t *CompactMessagesTool) Description() string {
	return "Compact the message history by summarizing old messages and keeping recent ones. Use this to significantly reduce token usage when context is large."
}

// Parameters returns the JSON schema for parameters.
func (t *CompactMessagesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{
				"type":        "string",
				"description": "Reason for compacting (optional, for logging)",
			},
		},
	}
}

// Execute marks that compaction should be performed.
// Note: The actual compaction is handled by the agent layer after the tool returns.
func (t *CompactMessagesTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	if t.snapshot == nil {
		return nil, fmt.Errorf("context snapshot is not available")
	}

	reason := ""
	if r, ok := params["reason"].(string); ok {
		reason = r
	}

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_compact_requested",
		traceevent.Field{Key: "reason", Value: reason},
		traceevent.Field{Key: "turn", Value: t.snapshot.AgentState.TotalTurns},
		traceevent.Field{Key: "message_count", Value: len(t.snapshot.RecentMessages)},
	)

	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "Message compaction will be performed."},
	}, nil
}
