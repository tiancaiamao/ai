package agent

import (
	"fmt"
	"sync"
	"time"

	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// Metrics aggregates statistics from trace events.
// Following unified observability: traces are the source of truth,
// metrics are derived views over trace events.
type Metrics struct {
	mu            sync.RWMutex
	buf           *traceevent.TraceBuf // Trace event source
	sessionStart  time.Time
	lastFlush     time.Time
	flushInterval time.Duration

	// Cached aggregations (updated on demand)
	cachedToolStats    map[string]*toolStatsCache
	cachedLLMStats     *llmStatsCache
	cachedPromptStats  *promptStatsCache
	cachedMessageStats *messageStatsCache
	cacheValid         bool
}

// toolStatsCache holds aggregated tool statistics
type toolStatsCache struct {
	Name            string
	CallCount       int64
	SuccessCount    int64
	FailCount       int64
	TotalDurationNs int64
	RetryCount      int64
	LastCall        time.Time
}

// llmStatsCache holds aggregated LLM statistics
type llmStatsCache struct {
	CallCount         int64
	TokenInput        int64
	TokenOutput       int64
	CacheRead         int64
	CacheWrite        int64
	ErrorCount        int64
	TotalDurationNs   int64
	FirstTokenTotalNs int64
	LastEndNs         int64
	LastStartNs       int64
}

// promptStatsCache holds aggregated prompt statistics
type promptStatsCache struct {
	CallCount       int64
	ErrorCount      int64
	TotalDurationNs int64
	LastEndNs       int64
	LastStartNs     int64
}

// messageStatsCache holds aggregated message statistics
type messageStatsCache struct {
	UserMessages      int64
	AssistantMessages int64
	ToolCalls         int64
	ToolResults       int64
}

// NewMetrics creates a new metrics collector attached to a trace buffer.
func NewMetrics(buf *traceevent.TraceBuf) *Metrics {
	return &Metrics{
		buf:                buf,
		sessionStart:       time.Now(),
		flushInterval:      5 * time.Second,
		cachedToolStats:    make(map[string]*toolStatsCache),
		cachedLLMStats:     &llmStatsCache{},
		cachedPromptStats:  &promptStatsCache{},
		cachedMessageStats: &messageStatsCache{},
	}
}

// InvalidateCache marks the cached aggregations as stale.
func (m *Metrics) InvalidateCache() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cacheValid = false
}

// refreshAggregations recomputes metrics from trace events if cache is stale.
func (m *Metrics) refreshAggregations() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cacheValid {
		return
	}

	// Reset caches
	m.cachedToolStats = make(map[string]*toolStatsCache)
	m.cachedLLMStats = &llmStatsCache{}
	m.cachedPromptStats = &promptStatsCache{}
	m.cachedMessageStats = &messageStatsCache{}

	// Aggregate from trace events
	events := m.buf.Snapshot()
	for _, event := range events {
		m.aggregateEvent(event)
	}

	m.cacheValid = true
	m.lastFlush = time.Now()
}

// aggregateEvent incorporates a single trace event into the aggregation.
func (m *Metrics) aggregateEvent(event traceevent.TraceEvent) {
	switch canonicalTraceName(event.Name) {
	case "prompt":
		m.aggregatePrompt(event)
	case "llm_call":
		m.aggregateLLM(event)
	case "tool_execution":
		m.aggregateTool(event)
	case "message_end":
		m.aggregateMessage(event)
	}
}

// aggregatePrompt processes prompt-related events
func (m *Metrics) aggregatePrompt(event traceevent.TraceEvent) {
	if event.Phase == traceevent.PhaseEnd {
		m.cachedPromptStats.CallCount++
		durationMs := traceFieldInt64(event.Fields, "duration_ms")
		m.cachedPromptStats.TotalDurationNs += durationMs * 1_000_000
		m.cachedPromptStats.LastEndNs = event.Timestamp.UnixNano()

		if traceFieldBool(event.Fields, "error") {
			m.cachedPromptStats.ErrorCount++
		}
	}
	if event.Phase == traceevent.PhaseBegin {
		m.cachedPromptStats.LastStartNs = event.Timestamp.UnixNano()
	}
}

