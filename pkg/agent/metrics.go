package agent

import (
	"sync"
	"time"

	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

const defaultLLMRecentWindow = 60 * time.Second

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
	cachedContextStats *contextStatsCache
	cacheValid         bool

	// Incremental aggregation state
	lastAggregatedCount int       // Number of events aggregated last time
	lastAggregatedTime  time.Time // Timestamp of last aggregated event
	bufferResetDetected bool      // Whether buffer was flushed/reset
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
	CallCount              int64
	TokenInput             int64
	TokenOutput            int64
	CacheRead              int64
	CacheWrite             int64
	ErrorCount             int64
	RetryCount             int64
	ErrorRateLimitCount    int64
	ErrorTimeoutCount      int64
	ErrorContextLimitCount int64
	ErrorNetworkCount      int64
	ErrorServerCount       int64
	ErrorClientCount       int64
	ErrorCanceledCount     int64
	ErrorUnknownCount      int64
	LastErrorType          string
	LastErrorMessage       string
	LastErrorStatusCode    int64
	LastErrorAtNs          int64
	LastRetryAfterNs       int64
	TotalDurationNs        int64
	FirstTokenTotalNs      int64
	LastEndNs              int64
	LastStartNs            int64
	FirstStartNs           int64
	LastDurationNs         int64
	LastInputTokens        int64
	LastOutputTokens       int64
	LastTotalTokens        int64
	LastFirstTokenNs       int64
	RecentWindowSeconds    int64
	RecentWindowStartNs    int64
	RecentWindowEndNs      int64
	RecentWindowInput      int64
	RecentWindowOutput     int64
	RecentWindowTotal      int64
	RecentWindowDurationNs int64
	samples                []llmTokenSample
}

type llmTokenSample struct {
	endNs        int64
	inputTokens  int64
	outputTokens int64
	totalTokens  int64
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

// contextStatsCache holds aggregated context management statistics
type contextStatsCache struct {
	UpdateReminders   int64
	DecisionReminders int64
	LastReminderType  string
	LastReminderAtNs  int64
}

// NewMetrics creates a new metrics collector attached to a trace buffer.
func NewMetrics(buf *traceevent.TraceBuf) *Metrics {
	return &Metrics{
		buf:                 buf,
		sessionStart:        time.Now(),
		flushInterval:       5 * time.Second,
		cachedToolStats:     make(map[string]*toolStatsCache),
		cachedLLMStats:      &llmStatsCache{RecentWindowSeconds: int64(defaultLLMRecentWindow.Seconds())},
		cachedPromptStats:   &promptStatsCache{},
		cachedMessageStats:  &messageStatsCache{},
		cachedContextStats:  &contextStatsCache{},
		lastAggregatedCount: 0,
		lastAggregatedTime:  time.Time{},
		bufferResetDetected: false,
	}
}

// InvalidateCache marks the cached aggregations as stale.
// Also detects if the trace buffer was flushed/reset.
func (m *Metrics) InvalidateCache() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Keep invalidation O(1) because it runs for every trace event.
	// Buffer reset detection happens in refreshAggregations where we already need a snapshot.
	m.cacheValid = false
}

