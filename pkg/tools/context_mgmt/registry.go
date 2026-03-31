package context_mgmt

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// GetContextMgmtTools returns the tools available in Context Management mode.
func GetContextMgmtTools(sessionDir string, snapshot *agentctx.ContextSnapshot, journal *agentctx.Journal) []agentctx.Tool {
	return []agentctx.Tool{
		NewUpdateLLMContextTool(sessionDir),
		NewTruncateMessagesTool(snapshot, journal),
		NewNoActionTool(snapshot),
	}
}