// aggregateLLM processes LLM-related events
func (m *Metrics) aggregateLLM(event traceevent.TraceEvent) {
	if event.Phase == traceevent.PhaseEnd {
		m.cachedLLMStats.CallCount++
		m.cachedLLMStats.TokenInput += traceFieldInt64(event.Fields, "input_tokens")
		m.cachedLLMStats.TokenOutput += traceFieldInt64(event.Fields, "output_tokens")
		m.cachedLLMStats.CacheRead += traceFieldInt64(event.Fields, "cache_read")
		m.cachedLLMStats.CacheWrite += traceFieldInt64(event.Fields, "cache_write")
		durationMs := traceFieldInt64(event.Fields, "duration_ms")
		m.cachedLLMStats.TotalDurationNs += durationMs * 1_000_000
		m.cachedLLMStats.LastEndNs = event.Timestamp.UnixNano()

		firstTokenMs := traceFieldInt64(event.Fields, "first_token_ms")
		if firstTokenMs > 0 {
			m.cachedLLMStats.FirstTokenTotalNs += firstTokenMs * 1_000_000
		}

		if traceFieldString(event.Fields, "error") != "" {
			m.cachedLLMStats.ErrorCount++
		}
	}
	if event.Phase == traceevent.PhaseBegin {
		m.cachedLLMStats.LastStartNs = event.Timestamp.UnixNano()
	}
}

// aggregateTool processes tool execution events
func (m *Metrics) aggregateTool(event traceevent.TraceEvent) {
	if event.Phase == traceevent.PhaseBegin {
		m.cachedMessageStats.ToolCalls++
	}
	if event.Phase == traceevent.PhaseEnd {
		m.cachedMessageStats.ToolResults++

		toolName := traceFieldString(event.Fields, "tool")
		if toolName == "" {
			toolName = "unknown"
		}

		cache, ok := m.cachedToolStats[toolName]
		if !ok {
			cache = &toolStatsCache{Name: toolName}
			m.cachedToolStats[toolName] = cache
		}

		cache.CallCount++
		durationMs := traceFieldInt64(event.Fields, "duration_ms")
		cache.TotalDurationNs += durationMs * 1_000_000
		cache.LastCall = event.Timestamp

		if traceFieldBool(event.Fields, "error") {
			cache.FailCount++
		} else {
			cache.SuccessCount++
		}
	}
}

// aggregateMessage processes message events
func (m *Metrics) aggregateMessage(event traceevent.TraceEvent) {
	if event.Phase != traceevent.PhaseInstant {
		return
	}
	role := traceFieldString(event.Fields, "role")
	switch role {
	case "user":
		m.cachedMessageStats.UserMessages++
	case "assistant":
		m.cachedMessageStats.AssistantMessages++
	}
}

// GetToolMetrics returns aggregated metrics for a specific tool.
func (m *Metrics) GetToolMetrics(toolName string) ToolMetricsSnapshot {
	m.refreshAggregations()

	m.mu.RLock()
	defer m.mu.RUnlock()

	cache, ok := m.cachedToolStats[toolName]
	if !ok {
		return ToolMetricsSnapshot{}
	}

	return ToolMetricsSnapshot{
		Name:              cache.Name,
		CallCount:         cache.CallCount,
		SuccessCount:      cache.SuccessCount,
		FailCount:         cache.FailCount,
		SuccessRate:       float64(cache.SuccessCount) / float64(cache.CallCount),
		AverageDurationMs: cache.TotalDurationNs / cache.CallCount / 1_000_000,
		LastCall:          cache.LastCall,
	}
}

