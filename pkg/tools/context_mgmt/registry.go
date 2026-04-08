package context_mgmt

import (
	"context"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// Registry holds all context management tools.
type Registry struct {
	snapshot *agentctx.ContextSnapshot
	journal  *agentctx.Journal
}

// NewRegistry creates a new context management tool registry.
func NewRegistry(snapshot *agentctx.ContextSnapshot, journal *agentctx.Journal) *Registry {
	return &Registry{
		snapshot: snapshot,
		journal:  journal,
	}
}

// Tools returns all context management tools.
func (r *Registry) Tools() map[string]agentctx.Tool {
	tools := make(map[string]agentctx.Tool)

	// Truncate messages tool
	truncateTool := NewTruncateMessagesTool(r.snapshot, r.journal)
	tools[truncateTool.Name()] = truncateTool

	// Update LLM context tool
	updateTool := NewUpdateLLMContextTool(r.snapshot)
	tools[updateTool.Name()] = updateTool

	return tools
}

// Tool returns a specific tool by name.
func (r *Registry) Tool(name string) (agentctx.Tool, bool) {
	tools := r.Tools()
	tool, ok := tools[name]
	return tool, ok
}

// NoActionTool is a placeholder for no-action decision.
type NoActionTool struct{}

// NewNoActionTool creates a new no-action tool.
func NewNoActionTool() *NoActionTool {
	return &NoActionTool{}
}

// Name returns the tool name.
func (t *NoActionTool) Name() string {
	return "no_action"
}

// Description returns the tool description.
func (t *NoActionTool) Description() string {
	return "Indicates that no context management is needed right now."
}

// Parameters returns the JSON schema for parameters.
func (t *NoActionTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

// Execute indicates no action is needed.
func (t *NoActionTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "No action needed."},
	}, nil
}