// refreshAggregations recomputes metrics from trace events if cache is stale.
// Uses incremental aggregation: only processes new events since last refresh.
func (m *Metrics) refreshAggregations() {
	m.mu.RLock()
	if m.cacheValid {
		m.mu.RUnlock()
		return
	}
	m.mu.RUnlock()

	// Snapshot outside the metrics lock to avoid lock-order contention with trace writes.
	events := m.buf.Snapshot()
	currentCount := len(events)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cacheValid {
		return
	}

	var newestEventTime time.Time
	if currentCount > 0 {
		newestEventTime = events[currentCount-1].Timestamp
	}
	newestWentBackward := !newestEventTime.IsZero() && !m.lastAggregatedTime.IsZero() && newestEventTime.Before(m.lastAggregatedTime)
	// If count is unchanged but newest timestamp advanced, events were overwritten in-place.
	// This happens when TraceBuf runs as a ring buffer after reaching maxEvents.
	bufferOverwroteEvents := currentCount == m.lastAggregatedCount &&
		!newestEventTime.IsZero() &&
		!m.lastAggregatedTime.IsZero() &&
		newestEventTime.After(m.lastAggregatedTime)

		// Detect buffer reset/overwrite and fall back to full re-aggregation.
	needsFullAggregation := m.bufferResetDetected ||
		currentCount < m.lastAggregatedCount ||
		newestWentBackward ||
		bufferOverwroteEvents

	if needsFullAggregation {
		// Full aggregation: reset all caches and process all events
		m.cachedToolStats = make(map[string]*toolStatsCache)
		m.cachedLLMStats = &llmStatsCache{RecentWindowSeconds: int64(defaultLLMRecentWindow.Seconds())}
		m.cachedPromptStats = &promptStatsCache{}
		m.cachedMessageStats = &messageStatsCache{}
		m.cachedContextStats = &contextStatsCache{}

		for _, event := range events {
			m.aggregateEvent(event)
		}
	} else {
		// Incremental aggregation: only process new events
		startIdx := m.lastAggregatedCount
		if startIdx < 0 {
			startIdx = 0
		}

		// Process only the new events
		for i := startIdx; i < currentCount; i++ {
			m.aggregateEvent(events[i])
		}
	}

	// Update incremental state
	m.finalizeLLMWindowStats()
	m.cacheValid = true
	m.lastFlush = time.Now()
	m.lastAggregatedCount = currentCount
	m.bufferResetDetected = false

	// Track the timestamp of the last aggregated event
	if !newestEventTime.IsZero() {
		m.lastAggregatedTime = newestEventTime
	} else {
		m.lastAggregatedTime = time.Time{}
	}
}

func (m *Metrics) finalizeLLMWindowStats() {
	stats := m.cachedLLMStats
	if stats == nil {
		return
	}
	stats.RecentWindowStartNs = 0
	stats.RecentWindowEndNs = 0
	stats.RecentWindowInput = 0
	stats.RecentWindowOutput = 0
	stats.RecentWindowTotal = 0
	stats.RecentWindowDurationNs = 0
	if stats.RecentWindowSeconds <= 0 {
		stats.RecentWindowSeconds = int64(defaultLLMRecentWindow.Seconds())
	}
	if stats.LastEndNs == 0 || len(stats.samples) == 0 {
		return
	}

	windowNs := stats.RecentWindowSeconds * int64(time.Second)
	cutoffNs := stats.LastEndNs - windowNs
	firstInWindow := int64(0)
	keepIdx := len(stats.samples)
	for i, sample := range stats.samples {
		if sample.endNs < cutoffNs {
			continue
		}
		if keepIdx == len(stats.samples) {
			keepIdx = i
		}
		if firstInWindow == 0 {
			firstInWindow = sample.endNs
		}
		stats.RecentWindowInput += sample.inputTokens
		stats.RecentWindowOutput += sample.outputTokens
		stats.RecentWindowTotal += sample.totalTokens
	}
	if keepIdx > 0 && keepIdx < len(stats.samples) {
		copy(stats.samples, stats.samples[keepIdx:])
		stats.samples = stats.samples[:len(stats.samples)-keepIdx]
	}
	if firstInWindow == 0 {
		return
	}
	stats.RecentWindowStartNs = firstInWindow
	stats.RecentWindowEndNs = stats.LastEndNs
	stats.RecentWindowDurationNs = stats.RecentWindowEndNs - stats.RecentWindowStartNs
}

// GetLLMMetrics returns aggregated LLM metrics.
func (m *Metrics) GetLLMMetrics() LLMMetricsSnapshot {
	m.refreshAggregations()

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.llmMetricsSnapshotLocked()
}