// GetLLMMetrics returns aggregated LLM metrics.
func (m *Metrics) GetLLMMetrics() LLMMetricsSnapshot {
	m.refreshAggregations()

	m.mu.RLock()
	defer m.mu.RUnlock()

	totalCalls := m.cachedLLMStats.CallCount
	successRate := 0.0
	if totalCalls > 0 {
		successRate = float64(totalCalls-m.cachedLLMStats.ErrorCount) / float64(totalCalls)
	}

	avgTokens := int64(0)
	if totalCalls > 0 {
		avgTokens = (m.cachedLLMStats.TokenInput + m.cachedLLMStats.TokenOutput) / totalCalls
	}

	avgFirstTokenMs := int64(0)
	if totalCalls > 0 && m.cachedLLMStats.FirstTokenTotalNs > 0 {
		avgFirstTokenMs = m.cachedLLMStats.FirstTokenTotalNs / totalCalls / 1_000_000
	}

	return LLMMetricsSnapshot{
		CallCount:               totalCalls,
		TokenInput:              m.cachedLLMStats.TokenInput,
		TokenOutput:             m.cachedLLMStats.TokenOutput,
		CacheRead:               m.cachedLLMStats.CacheRead,
		CacheWrite:              m.cachedLLMStats.CacheWrite,
		ErrorCount:              m.cachedLLMStats.ErrorCount,
		SuccessRate:             successRate,
		AvgTokensPerCall:        avgTokens,
		TotalDuration:           time.Duration(m.cachedLLMStats.TotalDurationNs),
		AvgDurationPerCall:      time.Duration(m.cachedLLMStats.TotalDurationNs / int64(totalCalls)),
		AvgFirstTokenDuration:   time.Duration(avgFirstTokenMs) * time.Millisecond,
		LastStart:               timeFromNs(m.cachedLLMStats.LastStartNs),
		LastEnd:                 timeFromNs(m.cachedLLMStats.LastEndNs),
	}
}

// GetPromptMetrics returns aggregated prompt metrics.
func (m *Metrics) GetPromptMetrics() PromptMetricsSnapshot {
	m.refreshAggregations()

	m.mu.RLock()
	defer m.mu.RUnlock()

	totalCalls := m.cachedPromptStats.CallCount
	successRate := 0.0
	if totalCalls > 0 {
		successRate = float64(totalCalls-m.cachedPromptStats.ErrorCount) / float64(totalCalls)
	}

	return PromptMetricsSnapshot{
		CallCount:    totalCalls,
		ErrorCount:   m.cachedPromptStats.ErrorCount,
		SuccessRate:  successRate,
		TotalDuration: time.Duration(m.cachedPromptStats.TotalDurationNs),
		AvgDuration:   time.Duration(m.cachedPromptStats.TotalDurationNs / int64(totalCalls)),
		LastStart:     timeFromNs(m.cachedPromptStats.LastStartNs),
		LastEnd:       timeFromNs(m.cachedPromptStats.LastEndNs),
	}
}

// GetMessageCounts returns aggregated message counts.
func (m *Metrics) GetMessageCounts() MessageCountsSnapshot {
	m.refreshAggregations()

	m.mu.RLock()
	defer m.mu.RUnlock()

	return MessageCountsSnapshot{
		UserMessages:      m.cachedMessageStats.UserMessages,
		AssistantMessages: m.cachedMessageStats.AssistantMessages,
		ToolCalls:         m.cachedMessageStats.ToolCalls,
		ToolResults:       m.cachedMessageStats.ToolResults,
	}
}

// GetAllTools returns all tool names that have metrics.
func (m *Metrics) GetAllTools() []string {
	m.refreshAggregations()

	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.cachedToolStats))
	for name := range m.cachedToolStats {
		names = append(names, name)
	}
	return names
}

// GetFullMetrics returns a complete metrics snapshot.
func (m *Metrics) GetFullMetrics() FullMetricsSnapshot {
	m.refreshAggregations()

	m.mu.RLock()
	defer m.mu.RUnlock()

	toolSnapshots := make([]ToolMetricsSnapshot, 0, len(m.cachedToolStats))
	for _, cache := range m.cachedToolStats {
		toolSnapshots = append(toolSnapshots, ToolMetricsSnapshot{
			Name:              cache.Name,
			CallCount:         cache.CallCount,
			SuccessCount:      cache.SuccessCount,
			FailCount:         cache.FailCount,
			SuccessRate:       float64(cache.SuccessCount) / float64(cache.CallCount),
			AverageDurationMs: cache.TotalDurationNs / cache.CallCount / 1_000_000,
			LastCall:          cache.LastCall,
		})
	}

	return FullMetricsSnapshot{
		SessionUptime: time.Since(m.sessionStart),
		ToolMetrics:   toolSnapshots,
		LLMMetrics:    m.GetLLMMetrics(),
		MessageCounts: m.GetMessageCounts(),
		PromptMetrics: m.GetPromptMetrics(),
	}
}

