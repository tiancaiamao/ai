package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// extractToolNameFromBlock extracts the tool name from a ContentBlock.
// Returns ("", false) if the block is not a ToolCallContent.
func extractToolNameFromBlock(block agentctx.ContentBlock) (string, bool) {
	tc, ok := block.(agentctx.ToolCallContent)
	if !ok {
		return "", false
	}
	return tc.Name, true
}

// extractToolArgsFromBlock extracts the tool arguments from a ContentBlock.
// Returns (nil, false) if the block is not a ToolCallContent.
func extractToolArgsFromBlock(block agentctx.ContentBlock) (map[string]any, bool) {
	tc, ok := block.(agentctx.ToolCallContent)
	if !ok {
		return nil, false
	}
	return tc.Arguments, true
}