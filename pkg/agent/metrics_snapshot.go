package agent

import "time"

type ToolMetricsSnapshot struct {
	Name              string    `json:"name"`
	CallCount         int64     `json:"callCount"`
	SuccessCount      int64     `json:"successCount"`
	FailCount         int64     `json:"failCount"`
	SuccessRate       float64   `json:"successRate"`
	AverageDurationMs int64     `json:"averageDurationMs"`
	RetryCount        int64     `json:"retryCount"` // Kept for compatibility
	LastCall          time.Time `json:"lastCall"`
}

type LLMMetricsSnapshot struct {
	CallCount                int64         `json:"callCount"`
	TokenInput               int64         `json:"tokenInput"`
	TokenOutput              int64         `json:"tokenOutput"`
	TokenTotal               int64         `json:"tokenTotal"`
	CacheRead                int64         `json:"cacheRead"`
	CacheWrite               int64         `json:"cacheWrite"`
	ErrorCount               int64         `json:"errorCount"`
	RetryCount               int64         `json:"retryCount"`
	ErrorRateLimitCount      int64         `json:"errorRateLimitCount"`
	ErrorTimeoutCount        int64         `json:"errorTimeoutCount"`
	ErrorContextLimitCount   int64         `json:"errorContextLimitCount"`
	ErrorNetworkCount        int64         `json:"errorNetworkCount"`
	ErrorServerCount         int64         `json:"errorServerCount"`
	ErrorClientCount         int64         `json:"errorClientCount"`
	ErrorCanceledCount       int64         `json:"errorCanceledCount"`
	ErrorUnknownCount        int64         `json:"errorUnknownCount"`
	LastErrorType            string        `json:"lastErrorType"`
	LastErrorMessage         string        `json:"lastErrorMessage"`
	LastErrorStatusCode      int           `json:"lastErrorStatusCode"`
	LastErrorAt              time.Time     `json:"lastErrorAt"`
	LastRetryAfter           time.Duration `json:"lastRetryAfter"`
	SuccessRate              float64       `json:"successRate"`
	AvgTokensPerCall         int64         `json:"avgTokensPerCall"`
	TotalDuration            time.Duration `json:"totalDuration"`
	AvgDurationPerCall       time.Duration `json:"avgDurationPerCall"`
	LastDuration             time.Duration `json:"lastDuration"`
	LastInputTokens          int64         `json:"lastInputTokens"`
	LastOutputTokens         int64         `json:"lastOutputTokens"`
	LastTotalTokens          int64         `json:"lastTotalTokens"`
	LastInputTokensPerSec    float64       `json:"lastInputTokensPerSec"`
	LastOutputTokensPerSec   float64       `json:"lastOutputTokensPerSec"`
	LastTotalTokensPerSec    float64       `json:"lastTotalTokensPerSec"`
	ActiveInputTokensPerSec  float64       `json:"activeInputTokensPerSec"`
	ActiveOutputTokensPerSec float64       `json:"activeOutputTokensPerSec"`
	ActiveTotalTokensPerSec  float64       `json:"activeTotalTokensPerSec"`
	WallDuration             time.Duration `json:"wallDuration"`
	WallInputTokensPerSec    float64       `json:"wallInputTokensPerSec"`
	WallOutputTokensPerSec   float64       `json:"wallOutputTokensPerSec"`
	WallTotalTokensPerSec    float64       `json:"wallTotalTokensPerSec"`
	RecentWindowSeconds      int64         `json:"recentWindowSeconds"`
	RecentWindowInputTokens  int64         `json:"recentWindowInputTokens"`
	RecentWindowOutputTokens int64         `json:"recentWindowOutputTokens"`
	RecentWindowTotalTokens  int64         `json:"recentWindowTotalTokens"`
	RecentWindowDuration     time.Duration `json:"recentWindowDuration"`
	RecentInputTokensPerSec  float64       `json:"recentInputTokensPerSec"`
	RecentOutputTokensPerSec float64       `json:"recentOutputTokensPerSec"`
	RecentTotalTokensPerSec  float64       `json:"recentTotalTokensPerSec"`
	FirstTokenTotalDuration  time.Duration `json:"firstTokenTotalDuration"`
	LastFirstTokenDuration   time.Duration `json:"lastFirstTokenDuration"`
	AvgFirstTokenDuration    time.Duration `json:"avgFirstTokenDuration"`
	InFlight                 bool          `json:"inFlight"`
	InFlightDuration         time.Duration `json:"inFlightDuration"`
	LastStart                time.Time     `json:"lastStart"`
	LastEnd                  time.Time     `json:"lastEnd"`
}

type MessageCountsSnapshot struct {
	UserMessages      int64 `json:"userMessages"`
	AssistantMessages int64 `json:"assistantMessages"`
	ToolCalls         int64 `json:"toolCalls"`
	ToolResults       int64 `json:"toolResults"`
}

type PromptMetricsSnapshot struct {
	CallCount        int64         `json:"callCount"`
	ErrorCount       int64         `json:"errorCount"`
	SuccessRate      float64       `json:"successRate"`
	TotalDuration    time.Duration `json:"totalDuration"`
	LastDuration     time.Duration `json:"lastDuration"`
	AvgDuration      time.Duration `json:"avgDuration"`
	InFlight         bool          `json:"inFlight"`
	InFlightDuration time.Duration `json:"inFlightDuration"`
	LastStart        time.Time     `json:"lastStart"`
	LastEnd          time.Time     `json:"lastEnd"`
}

type ContextMetricsSnapshot struct {
	UpdateReminders   int64     `json:"updateReminders"`
	DecisionReminders int64     `json:"decisionReminders"`
	TotalReminders    int64     `json:"totalReminders"`
	LastReminderType  string    `json:"lastReminderType"`
	LastReminderAt    time.Time `json:"lastReminderAt"`
}

type FullMetricsSnapshot struct {
	SessionUptime  time.Duration          `json:"sessionUptime"`
	ToolMetrics    []ToolMetricsSnapshot  `json:"toolMetrics"`
	LLMMetrics     LLMMetricsSnapshot     `json:"llmMetrics"`
	MessageCounts  MessageCountsSnapshot  `json:"messageCounts"`
	PromptMetrics  PromptMetricsSnapshot  `json:"promptMetrics"`
	ContextMetrics ContextMetricsSnapshot `json:"contextMetrics"`
}
