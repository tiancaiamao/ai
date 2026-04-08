package context

// Compactor interface for context compression.
type Compactor interface {
	ShouldCompact(ctx *AgentContext) bool
	Compact(ctx *AgentContext) (*CompactionResult, error)
	// CalculateDynamicThreshold returns the token threshold for compaction
	CalculateDynamicThreshold() int
	// EstimateContextTokens estimates the token count of context
	EstimateContextTokens(ctx *AgentContext) int
}

// CompactionResult contains the result of a compaction operation.
// Note: Messages are not returned because Compact() directly modifies ctx.RecentMessages.
type CompactionResult struct {
	Summary      string // The generated summary
	TokensBefore int    // Token count before compaction
	TokensAfter  int    // Token count after compaction
}