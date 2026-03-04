package context

// Compactor interface for context compression.
type Compactor interface {
	ShouldCompact(messages []AgentMessage) bool
	Compact(messages []AgentMessage, previousSummary string) (*CompactionResult, error)
}

// CompactionResult contains the result of a compaction operation.
type CompactionResult struct {
	Summary      string        // The generated summary
	Messages     []AgentMessage // The compressed message list
	TokensBefore int           // Token count before compaction
	TokensAfter  int           // Token count after compaction
}