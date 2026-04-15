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
	MessagesBefore int   // Message count before compaction (for major compactions)
	MessagesAfter  int   // Message count after compaction (for major compactions)

	// Context management specific fields
	Type              string // "major" or "mini"
	TruncatedCount    int    // Number of messages truncated (mini only)
	LLMContextUpdated bool   // Whether LLM context was updated (mini only)
	ExecutedTools     []ToolCallRecord // Tools actually executed during this compaction
}

// ToolCallRecord captures a single tool invocation during compaction.
type ToolCallRecord struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
	Result string         `json:"result"`
	Error  string         `json:"error,omitempty"`
}
