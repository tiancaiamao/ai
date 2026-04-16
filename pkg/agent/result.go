package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// GetTotalUsage aggregates usage statistics from all assistant messages.
func GetTotalUsage(messages []agentctx.AgentMessage) UsageStats {
	var total UsageStats
	for _, msg := range messages {
		if msg.Role == "assistant" && msg.Usage != nil {
			total.InputTokens += msg.Usage.InputTokens
			total.OutputTokens += msg.Usage.OutputTokens
			total.TotalTokens += msg.Usage.TotalTokens
		}
	}
	return total
}

// UsageStats represents token usage statistics.
type UsageStats struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}