package agent

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

const (
	// Metric collection intervals
	defaultMetricsFlushInterval = 5 * time.Second
	maxMetricsBufferSize        = 1000
)

// Metrics collects performance and usage statistics.
type Metrics struct {
	mu                sync.RWMutex
	toolExecutions    map[string]*ToolMetrics
	llmCalls          *LLMMetrics
	messageCounts     *MessageCounts
	promptCalls       *PromptMetrics
	promptInFlight    atomic.Int64
	promptStartNs     atomic.Int64
	lastPromptStartNs atomic.Int64
	lastPromptEndNs   atomic.Int64
	sessionStart      time.Time
	lastFlush         time.Time
	flushInterval     time.Duration
}

// ToolMetrics tracks statistics for a specific tool.
type ToolMetrics struct {
	Name            string
	CallCount       atomic.Int64
	SuccessCount    atomic.Int64
	FailCount       atomic.Int64
	TotalDuration   time.Duration
	RetryCount      atomic.Int64
	LastCall        time.Time
	AverageDuration atomic.Int64 // in milliseconds
}

// LLMMetrics tracks LLM API statistics.
type LLMMetrics struct {
	CallCount         atomic.Int64
	TokenInput        atomic.Int64
	TokenOutput       atomic.Int64
	CacheRead         atomic.Int64
	CacheWrite        atomic.Int64
	ErrorCount        atomic.Int64
	AvgTokensPerCall  atomic.Int64
	TotalDuration     time.Duration
	FirstTokenTotalNs atomic.Int64
	FirstTokenLastNs  atomic.Int64
	AvgFirstTokenMs   atomic.Int64
	InFlight          atomic.Int64
	StartNs           atomic.Int64
	LastStartNs       atomic.Int64
	LastEndNs         atomic.Int64
}

// MessageCounts tracks message statistics.
type MessageCounts struct {
	UserMessages      atomic.Int64
	AssistantMessages atomic.Int64
	ToolCalls         atomic.Int64
	ToolResults       atomic.Int64
}

// PromptMetrics tracks end-to-end prompt timing.
type PromptMetrics struct {
	CallCount       atomic.Int64
	ErrorCount      atomic.Int64
	TotalDurationNs atomic.Int64
	LastDurationNs  atomic.Int64
	AvgDurationMs   atomic.Int64
}

// NewMetrics creates a new metrics collector.
func NewMetrics() *Metrics {
	return &Metrics{
		toolExecutions: make(map[string]*ToolMetrics),
		llmCalls:       &LLMMetrics{},
		messageCounts:  &MessageCounts{},
		promptCalls:    &PromptMetrics{},
		sessionStart:   time.Now(),
		flushInterval:  defaultMetricsFlushInterval,
	}
}

// RecordLLMStart marks LLM call as in-flight.
func (m *Metrics) RecordLLMStart() {
	nowNs := time.Now().UnixNano()
	m.llmCalls.InFlight.Store(1)
	m.llmCalls.StartNs.Store(nowNs)
	m.llmCalls.LastStartNs.Store(nowNs)
}

