package context

// Trigger condition constants
const (
	// Token-based trigger thresholds
	TokenUrgent = 0.70 // 70% — urgent, ignore interval
	TokenHigh   = 0.50 // 50% — aggressive truncation
	TokenMedium = 0.30 // 30% — start truncating
	TokenLow    = 0.20 // 20% — minimal intervention

	// Tool-call intervals based on token usage
	// After how many tool calls since last trigger to consider triggering again
	IntervalAtLow    = 30 // At 20-30%: truncate every ~30 tool calls
	IntervalAtMedium = 15 // At 30-50%: truncate every ~15 tool calls
	IntervalAtHigh   = 5  // At 50-70%: truncate every ~5 tool calls
	IntervalAtUrgent = 1  // At 70%+:  truncate every tool call

	// Stale output threshold
	StaleCount = 15 // 15 stale outputs triggers independently

	// Message management
	RecentMessagesKeep      = 30 // Protected region size
	RecentMessagesShowInMgmt = 10 // Messages shown in LLM context during mgmt

	// Tool output formatting
	ToolOutputMaxChars    = 2000
	ToolOutputPreviewHead = 1800
	ToolOutputPreviewTail = 200
)
