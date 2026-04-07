package context_mgmt

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// GetContextMgmtTools returns the tools available in Context Management mode.
// Note: compact_messages is NOT included - it's used as a system fallback when token usage remains critically high (>75%) after truncate+update.
func GetContextMgmtTools(sessionDir string, snapshot *agentctx.ContextSnapshot, journal *agentctx.Journal) []agentctx.Tool {
	return []agentctx.Tool{
		NewTruncateMessagesTool(snapshot, journal),
		NewUpdateLLMContextTool(snapshot),
		NewNoActionTool(snapshot),
	}
}
