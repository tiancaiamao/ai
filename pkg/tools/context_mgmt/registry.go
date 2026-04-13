package context_mgmt

import (
	"context"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// Tool is the interface for context management tools.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error)
}

// GetMiniCompactTools returns tools available for mini compact mode.
func GetMiniCompactTools(agentCtx *agentctx.AgentContext) []Tool {
	return []Tool{
		NewTruncateMessagesTool(agentCtx),
		NewUpdateLLMContextTool(agentCtx),
		NewNoActionTool(agentCtx),
	}
}

// GetMiniCompactToolsWithCompactor returns tools for mini compact mode including compact tool.
func GetMiniCompactToolsWithCompactor(agentCtx *agentctx.AgentContext, compactor interface{}) []Tool {
	// Import compact package to get CompactTool
	tools := []Tool{
		NewTruncateMessagesTool(agentCtx),
		NewUpdateLLMContextTool(agentCtx),
		NewNoActionTool(agentCtx),
	}
	
	// Add compact tool if compactor is provided
	// This is done by the caller in compact package to avoid circular import
	return tools
}