func (m *Metrics) llmMetricsSnapshotLocked() LLMMetricsSnapshot {
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

	avgDurationPerCall := time.Duration(0)
	if totalCalls > 0 {
		avgDurationPerCall = time.Duration(m.cachedLLMStats.TotalDurationNs / totalCalls)
	}

	activeInputTPS := ratePerSecond(m.cachedLLMStats.TokenInput, m.cachedLLMStats.TotalDurationNs)
	activeOutputTPS := ratePerSecond(m.cachedLLMStats.TokenOutput, m.cachedLLMStats.TotalDurationNs)
	activeTotalTPS := ratePerSecond(m.cachedLLMStats.TokenInput+m.cachedLLMStats.TokenOutput, m.cachedLLMStats.TotalDurationNs)

	wallDurationNs := int64(0)
	if m.cachedLLMStats.FirstStartNs > 0 && m.cachedLLMStats.LastEndNs > m.cachedLLMStats.FirstStartNs {
		wallDurationNs = m.cachedLLMStats.LastEndNs - m.cachedLLMStats.FirstStartNs
	}
	wallInputTPS := ratePerSecond(m.cachedLLMStats.TokenInput, wallDurationNs)
	wallOutputTPS := ratePerSecond(m.cachedLLMStats.TokenOutput, wallDurationNs)
	wallTotalTPS := ratePerSecond(m.cachedLLMStats.TokenInput+m.cachedLLMStats.TokenOutput, wallDurationNs)

	lastInputTPS := ratePerSecond(m.cachedLLMStats.LastInputTokens, m.cachedLLMStats.LastDurationNs)
	lastOutputTPS := ratePerSecond(m.cachedLLMStats.LastOutputTokens, m.cachedLLMStats.LastDurationNs)
	lastTotalTPS := ratePerSecond(m.cachedLLMStats.LastTotalTokens, m.cachedLLMStats.LastDurationNs)

	recentInputTPS := ratePerSecond(m.cachedLLMStats.RecentWindowInput, m.cachedLLMStats.RecentWindowDurationNs)
	recentOutputTPS := ratePerSecond(m.cachedLLMStats.RecentWindowOutput, m.cachedLLMStats.RecentWindowDurationNs)
	recentTotalTPS := ratePerSecond(m.cachedLLMStats.RecentWindowTotal, m.cachedLLMStats.RecentWindowDurationNs)

	return LLMMetricsSnapshot{
		CallCount:                totalCalls,
		TokenInput:               m.cachedLLMStats.TokenInput,
		TokenOutput:              m.cachedLLMStats.TokenOutput,
		TokenTotal:               m.cachedLLMStats.TokenInput + m.cachedLLMStats.TokenOutput,
		CacheRead:                m.cachedLLMStats.CacheRead,
		CacheWrite:               m.cachedLLMStats.CacheWrite,
		ErrorCount:               m.cachedLLMStats.ErrorCount,
		RetryCount:               m.cachedLLMStats.RetryCount,
		ErrorRateLimitCount:      m.cachedLLMStats.ErrorRateLimitCount,
		ErrorTimeoutCount:        m.cachedLLMStats.ErrorTimeoutCount,
		ErrorContextLimitCount:   m.cachedLLMStats.ErrorContextLimitCount,
		ErrorNetworkCount:        m.cachedLLMStats.ErrorNetworkCount,
		ErrorServerCount:         m.cachedLLMStats.ErrorServerCount,
		ErrorClientCount:         m.cachedLLMStats.ErrorClientCount,
		ErrorCanceledCount:       m.cachedLLMStats.ErrorCanceledCount,
		ErrorUnknownCount:        m.cachedLLMStats.ErrorUnknownCount,
		LastErrorType:            m.cachedLLMStats.LastErrorType,
		LastErrorMessage:         m.cachedLLMStats.LastErrorMessage,
		LastErrorStatusCode:      int(m.cachedLLMStats.LastErrorStatusCode),
		LastErrorAt:              timeFromNs(m.cachedLLMStats.LastErrorAtNs),
		LastRetryAfter:           time.Duration(m.cachedLLMStats.LastRetryAfterNs),
		SuccessRate:              successRate,
		AvgTokensPerCall:         avgTokens,
		TotalDuration:            time.Duration(m.cachedLLMStats.TotalDurationNs),
		AvgDurationPerCall:       avgDurationPerCall,
		LastDuration:             time.Duration(m.cachedLLMStats.LastDurationNs),
		LastInputTokens:          m.cachedLLMStats.LastInputTokens,
		LastOutputTokens:         m.cachedLLMStats.LastOutputTokens,
		LastTotalTokens:          m.cachedLLMStats.LastTotalTokens,
		LastInputTokensPerSec:    lastInputTPS,
		LastOutputTokensPerSec:   lastOutputTPS,
		LastTotalTokensPerSec:    lastTotalTPS,
		ActiveInputTokensPerSec:  activeInputTPS,
		ActiveOutputTokensPerSec: activeOutputTPS,
		ActiveTotalTokensPerSec:  activeTotalTPS,
		WallDuration:             time.Duration(wallDurationNs),
		WallInputTokensPerSec:    wallInputTPS,
		WallOutputTokensPerSec:   wallOutputTPS,
		WallTotalTokensPerSec:    wallTotalTPS,
		RecentWindowSeconds:      m.cachedLLMStats.RecentWindowSeconds,
		RecentWindowInputTokens:  m.cachedLLMStats.RecentWindowInput,
		RecentWindowOutputTokens: m.cachedLLMStats.RecentWindowOutput,
		RecentWindowTotalTokens:  m.cachedLLMStats.RecentWindowTotal,
		RecentWindowDuration:     time.Duration(m.cachedLLMStats.RecentWindowDurationNs),
		RecentInputTokensPerSec:  recentInputTPS,
		RecentOutputTokensPerSec: recentOutputTPS,
		RecentTotalTokensPerSec:  recentTotalTPS,
		FirstTokenTotalDuration:  time.Duration(m.cachedLLMStats.FirstTokenTotalNs),
		LastFirstTokenDuration:   time.Duration(m.cachedLLMStats.LastFirstTokenNs),
		AvgFirstTokenDuration:    time.Duration(avgFirstTokenMs) * time.Millisecond,
		LastStart:                timeFromNs(m.cachedLLMStats.LastStartNs),
		LastEnd:                  timeFromNs(m.cachedLLMStats.LastEndNs),
	}
}