// RecordPromptStart marks prompt as in-flight.
func (m *Metrics) RecordPromptStart() {
	nowNs := time.Now().UnixNano()
	m.promptInFlight.Store(1)
	m.promptStartNs.Store(nowNs)
	m.lastPromptStartNs.Store(nowNs)
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
func (m *Metrics) RecordLLMCall(inputTokens, outputTokens, cacheRead, cacheWrite int, duration, firstToken time.Duration, err error) {
	m.llmCalls.CallCount.Add(1)
	m.llmCalls.TokenInput.Add(int64(inputTokens))
	m.llmCalls.TokenOutput.Add(int64(outputTokens))
	m.llmCalls.CacheRead.Add(int64(cacheRead))
	m.llmCalls.CacheWrite.Add(int64(cacheWrite))
	m.llmCalls.TotalDuration += duration
	if firstToken > 0 {
		totalFirst := m.llmCalls.FirstTokenTotalNs.Add(firstToken.Nanoseconds())
		m.llmCalls.FirstTokenLastNs.Store(firstToken.Nanoseconds())
		totalCalls := m.llmCalls.CallCount.Load()
		if totalCalls > 0 {
			avgMs := (totalFirst / totalCalls) / int64(time.Millisecond)
			m.llmCalls.AvgFirstTokenMs.Store(avgMs)
		}
	}

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

	m.llmCalls.InFlight.Store(0)
	m.llmCalls.LastEndNs.Store(time.Now().UnixNano())
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

// RecordPrompt records end-to-end prompt timing.
func (m *Metrics) RecordPrompt(duration time.Duration, err error) {
	if m.promptCalls == nil {
		return
	}
	m.promptCalls.CallCount.Add(1)
	if err != nil {
		m.promptCalls.ErrorCount.Add(1)
	}
	total := m.promptCalls.TotalDurationNs.Add(duration.Nanoseconds())
	m.promptCalls.LastDurationNs.Store(duration.Nanoseconds())

	totalCalls := m.promptCalls.CallCount.Load()
	if totalCalls > 0 {
		avgMs := (total / totalCalls) / int64(time.Millisecond)
		m.promptCalls.AvgDurationMs.Store(avgMs)
	}

	m.promptInFlight.Store(0)
	m.lastPromptEndNs.Store(time.Now().UnixNano())
}

// RecordTraceEvent derives metrics from a unified trace event stream.
func (m *Metrics) RecordTraceEvent(event traceevent.TraceEvent) {
	switch canonicalTraceName(event.Name) {
	case "prompt":
		if event.Phase == traceevent.PhaseBegin {
			nowNs := event.Timestamp.UnixNano()
			m.promptInFlight.Store(1)
			m.promptStartNs.Store(nowNs)
			m.lastPromptStartNs.Store(nowNs)
		}
		if event.Phase == traceevent.PhaseEnd {
			durationMs := traceFieldInt64(event.Fields, "duration_ms")
			errFlag := traceFieldBool(event.Fields, "error")
			var err error
			if errFlag {
				err = errors.New("prompt failed")
			}
			m.RecordPrompt(time.Duration(durationMs)*time.Millisecond, err)
		}
	case "llm_call":
		if event.Phase == traceevent.PhaseBegin {
			nowNs := event.Timestamp.UnixNano()
			m.llmCalls.InFlight.Store(1)
			m.llmCalls.StartNs.Store(nowNs)
			m.llmCalls.LastStartNs.Store(nowNs)
		}
		if event.Phase == traceevent.PhaseEnd {
			inputTokens := int(traceFieldInt64(event.Fields, "input_tokens"))
			outputTokens := int(traceFieldInt64(event.Fields, "output_tokens"))
			cacheRead := int(traceFieldInt64(event.Fields, "cache_read"))
			cacheWrite := int(traceFieldInt64(event.Fields, "cache_write"))
			durationMs := traceFieldInt64(event.Fields, "duration_ms")
			firstTokenMs := traceFieldInt64(event.Fields, "first_token_ms")
			errValue := traceFieldString(event.Fields, "error")
			var err error
			if errValue != "" {
				err = errors.New(errValue)
			}
			m.RecordLLMCall(
				inputTokens,
				outputTokens,
				cacheRead,
				cacheWrite,
				time.Duration(durationMs)*time.Millisecond,
				time.Duration(firstTokenMs)*time.Millisecond,
				err,
			)
		}
	case "tool_execution":
		if event.Phase == traceevent.PhaseBegin {
			m.messageCounts.ToolCalls.Add(1)
		}
		if event.Phase == traceevent.PhaseEnd {
			m.messageCounts.ToolResults.Add(1)
			toolName := traceFieldString(event.Fields, "tool")
			if toolName == "" {
				toolName = "unknown"
			}
			durationMs := traceFieldInt64(event.Fields, "duration_ms")
			errFlag := traceFieldBool(event.Fields, "error")
			errMsg := traceFieldString(event.Fields, "error_message")
			var err error
			if errFlag {
				if errMsg == "" {
					errMsg = "tool execution failed"
				}
				err = errors.New(errMsg)
			}
			m.RecordToolExecution(toolName, time.Duration(durationMs)*time.Millisecond, err, 0)
		}
	case "message_end":
		if event.Phase == traceevent.PhaseInstant {
			role := traceFieldString(event.Fields, "role")
			if role != "" {
				m.RecordMessage(role)
			}
		}
	}
}

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
		SuccessCount:      tool.SuccessCount.Load(),
		FailCount:         tool.FailCount.Load(),
		SuccessRate:       float64(tool.SuccessCount.Load()) / float64(tool.CallCount.Load()),
		AverageDurationMs: tool.AverageDuration.Load(),
		RetryCount:        tool.RetryCount.Load(),
		LastCall:          tool.LastCall,
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
		CallCount:               totalCalls,
		TokenInput:              m.llmCalls.TokenInput.Load(),
		TokenOutput:             m.llmCalls.TokenOutput.Load(),
		CacheRead:               m.llmCalls.CacheRead.Load(),
		CacheWrite:              m.llmCalls.CacheWrite.Load(),
		ErrorCount:              m.llmCalls.ErrorCount.Load(),
		SuccessRate:             successRate,
		AvgTokensPerCall:        m.llmCalls.AvgTokensPerCall.Load(),
		TotalDuration:           m.llmCalls.TotalDuration,
		AvgDurationPerCall:      m.avgDurationPerCall(totalCalls, m.llmCalls.TotalDuration),
		FirstTokenTotalDuration: time.Duration(m.llmCalls.FirstTokenTotalNs.Load()),
		LastFirstTokenDuration:  time.Duration(m.llmCalls.FirstTokenLastNs.Load()),
		AvgFirstTokenDuration:   time.Duration(m.llmCalls.AvgFirstTokenMs.Load()) * time.Millisecond,
		InFlight:                m.llmCalls.InFlight.Load() > 0,
		InFlightDuration:        inFlightDuration(m.llmCalls.StartNs.Load()),
		LastStart:               timeFromNs(m.llmCalls.LastStartNs.Load()),
		LastEnd:                 timeFromNs(m.llmCalls.LastEndNs.Load()),
	}
}

