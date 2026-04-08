package context_mgmt

import (
	"context"
	"fmt"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// NoActionTool indicates that no context management action is needed.
type NoActionTool struct {
	agentCtx *agentctx.AgentContext
}

// NewNoActionTool creates a new NoActionTool.
func NewNoActionTool(agentCtx *agentctx.AgentContext) *NoActionTool {
	return &NoActionTool{agentCtx: agentCtx}
}

// Name returns the tool name.
func (t *NoActionTool) Name() string {
	return "no_action"
}

// Description returns the tool description.
func (t *NoActionTool) Description() string {
	return "No context management action is needed at this time. Use when the context is healthy."
}

// Parameters returns the JSON schema for parameters.
func (t *NoActionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason": map[string]any{
				"type":        "string",
				"description": "Reason for no action (optional)",
			},
		},
	}
}

// Execute does nothing but returns a confirmation.
func (t *NoActionTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	reason := "context is healthy"
	if r, ok := params["reason"].(string); ok && r != "" {
		reason = r
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: fmt.Sprintf("No action taken: %s.", reason)},
	}, nil
}
