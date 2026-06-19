package agent

import (
	"fmt"
	"time"

	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// aggregateEvent incorporates a single trace event into the aggregation.
func (m *Metrics) aggregateEvent(event traceevent.TraceEvent) {
	switch canonicalTraceName(event.Name) {
	case "llm_call":
		m.aggregateLLM(event)
	case "tool_execution":
		m.aggregateTool(event)
	case "message_end":
		m.aggregateMessage(event)
	}
}

// aggregateLLM processes LLM-related events
func (m *Metrics) aggregateLLM(event traceevent.TraceEvent) {
	if event.Phase == traceevent.PhaseEnd {
		inputTokens := traceFieldInt64(event.Fields, "input_tokens")
		outputTokens := traceFieldInt64(event.Fields, "output_tokens")
		totalTokens := traceFieldInt64(event.Fields, "total_tokens")
		if totalTokens == 0 {
			totalTokens = inputTokens + outputTokens
		}
		m.cachedLLMStats.CallCount++
		m.cachedLLMStats.TokenInput += inputTokens
		m.cachedLLMStats.TokenOutput += outputTokens
		m.cachedLLMStats.CacheRead += traceFieldInt64(event.Fields, "cache_read")
		m.cachedLLMStats.CacheWrite += traceFieldInt64(event.Fields, "cache_write")
		durationMs := traceFieldInt64(event.Fields, "duration_ms")
		durationNs := durationMs * 1_000_000
		m.cachedLLMStats.TotalDurationNs += durationNs
		m.cachedLLMStats.LastEndNs = event.Timestamp.UnixNano()
		m.cachedLLMStats.LastDurationNs = durationNs
		m.cachedLLMStats.LastInputTokens = inputTokens
		m.cachedLLMStats.LastOutputTokens = outputTokens
		m.cachedLLMStats.LastTotalTokens = totalTokens
		if traceFieldInt64(event.Fields, "attempt") > 0 {
			m.cachedLLMStats.RetryCount++
		}
		m.cachedLLMStats.samples = append(m.cachedLLMStats.samples, llmTokenSample{
			endNs:        event.Timestamp.UnixNano(),
			inputTokens:  inputTokens,
			outputTokens: outputTokens,
			totalTokens:  totalTokens,
		})

		firstTokenMs := traceFieldInt64(event.Fields, "first_token_ms")
		if firstTokenMs > 0 {
			m.cachedLLMStats.FirstTokenTotalNs += firstTokenMs * 1_000_000
			m.cachedLLMStats.LastFirstTokenNs = firstTokenMs * 1_000_000
		}

		if errMessage := traceFieldString(event.Fields, "error"); errMessage != "" {
			m.cachedLLMStats.ErrorCount++
			errType := normalizeLLMErrorType(traceFieldString(event.Fields, "error_type"))
			if errType == llmErrorTypeUnknown {
				errType = inferLLMErrorTypeFromMessage(errMessage)
			}
			switch errType {
			case llmErrorTypeRateLimit:
				m.cachedLLMStats.ErrorRateLimitCount++
			case llmErrorTypeTimeout:
				m.cachedLLMStats.ErrorTimeoutCount++
			case llmErrorTypeContextLimit:
				m.cachedLLMStats.ErrorContextLimitCount++
			case llmErrorTypeNetwork:
				m.cachedLLMStats.ErrorNetworkCount++
			case llmErrorTypeServer:
				m.cachedLLMStats.ErrorServerCount++
			case llmErrorTypeClient:
				m.cachedLLMStats.ErrorClientCount++
			case llmErrorTypeCanceled:
				m.cachedLLMStats.ErrorCanceledCount++
			default:
				m.cachedLLMStats.ErrorUnknownCount++
			}
			m.cachedLLMStats.LastErrorType = errType
			m.cachedLLMStats.LastErrorMessage = errMessage
			m.cachedLLMStats.LastErrorStatusCode = traceFieldInt64(event.Fields, "error_status_code")
			m.cachedLLMStats.LastErrorAtNs = event.Timestamp.UnixNano()
			if retryAfterMs := traceFieldInt64(event.Fields, "retry_after_ms"); retryAfterMs > 0 {
				m.cachedLLMStats.LastRetryAfterNs = retryAfterMs * 1_000_000
			}
		}
	}
	if event.Phase == traceevent.PhaseBegin {
		startNs := event.Timestamp.UnixNano()
		m.cachedLLMStats.LastStartNs = startNs
		if m.cachedLLMStats.FirstStartNs == 0 || startNs < m.cachedLLMStats.FirstStartNs {
			m.cachedLLMStats.FirstStartNs = startNs
		}
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

// Helper functions

func canonicalTraceName(name string) string {
	switch name {
	case "llm_call_start", "llm_call_end":
		return "llm_call"
	case "tool_execution_start", "tool_execution_end":
		return "tool_execution"
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

func safeRatio(num, den int64) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func safeDuration(totalNs, calls int64) time.Duration {
	if calls <= 0 {
		return 0
	}
	return time.Duration(totalNs / calls)
}

func safeDurationMillis(totalNs, calls int64) int64 {
	if calls <= 0 {
		return 0
	}
	return totalNs / calls / 1_000_000
}

func ratePerSecond(tokens, durationNs int64) float64 {
	if tokens <= 0 || durationNs <= 0 {
		return 0
	}
	return float64(tokens) * float64(time.Second) / float64(durationNs)
}

// Snapshot types (exported for RPC handlers)