// GetMessageCounts returns message count snapshot.
func (m *Metrics) GetMessageCounts() MessageCountsSnapshot {
	return MessageCountsSnapshot{
		UserMessages:      m.messageCounts.UserMessages.Load(),
		AssistantMessages: m.messageCounts.AssistantMessages.Load(),
		ToolCalls:         m.messageCounts.ToolCalls.Load(),
		ToolResults:       m.messageCounts.ToolResults.Load(),
	}
}

// GetPromptMetrics returns prompt timing metrics snapshot.
func (m *Metrics) GetPromptMetrics() PromptMetricsSnapshot {
	if m.promptCalls == nil {
		return PromptMetricsSnapshot{}
	}
	totalCalls := m.promptCalls.CallCount.Load()
	successRate := 0.0
	if totalCalls > 0 {
		successRate = float64(totalCalls-m.promptCalls.ErrorCount.Load()) / float64(totalCalls)
	}
	totalDuration := time.Duration(m.promptCalls.TotalDurationNs.Load())
	lastDuration := time.Duration(m.promptCalls.LastDurationNs.Load())
	avgDuration := time.Duration(m.promptCalls.AvgDurationMs.Load()) * time.Millisecond
	inFlight := m.promptInFlight.Load() > 0
	startNs := m.promptStartNs.Load()
	inFlightDuration := time.Duration(0)
	if inFlight && startNs > 0 {
		inFlightDuration = time.Since(time.Unix(0, startNs))
	}
	lastStartNs := m.lastPromptStartNs.Load()
	lastEndNs := m.lastPromptEndNs.Load()
	lastStart := time.Time{}
	if lastStartNs > 0 {
		lastStart = time.Unix(0, lastStartNs)
	}
	lastEnd := time.Time{}
	if lastEndNs > 0 {
		lastEnd = time.Unix(0, lastEndNs)
	}
	return PromptMetricsSnapshot{
		CallCount:        totalCalls,
		ErrorCount:       m.promptCalls.ErrorCount.Load(),
		SuccessRate:      successRate,
		TotalDuration:    totalDuration,
		LastDuration:     lastDuration,
		AvgDuration:      avgDuration,
		InFlight:         inFlight,
		InFlightDuration: inFlightDuration,
		LastStart:        lastStart,
		LastEnd:          lastEnd,
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
	m.promptCalls = &PromptMetrics{}
	m.promptInFlight.Store(0)
	m.promptStartNs.Store(0)
	m.lastPromptStartNs.Store(0)
	m.lastPromptEndNs.Store(0)
	m.llmCalls.InFlight.Store(0)
	m.llmCalls.StartNs.Store(0)
	m.llmCalls.LastStartNs.Store(0)
	m.llmCalls.LastEndNs.Store(0)
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
	SuccessCount      int64     `json:"successCount"`
	FailCount         int64     `json:"failCount"`
	SuccessRate       float64   `json:"successRate"`
	AverageDurationMs int64     `json:"averageDurationMs"`
	RetryCount        int64     `json:"retryCount"`
	LastCall          time.Time `json:"lastCall"`
}

// LLMMetricsSnapshot represents a snapshot of LLM metrics.
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

// MessageCountsSnapshot represents a snapshot of message counts.
type MessageCountsSnapshot struct {
	UserMessages      int64 `json:"userMessages"`
	AssistantMessages int64 `json:"assistantMessages"`
	ToolCalls         int64 `json:"toolCalls"`
	ToolResults       int64 `json:"toolResults"`
}

// PromptMetricsSnapshot represents a snapshot of prompt timing metrics.
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

// GetFullMetrics returns a complete metrics snapshot.
func (m *Metrics) GetFullMetrics() FullMetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	toolSnapshots := make([]ToolMetricsSnapshot, 0, len(m.toolExecutions))
	for _, tool := range m.toolExecutions {
		toolSnapshots = append(toolSnapshots, m.GetToolMetrics(tool.Name))
	}

	return FullMetricsSnapshot{
		SessionUptime: m.GetSessionUptime(),
		ToolMetrics:   toolSnapshots,
		LLMMetrics:    m.GetLLMMetrics(),
		MessageCounts: m.GetMessageCounts(),
		PromptMetrics: m.GetPromptMetrics(),
	}
}

// FullMetricsSnapshot represents a complete metrics snapshot.
type FullMetricsSnapshot struct {
	SessionUptime time.Duration         `json:"sessionUptime"`
	ToolMetrics   []ToolMetricsSnapshot `json:"toolMetrics"`
	LLMMetrics    LLMMetricsSnapshot    `json:"llmMetrics"`
	MessageCounts MessageCountsSnapshot `json:"messageCounts"`
	PromptMetrics PromptMetricsSnapshot `json:"promptMetrics"`
}

func timeFromNs(ns int64) time.Time {
	if ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

func inFlightDuration(startNs int64) time.Duration {
	if startNs <= 0 {
		return 0
	}
	return time.Since(time.Unix(0, startNs))
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
			_, _ = fmt.Sscan(v, &parsed)
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
