package context

// Compactor interface for context compression.
type Compactor interface {
	ShouldCompact(messages []AgentMessage) bool
	Compact(messages []AgentMessage, previousSummary string) (*CompactionResult, error)
	// CalculateDynamicThreshold returns the token threshold for compaction
	CalculateDynamicThreshold() int
	// EstimateContextTokens estimates the token count of messages
	EstimateContextTokens(messages []AgentMessage) int
}

// CompactionResult contains the result of a compaction operation.
type CompactionResult struct {
	Summary      string        // The generated summary
	Messages     []AgentMessage // The compressed message list
	TokensBefore int           // Token count before compaction
	TokensAfter  int           // Token count after compaction
}