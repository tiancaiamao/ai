package context

import "context"

// Compactor interface for context compression.
type Compactor interface {
	ShouldCompact(ctx context.Context, agentCtx *AgentContext) bool
	Compact(ctx *AgentContext) (*CompactionResult, error)
	// CalculateDynamicThreshold returns the token threshold for compaction
	CalculateDynamicThreshold() int
}

// CompactionResult contains the result of a compaction operation.
// Note: Messages are not returned because Compact() directly modifies ctx.RecentMessages.
type CompactionResult struct {
	Summary      string // The generated summary
	TokensBefore int    // Token count before compaction
	TokensAfter  int    // Token count after compaction
}