// GetSessionUptime returns the session uptime.
func (m *Metrics) GetSessionUptime() time.Duration {
	return time.Since(m.sessionStart)
}

// Reset clears all cached metrics.
func (m *Metrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cachedToolStats = make(map[string]*toolStatsCache)
	m.cachedLLMStats = &llmStatsCache{}
	m.cachedPromptStats = &promptStatsCache{}
	m.cachedMessageStats = &messageStatsCache{}
	m.cacheValid = false
	m.sessionStart = time.Now()
}

// RecordTraceEvent records a trace event to the buffer and invalidates cache.
// This is called by the TraceBuf sink when new events are recorded.
// For direct recording (e.g., in tests), also records to the buffer.
func (m *Metrics) RecordTraceEvent(event traceevent.TraceEvent) {
	m.buf.Record(event)
	m.InvalidateCache()
}

// Helper functions

func canonicalTraceName(name string) string {
	switch name {
	case "prompt_start", "prompt_end":
		return "prompt"
	case "llm_call_start", "llm_call_end":
		return "llm_call"
	case "tool_execution_start", "tool_execution_end":
		return "tool_execution"
	case "event_loop_start", "event_loop_end":
		return "event_loop"
	case "assistant_text_start", "assistant_text_end":
		return "assistant_text"
	default:
		return name
	}
}

func traceFieldString(fields []traceevent.Field, key string) string {
	for _, f := range fields {
		if f.Key != key || f.Value == nil {
			continue
		}
		switch v := f.Value.(type) {
		case string:
			return v
		default:
			return fmt.Sprint(v)
		}
	}
	return ""
}

func traceFieldInt64(fields []traceevent.Field, key string) int64 {
	for _, f := range fields {
		if f.Key != key || f.Value == nil {
			continue
		}
		switch v := f.Value.(type) {
		case int:
			return int64(v)
		case int64:
			return v
		case uint64:
			return int64(v)
		case float64:
			return int64(v)
		case time.Duration:
			return v.Milliseconds()
		case string:
			var parsed int64
			_, _ = fmt.Sscanf(v, "%d", &parsed)
			return parsed
		}
	}
	return 0
}

func traceFieldBool(fields []traceevent.Field, key string) bool {
	for _, f := range fields {
		if f.Key != key || f.Value == nil {
			continue
		}
		switch v := f.Value.(type) {
		case bool:
			return v
		case string:
			return v == "true" || v == "1"
		case int:
			return v != 0
		case int64:
			return v != 0
		}
	}
	return false
}

func timeFromNs(ns int64) time.Time {
	if ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// Snapshot types (exported for RPC handlers)

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
	CallCount               int64         `json:"callCount"`
	TokenInput              int64         `json:"tokenInput"`
	TokenOutput             int64         `json:"tokenOutput"`
	CacheRead               int64         `json:"cacheRead"`
	CacheWrite              int64         `json:"cacheWrite"`
	ErrorCount              int64         `json:"errorCount"`
	SuccessRate             float64       `json:"successRate"`
	AvgTokensPerCall        int64         `json:"avgTokensPerCall"`
	TotalDuration           time.Duration `json:"totalDuration"`
	AvgDurationPerCall      time.Duration `json:"avgDurationPerCall"`
	FirstTokenTotalDuration time.Duration `json:"firstTokenTotalDuration"`
	LastFirstTokenDuration  time.Duration `json:"lastFirstTokenDuration"`
	AvgFirstTokenDuration   time.Duration `json:"avgFirstTokenDuration"`
	InFlight                bool          `json:"inFlight"`
	InFlightDuration        time.Duration `json:"inFlightDuration"`
	LastStart               time.Time     `json:"lastStart"`
	LastEnd                 time.Time     `json:"lastEnd"`
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

type FullMetricsSnapshot struct {
	SessionUptime time.Duration         `json:"sessionUptime"`
	ToolMetrics   []ToolMetricsSnapshot `json:"toolMetrics"`
	LLMMetrics    LLMMetricsSnapshot    `json:"llmMetrics"`
	MessageCounts MessageCountsSnapshot `json:"messageCounts"`
	PromptMetrics PromptMetricsSnapshot `json:"promptMetrics"`
}
