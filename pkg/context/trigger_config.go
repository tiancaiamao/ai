package context

// Trigger condition constants
const (
	// Trigger conditions
	IntervalTurns = 10  // Check every 10 turns
	MinTurns      = 5   // Don't trigger before turn 5
	TokenThreshold = 0.40 // 40% token usage
	TokenUrgent    = 0.75 // 75% urgent mode
	StaleCount     = 15  // 15 stale outputs
	MinInterval    = 3   // Min 3 turns between normal triggers

	// Message management
	RecentMessagesKeep      = 30 // Protected region size
	RecentMessagesShowInMgmt = 10 // Messages shown in LLM context during mgmt

	// Tool output formatting
	ToolOutputMaxChars    = 2000
	ToolOutputPreviewHead = 1800
	ToolOutputPreviewTail = 200
)
