package context

// CalculateStale calculates the stale score for a tool result.
// stale = total visible tool results - position in list (0-indexed from oldest)
// Higher stale = older output.
func CalculateStale(resultIndex int, totalVisibleToolResults int) int {
	if totalVisibleToolResults == 0 {
		return 0
	}
	return totalVisibleToolResults - resultIndex - 1
}

// CountStaleOutputs counts tool outputs with stale >= threshold.
func (s *ContextSnapshot) CountStaleOutputs(threshold int) int {
	if s == nil {
		return 0
	}

	// Get all visible, non-truncated tool results
	toolResults := s.GetVisibleToolResults()

	count := 0
	for i := range toolResults {
		stale := CalculateStale(i, len(toolResults))
		if stale >= threshold {
			count++
		}
	}

	return count
}

// GetVisibleToolResults returns all visible, non-truncated tool results.
func (s *ContextSnapshot) GetVisibleToolResults() []AgentMessage {
	if s == nil {
		return []AgentMessage{}
	}

	var results []AgentMessage
	for _, msg := range s.RecentMessages {
		if msg.Role == "toolResult" && !msg.IsTruncated() && msg.IsAgentVisible() {
			results = append(results, msg)
		}
	}
	return results
}
