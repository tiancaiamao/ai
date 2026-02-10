package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// MetricsHandler provides HTTP endpoints for metrics.
type MetricsHandler struct {
	metrics *agent.Metrics
}

// NewMetricsHandler creates a new metrics HTTP handler.
func NewMetricsHandler(metrics *agent.Metrics) *MetricsHandler {
	return &MetricsHandler{metrics: metrics}
}

// RegisterRoutes registers metrics endpoints with HTTP mux.
func (h *MetricsHandler) RegisterRoutes(mux *http.ServeMux) {
	// Metrics overview page
	mux.HandleFunc("/metrics", h.handleMetrics)
	mux.HandleFunc("/metrics/", h.handleMetrics)

	// Individual metric endpoints
	mux.HandleFunc("/metrics/tools", h.handleToolMetrics)
	mux.HandleFunc("/metrics/llm", h.handleLLMMetrics)
	mux.HandleFunc("/metrics/messages", h.handleMessageMetrics)
	mux.HandleFunc("/metrics/session", h.handleSessionMetrics)
	mux.HandleFunc("/metrics/health", h.handleHealth)

	// Prometheus-style metrics (text format)
	mux.HandleFunc("/metrics/prometheus", h.handlePrometheus)
}

// handleMetrics returns to full metrics snapshot as JSON.
func (h *MetricsHandler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	metrics := h.metrics.GetFullMetrics()
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleToolMetrics returns tool-specific metrics as JSON.
func (h *MetricsHandler) handleToolMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Get query parameter for specific tool
	toolName := r.URL.Query().Get("tool")
	if toolName != "" {
		toolMetrics := h.metrics.GetToolMetrics(toolName)
		if err := json.NewEncoder(w).Encode(toolMetrics); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// Return all tool metrics
	fullMetrics := h.metrics.GetFullMetrics()
	toolsJSON, err := json.MarshalIndent(fullMetrics.ToolMetrics, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(toolsJSON)
}

// handleLLMMetrics returns LLM metrics as JSON.
func (h *MetricsHandler) handleLLMMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	llmMetrics := h.metrics.GetLLMMetrics()
	if err := json.NewEncoder(w).Encode(llmMetrics); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleMessageMetrics returns message count metrics as JSON.
func (h *MetricsHandler) handleMessageMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	msgMetrics := h.metrics.GetMessageCounts()
	if err := json.NewEncoder(w).Encode(msgMetrics); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleSessionMetrics returns session-level metrics as JSON.
func (h *MetricsHandler) handleSessionMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	sessionMetrics := struct {
		Uptime       time.Duration `json:"uptime"`
		SessionStart time.Time     `json:"sessionStart"`
	}{
		Uptime:       h.metrics.GetSessionUptime(),
		SessionStart: time.Now().Add(-h.metrics.GetSessionUptime()),
	}

	if err := json.NewEncoder(w).Encode(sessionMetrics); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleHealth returns a simple health check with basic metrics.
func (h *MetricsHandler) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	llmMetrics := h.metrics.GetLLMMetrics()
	uptime := h.metrics.GetSessionUptime()

	health := struct {
		Status   string        `json:"status"`
		Uptime   time.Duration `json:"uptime"`
		LLMCalls int64         `json:"llmCalls"`
		LLErrors int64         `json:"llmErrors"`
	}{
		Status:   "healthy",
		Uptime:   uptime,
		LLMCalls: llmMetrics.CallCount,
		LLErrors: llmMetrics.ErrorCount,
	}

	if err := json.NewEncoder(w).Encode(health); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handlePrometheus returns metrics in Prometheus text format.
func (h *MetricsHandler) handlePrometheus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	fullMetrics := h.metrics.GetFullMetrics()

	// Write session metrics
	fmt.Fprintf(w, "# HELP ai_uptime_seconds Session uptime in seconds\n")
	fmt.Fprintf(w, "# TYPE ai_uptime_seconds gauge\n")
	fmt.Fprintf(w, "ai_uptime_seconds %.2f\n", fullMetrics.SessionUptime.Seconds())

	fmt.Fprintf(w, "\n")

	// Write message metrics
	fmt.Fprintf(w, "# HELP ai_messages_total Total number of messages by type\n")
	fmt.Fprintf(w, "# TYPE ai_messages_total counter\n")
	fmt.Fprintf(w, "ai_messages_total{type=\"user\"} %d\n", fullMetrics.MessageCounts.UserMessages)
	fmt.Fprintf(w, "ai_messages_total{type=\"assistant\"} %d\n", fullMetrics.MessageCounts.AssistantMessages)
	fmt.Fprintf(w, "ai_messages_total{type=\"tool_call\"} %d\n", fullMetrics.MessageCounts.ToolCalls)
	fmt.Fprintf(w, "ai_messages_total{type=\"tool_result\"} %d\n", fullMetrics.MessageCounts.ToolResults)

	fmt.Fprintf(w, "\n")

	// Write LLM metrics
	fmt.Fprintf(w, "# HELP ai_llm_calls_total Total number of LLM API calls\n")
	fmt.Fprintf(w, "# TYPE ai_llm_calls_total counter\n")
	fmt.Fprintf(w, "ai_llm_calls_total %d\n", fullMetrics.LLMMetrics.CallCount)

	fmt.Fprintf(w, "\n# HELP ai_llm_tokens_total Total number of LLM tokens\n")
	fmt.Fprintf(w, "# TYPE ai_llm_tokens_total counter\n")
	fmt.Fprintf(w, "ai_llm_tokens_total{type=\"input\"} %d\n", fullMetrics.LLMMetrics.TokenInput)
	fmt.Fprintf(w, "ai_llm_tokens_total{type=\"output\"} %d\n", fullMetrics.LLMMetrics.TokenOutput)
	fmt.Fprintf(w, "ai_llm_tokens_total{type=\"cache_read\"} %d\n", fullMetrics.LLMMetrics.CacheRead)
	fmt.Fprintf(w, "ai_llm_tokens_total{type=\"cache_write\"} %d\n", fullMetrics.LLMMetrics.CacheWrite)

	fmt.Fprintf(w, "\n# HELP ai_llm_duration_seconds LLM API call duration in seconds\n")
	fmt.Fprintf(w, "# TYPE ai_llm_duration_seconds histogram\n")
	fmt.Fprintf(w, "ai_llm_duration_seconds_sum %.3f\n", fullMetrics.LLMMetrics.TotalDuration.Seconds())
	fmt.Fprintf(w, "ai_llm_duration_seconds_count %d\n", fullMetrics.LLMMetrics.CallCount)

	fmt.Fprintf(w, "\n# HELP ai_llm_first_token_seconds Time to first token in seconds\n")
	fmt.Fprintf(w, "# TYPE ai_llm_first_token_seconds histogram\n")
	fmt.Fprintf(w, "ai_llm_first_token_seconds_sum %.3f\n", fullMetrics.LLMMetrics.FirstTokenTotalDuration.Seconds())
	fmt.Fprintf(w, "ai_llm_first_token_seconds_count %d\n", fullMetrics.LLMMetrics.CallCount)
	fmt.Fprintf(w, "ai_llm_first_token_last_seconds %.3f\n", fullMetrics.LLMMetrics.LastFirstTokenDuration.Seconds())

	fmt.Fprintf(w, "\n# HELP ai_llm_in_flight LLM call in flight (1/0)\n")
	fmt.Fprintf(w, "# TYPE ai_llm_in_flight gauge\n")
	if fullMetrics.LLMMetrics.InFlight {
		fmt.Fprintf(w, "ai_llm_in_flight 1\n")
	} else {
		fmt.Fprintf(w, "ai_llm_in_flight 0\n")
	}

	fmt.Fprintf(w, "\n# HELP ai_llm_in_flight_seconds LLM in-flight duration in seconds\n")
	fmt.Fprintf(w, "# TYPE ai_llm_in_flight_seconds gauge\n")
	fmt.Fprintf(w, "ai_llm_in_flight_seconds %.3f\n", fullMetrics.LLMMetrics.InFlightDuration.Seconds())

	fmt.Fprintf(w, "\n# HELP ai_llm_errors_total Total number of LLM errors\n")
	fmt.Fprintf(w, "# TYPE ai_llm_errors_total counter\n")
	fmt.Fprintf(w, "ai_llm_errors_total %d\n", fullMetrics.LLMMetrics.ErrorCount)

	fmt.Fprintf(w, "\n")

	// Write prompt metrics
	fmt.Fprintf(w, "# HELP ai_prompt_calls_total Total number of prompts processed\n")
	fmt.Fprintf(w, "# TYPE ai_prompt_calls_total counter\n")
	fmt.Fprintf(w, "ai_prompt_calls_total %d\n", fullMetrics.PromptMetrics.CallCount)

	fmt.Fprintf(w, "\n# HELP ai_prompt_errors_total Total number of prompt errors\n")
	fmt.Fprintf(w, "# TYPE ai_prompt_errors_total counter\n")
	fmt.Fprintf(w, "ai_prompt_errors_total %d\n", fullMetrics.PromptMetrics.ErrorCount)

	fmt.Fprintf(w, "\n# HELP ai_prompt_duration_seconds Prompt duration in seconds\n")
	fmt.Fprintf(w, "# TYPE ai_prompt_duration_seconds histogram\n")
	fmt.Fprintf(w, "ai_prompt_duration_seconds_sum %.3f\n", fullMetrics.PromptMetrics.TotalDuration.Seconds())
	fmt.Fprintf(w, "ai_prompt_duration_seconds_count %d\n", fullMetrics.PromptMetrics.CallCount)

	fmt.Fprintf(w, "\n# HELP ai_prompt_last_duration_seconds Last prompt duration in seconds\n")
	fmt.Fprintf(w, "# TYPE ai_prompt_last_duration_seconds gauge\n")
	fmt.Fprintf(w, "ai_prompt_last_duration_seconds %.3f\n", fullMetrics.PromptMetrics.LastDuration.Seconds())

	fmt.Fprintf(w, "\n")

	// Write event emission metrics
	fmt.Fprintf(w, "# HELP ai_event_emit_lag_seconds Event emit lag in seconds\n")
	fmt.Fprintf(w, "# TYPE ai_event_emit_lag_seconds histogram\n")
	fmt.Fprintf(w, "ai_event_emit_lag_seconds_sum %.3f\n", fullMetrics.EventMetrics.TotalLag.Seconds())
	fmt.Fprintf(w, "ai_event_emit_lag_seconds_count %d\n", fullMetrics.EventMetrics.Count)
	fmt.Fprintf(w, "ai_event_emit_lag_max_seconds %.3f\n", fullMetrics.EventMetrics.MaxLag.Seconds())
	fmt.Fprintf(w, "ai_event_emit_idle_seconds %.3f\n", fullMetrics.EventMetrics.IdleDuration.Seconds())

	if !fullMetrics.EventMetrics.LastEmitAt.IsZero() {
		fmt.Fprintf(w, "ai_event_last_emit_timestamp_seconds %d\n", fullMetrics.EventMetrics.LastEmitAt.Unix())
	}
	if !fullMetrics.EventMetrics.LastEventAt.IsZero() {
		fmt.Fprintf(w, "ai_event_last_event_timestamp_seconds %d\n", fullMetrics.EventMetrics.LastEventAt.Unix())
	}

	fmt.Fprintf(w, "\n")

	// Write tool metrics
	for _, tm := range fullMetrics.ToolMetrics {
		toolName := sanitizeMetricName(tm.Name)

		fmt.Fprintf(w, "# HELP ai_tool_calls_total Total number of tool calls\n")
		fmt.Fprintf(w, "# TYPE ai_tool_calls_total counter\n")
		fmt.Fprintf(w, "ai_tool_calls_total{tool=\"%s\"} %d\n", toolName, tm.CallCount)

		fmt.Fprintf(w, "\n# HELP ai_tool_success_rate Tool success rate\n")
		fmt.Fprintf(w, "# TYPE ai_tool_success_rate gauge\n")
		fmt.Fprintf(w, "ai_tool_success_rate{tool=\"%s\"} %.3f\n", toolName, tm.SuccessRate)

		fmt.Fprintf(w, "\n# HELP ai_tool_duration_seconds Tool execution duration in milliseconds\n")
		fmt.Fprintf(w, "# TYPE ai_tool_duration_seconds gauge\n")
		fmt.Fprintf(w, "ai_tool_duration_seconds{tool=\"%s\"} %.3f\n", toolName, float64(tm.AverageDurationMs)/1000.0)

		fmt.Fprintf(w, "\n# HELP ai_tool_retries_total Total number of tool retries\n")
		fmt.Fprintf(w, "# TYPE ai_tool_retries_total counter\n")
		fmt.Fprintf(w, "ai_tool_retries_total{tool=\"%s\"} %d\n", toolName, tm.RetryCount)

		fmt.Fprintf(w, "\n# HELP ai_tool_last_call_timestamp Timestamp of last tool call\n")
		fmt.Fprintf(w, "# TYPE ai_tool_last_call_timestamp gauge\n")
		if !tm.LastCall.IsZero() {
			fmt.Fprintf(w, "ai_tool_last_call_timestamp{tool=\"%s\"} %d\n", toolName, tm.LastCall.Unix())
		}
		fmt.Fprintf(w, "\n")
	}
}

// sanitizeMetricName sanitizes a tool name for Prometheus metrics.
func sanitizeMetricName(name string) string {
	// Replace invalid characters with underscores
	result := ""
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			result += string(r)
		} else {
			result += "_"
		}
	}
	// Ensure it doesn't start with a digit
	if len(result) > 0 && result[0] >= '0' && result[0] <= '9' {
		result = "_" + result
	}
	return result
}
