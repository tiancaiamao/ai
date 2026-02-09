package agent

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Metric collection intervals
	defaultMetricsFlushInterval = 5 * time.Second
	maxMetricsBufferSize          = 1000
)

// Metrics collects performance and usage statistics.
type Metrics struct {
	mu                    sync.RWMutex
	toolExecutions        map[string]*ToolMetrics
	llmCalls              *LLMMetrics
	messageCounts        *MessageCounts
	sessionStart           time.Time
	lastFlush              time.Time
	flushInterval         time.Duration
}

// ToolMetrics tracks statistics for a specific tool.
type ToolMetrics struct {
	Name              string
	CallCount        atomic.Int64
	SuccessCount     atomic.Int64
	FailCount        atomic.Int64
	TotalDuration     time.Duration
	RetryCount       atomic.Int64
	LastCall         time.Time
	AverageDuration   atomic.Int64 // in milliseconds
}

// LLMMetrics tracks LLM API statistics.
type LLMMetrics struct {
	CallCount          atomic.Int64
	TokenInput        atomic.Int64
	TokenOutput       atomic.Int64
	CacheRead         atomic.Int64
	CacheWrite        atomic.Int64
	ErrorCount         atomic.Int64
	AvgTokensPerCall  atomic.Int64
	TotalDuration      time.Duration
}

// MessageCounts tracks message statistics.
type MessageCounts struct {
	UserMessages       atomic.Int64
	AssistantMessages atomic.Int64
	ToolCalls         atomic.Int64
	ToolResults       atomic.Int64
}

// NewMetrics creates a new metrics collector.
func NewMetrics() *Metrics {
	return &Metrics{
		toolExecutions: make(map[string]*ToolMetrics),
		llmCalls:      &LLMMetrics{},
		messageCounts: &MessageCounts{},
		sessionStart:   time.Now(),
		flushInterval:  defaultMetricsFlushInterval,
	}
}

// RecordToolExecution records a tool execution.
func (m *Metrics) RecordToolExecution(toolName string, duration time.Duration, err error, retryCount int) {
	m.mu.Lock()
	tool, ok := m.toolExecutions[toolName]
	if !ok {
		tool = &ToolMetrics{Name: toolName}
		m.toolExecutions[toolName] = tool
	}
	m.mu.Unlock()

	tool.CallCount.Add(1)
	tool.LastCall = time.Now()
	tool.TotalDuration += duration

	if err != nil {
		tool.FailCount.Add(1)
		tool.RetryCount.Add(int64(retryCount))
	} else {
		tool.SuccessCount.Add(1)
	}

	// Update average duration (in milliseconds)
	totalCalls := tool.CallCount.Load()
	if totalCalls > 0 {
		avgMs := tool.TotalDuration.Milliseconds() / int64(totalCalls)
		tool.AverageDuration.Store(avgMs)
	}
}

// RecordLLMCall records an LLM API call.
func (m *Metrics) RecordLLMCall(inputTokens, outputTokens, cacheRead, cacheWrite int, duration time.Duration, err error) {
	m.llmCalls.CallCount.Add(1)
	m.llmCalls.TokenInput.Add(int64(inputTokens))
	m.llmCalls.TokenOutput.Add(int64(outputTokens))
	m.llmCalls.CacheRead.Add(int64(cacheRead))
	m.llmCalls.CacheWrite.Add(int64(cacheWrite))
	m.llmCalls.TotalDuration += duration

	if err != nil {
		m.llmCalls.ErrorCount.Add(1)
	}

	// Update average tokens per call
	totalCalls := m.llmCalls.CallCount.Load()
	if totalCalls > 0 {
		totalTokens := m.llmCalls.TokenInput.Load() + m.llmCalls.TokenOutput.Load()
		avgTokens := totalTokens / totalCalls
		m.llmCalls.AvgTokensPerCall.Store(avgTokens)
	}
}

// RecordMessage records a message.
func (m *Metrics) RecordMessage(role string) {
	switch role {
	case "user":
		m.messageCounts.UserMessages.Add(1)
	case "assistant":
		m.messageCounts.AssistantMessages.Add(1)
	}
}

// RecordToolCall records a tool call from assistant.
func (m *Metrics) RecordToolCall() {
	m.messageCounts.ToolCalls.Add(1)
}

// RecordToolResult records a tool result.
func (m *Metrics) RecordToolResult() {
	m.messageCounts.ToolResults.Add(1)
}

// GetToolMetrics returns metrics for a specific tool.
func (m *Metrics) GetToolMetrics(toolName string) ToolMetricsSnapshot {
	m.mu.RLock()
	tool, ok := m.toolExecutions[toolName]
	m.mu.RUnlock()

	if !ok {
		return ToolMetricsSnapshot{}
	}

	return ToolMetricsSnapshot{
		Name:              tool.Name,
		CallCount:         tool.CallCount.Load(),
		SuccessCount:       tool.SuccessCount.Load(),
		FailCount:          tool.FailCount.Load(),
		SuccessRate:       float64(tool.SuccessCount.Load()) / float64(tool.CallCount.Load()),
		AverageDurationMs: tool.AverageDuration.Load(),
		RetryCount:         tool.RetryCount.Load(),
		LastCall:           tool.LastCall,
	}
}

