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

// GetContextManagementTools returns tools available for context management mode.
// Note: compact tool is added separately by the caller (in pkg/compact)
// to avoid circular imports.
func GetContextManagementTools(agentCtx *agentctx.AgentContext) []Tool {
	return []Tool{
		NewTruncateMessagesTool(agentCtx),
		NewUpdateLLMContextTool(agentCtx),
		NewNoActionTool(agentCtx),
	}
}