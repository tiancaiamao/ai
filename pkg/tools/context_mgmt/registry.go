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