// GetLLMMetrics returns LLM metrics snapshot.
func (m *Metrics) GetLLMMetrics() LLMMetricsSnapshot {
	totalCalls := m.llmCalls.CallCount.Load()
	successRate := 0.0
	if totalCalls > 0 {
		successRate = float64(totalCalls-m.llmCalls.ErrorCount.Load()) / float64(totalCalls)
	}

	return LLMMetricsSnapshot{
		CallCount:          totalCalls,
		TokenInput:         m.llmCalls.TokenInput.Load(),
		TokenOutput:        m.llmCalls.TokenOutput.Load(),
		CacheRead:          m.llmCalls.CacheRead.Load(),
		CacheWrite:         m.llmCalls.CacheWrite.Load(),
		ErrorCount:          m.llmCalls.ErrorCount.Load(),
		SuccessRate:         successRate,
		AvgTokensPerCall:    m.llmCalls.AvgTokensPerCall.Load(),
		TotalDuration:       m.llmCalls.TotalDuration,
		AvgDurationPerCall:  m.avgDurationPerCall(totalCalls, m.llmCalls.TotalDuration),
	}
}

// GetMessageCounts returns message count snapshot.
func (m *Metrics) GetMessageCounts() MessageCountsSnapshot {
	return MessageCountsSnapshot{
		UserMessages:       m.messageCounts.UserMessages.Load(),
		AssistantMessages: m.messageCounts.AssistantMessages.Load(),
		ToolCalls:         m.messageCounts.ToolCalls.Load(),
		ToolResults:       m.messageCounts.ToolResults.Load(),
	}
}

// GetSessionUptime returns the session uptime.
func (m *Metrics) GetSessionUptime() time.Duration {
	return time.Since(m.sessionStart)
}

// Reset resets all metrics.
func (m *Metrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.toolExecutions = make(map[string]*ToolMetrics)
	m.llmCalls = &LLMMetrics{}
	m.messageCounts = &MessageCounts{}
	m.sessionStart = time.Now()
}

// avgDurationPerCall calculates average duration per LLM call.
func (m *Metrics) avgDurationPerCall(totalCalls int64, totalDuration time.Duration) time.Duration {
	if totalCalls == 0 {
		return 0
	}
	return totalDuration / time.Duration(totalCalls)
}

// ToolMetricsSnapshot represents a snapshot of tool metrics.
type ToolMetricsSnapshot struct {
	Name              string    `json:"name"`
	CallCount         int64     `json:"callCount"`
	SuccessCount       int64     `json:"successCount"`
	FailCount          int64     `json:"failCount"`
	SuccessRate       float64   `json:"successRate"`
	AverageDurationMs int64     `json:"averageDurationMs"`
	RetryCount         int64     `json:"retryCount"`
	LastCall           time.Time  `json:"lastCall"`
}

// LLMMetricsSnapshot represents a snapshot of LLM metrics.
type LLMMetricsSnapshot struct {
	CallCount           int64         `json:"callCount"`
	TokenInput          int64         `json:"tokenInput"`
	TokenOutput         int64         `json:"tokenOutput"`
	CacheRead           int64         `json:"cacheRead"`
	CacheWrite          int64         `json:"cacheWrite"`
	ErrorCount          int64         `json:"errorCount"`
	SuccessRate         float64       `json:"successRate"`
	AvgTokensPerCall    int64         `json:"avgTokensPerCall"`
	TotalDuration       time.Duration  `json:"totalDuration"`
	AvgDurationPerCall  time.Duration  `json:"avgDurationPerCall"`
}

// MessageCountsSnapshot represents a snapshot of message counts.
type MessageCountsSnapshot struct {
	UserMessages       int64 `json:"userMessages"`
	AssistantMessages int64 `json:"assistantMessages"`
	ToolCalls         int64 `json:"toolCalls"`
	ToolResults       int64 `json:"toolResults"`
}

// GetFullMetrics returns a complete metrics snapshot.
func (m *Metrics) GetFullMetrics() FullMetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	toolSnapshots := make([]ToolMetricsSnapshot, 0, len(m.toolExecutions))
	i := 0
	for _, tool := range m.toolExecutions {
		toolSnapshots[i] = m.GetToolMetrics(tool.Name)
		i++
	}

	return FullMetricsSnapshot{
		SessionUptime:  m.GetSessionUptime(),
		ToolMetrics:    toolSnapshots,
		LLMMetrics:     m.GetLLMMetrics(),
		MessageCounts:  m.GetMessageCounts(),
	}
}

// FullMetricsSnapshot represents a complete metrics snapshot.
type FullMetricsSnapshot struct {
	SessionUptime  time.Duration            `json:"sessionUptime"`
	ToolMetrics    []ToolMetricsSnapshot      `json:"toolMetrics"`
	LLMMetrics     LLMMetricsSnapshot         `json:"llmMetrics"`
	MessageCounts MessageCountsSnapshot         `json:"messageCounts"`
}
