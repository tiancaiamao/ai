package context_mgmt

import (
	"context"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// NoActionTool indicates no context management is needed.
type NoActionTool struct {
	snapshot *agentctx.ContextSnapshot
}

// NewNoActionTool creates a new NoActionTool.
func NewNoActionTool(snapshot *agentctx.ContextSnapshot) *NoActionTool {
	return &NoActionTool{
		snapshot: snapshot,
	}
}

// Name returns the tool name.
func (t *NoActionTool) Name() string {
	return "no_action"
}

// Description returns the tool description.
func (t *NoActionTool) Description() string {
	return "Indicate that no context management is needed this cycle. Context is healthy."
}

// Parameters returns the JSON schema for parameters.
func (t *NoActionTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// Execute handles the no_action case.
func (t *NoActionTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	// Update LastTriggerTurn to enforce minInterval before next trigger
	t.snapshot.AgentState.LastTriggerTurn = t.snapshot.AgentState.TotalTurns
	t.snapshot.AgentState.TurnsSinceLastTrigger = 0
	t.snapshot.AgentState.ToolCallsSinceLastTrigger = 0

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_no_action",
		traceevent.Field{Key: "turn", Value: t.snapshot.AgentState.TotalTurns})

	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "No action taken. Context is healthy."},
	}, nil
}