// GetPromptMetrics returns aggregated prompt metrics.
func (m *Metrics) GetPromptMetrics() PromptMetricsSnapshot {
	m.refreshAggregations()

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.promptMetricsSnapshotLocked()
}

func (m *Metrics) promptMetricsSnapshotLocked() PromptMetricsSnapshot {
	totalCalls := m.cachedPromptStats.CallCount
	successRate := 0.0
	if totalCalls > 0 {
		successRate = float64(totalCalls-m.cachedPromptStats.ErrorCount) / float64(totalCalls)
	}

	return PromptMetricsSnapshot{
		CallCount:     totalCalls,
		ErrorCount:    m.cachedPromptStats.ErrorCount,
		SuccessRate:   successRate,
		TotalDuration: time.Duration(m.cachedPromptStats.TotalDurationNs),
		AvgDuration:   safeDuration(m.cachedPromptStats.TotalDurationNs, totalCalls),
		LastStart:     timeFromNs(m.cachedPromptStats.LastStartNs),
		LastEnd:       timeFromNs(m.cachedPromptStats.LastEndNs),
	}
}

// GetMessageCounts returns aggregated message counts.
func (m *Metrics) GetMessageCounts() MessageCountsSnapshot {
	m.refreshAggregations()

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.messageCountsSnapshotLocked()
}

func (m *Metrics) messageCountsSnapshotLocked() MessageCountsSnapshot {
	return MessageCountsSnapshot{
		UserMessages:      m.cachedMessageStats.UserMessages,
		AssistantMessages: m.cachedMessageStats.AssistantMessages,
		ToolCalls:         m.cachedMessageStats.ToolCalls,
		ToolResults:       m.cachedMessageStats.ToolResults,
	}
}

func (m *Metrics) contextMetricsSnapshotLocked() ContextMetricsSnapshot {
	return ContextMetricsSnapshot{
		UpdateReminders:   m.cachedContextStats.UpdateReminders,
		DecisionReminders: m.cachedContextStats.DecisionReminders,
		TotalReminders:    m.cachedContextStats.UpdateReminders + m.cachedContextStats.DecisionReminders,
		LastReminderType:  m.cachedContextStats.LastReminderType,
		LastReminderAt:    timeFromNs(m.cachedContextStats.LastReminderAtNs),
	}
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
			SuccessRate:       safeRatio(cache.SuccessCount, cache.CallCount),
			AverageDurationMs: safeDurationMillis(cache.TotalDurationNs, cache.CallCount),
			LastCall:          cache.LastCall,
		})
	}

	return FullMetricsSnapshot{
		SessionUptime:  time.Since(m.sessionStart),
		ToolMetrics:    toolSnapshots,
		LLMMetrics:     m.llmMetricsSnapshotLocked(),
		MessageCounts:  m.messageCountsSnapshotLocked(),
		PromptMetrics:  m.promptMetricsSnapshotLocked(),
		ContextMetrics: m.contextMetricsSnapshotLocked(),
	}
}

// RecordTraceEvent records a trace event to the buffer and invalidates cache.
// This is called by the TraceBuf sink when new events are recorded.
// For direct recording (e.g., in tests), also records to the buffer.
func (m *Metrics) RecordTraceEvent(event traceevent.TraceEvent) {
	m.buf.Record(event)
	m.InvalidateCache()
}

// Helper functions
