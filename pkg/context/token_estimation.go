package context

const (
	// ApproxBytesPerToken is the approximate number of bytes per token.
	// We use 4 as a conservative estimate (actual average is 3-4).
	ApproxBytesPerToken = 4
)

// EstimateTokens estimates the token count for a ContextSnapshot.
func (s *ContextSnapshot) EstimateTokens() int {
	if s == nil {
		return 0
	}

	// 1. If we have actual LLM usage data, use it
	if s.AgentState.TokensUsed > 0 {
		return s.AgentState.TokensUsed
	}

	// 2. Otherwise, estimate from snapshot:
	//    - LLMContext: len() / 4
	//    - RecentMessages: sum of message sizes / 4
	//    - AgentState: fixed overhead ~200 tokens
	total := 0

	// LLM context tokens
	total += len(s.LLMContext) / ApproxBytesPerToken

	// Recent messages tokens
	total += EstimateMessageTokens(s.RecentMessages)

	// AgentState overhead (fixed estimate for metadata)
	total += 200

	return total
}

// EstimateMessageTokens estimates token count for messages.
func EstimateMessageTokens(messages []AgentMessage) int {
	total := 0
	for _, msg := range messages {
		if !msg.IsAgentVisible() || msg.IsTruncated() {
			continue
		}
		// Rough estimate: 1 token per 4 characters
		total += len(msg.ExtractText()) / ApproxBytesPerToken
	}
	return total
}

// EstimateTokenPercent calculates the percentage of token limit used.
func (s *ContextSnapshot) EstimateTokenPercent() float64 {
	if s == nil || s.AgentState.TokensLimit == 0 {
		return 0
	}
	return float64(s.EstimateTokens()) / float64(s.AgentState.TokensLimit)
}
