package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultLLMMaxRetries               = 1
	defaultRateLimitMaxRetries         = 8 // More retries for rate limit errors
	defaultRetryBaseDelay              = 1 * time.Second
	defaultRateLimitBaseDelay          = 3 * time.Second // Longer base delay for rate limit
	defaultLoopMaxConsecutiveToolCalls = 6
	defaultLoopMaxToolCallsPerName     = 60
	defaultMalformedToolCallRecoveries = 2
	defaultRuntimeMetaHeartbeatTurns   = 6
	defaultLLMTotalTimeout             = 10 * time.Minute // Total timeout for LLM request
	defaultLLMFirstResponseTimeout     = 2 * time.Minute  // Timeout between streaming chunks (2min)
)

type LoopConfig struct {
	Model  llm.Model
	APIKey string
	// GetModel returns the current model. If nil, falls back to Model field.
	// This allows dynamic model switching without restarting the loop.
	GetModel func() llm.Model
	// GetAPIKey returns the current API key. If nil, falls back to APIKey field.
	// This allows dynamic API key switching without restarting the loop.
	GetAPIKey func() string
	// GetWorkingDir returns the current working directory for runtime_state telemetry.
	GetWorkingDir func() string
	// GetStartupPath returns the startup/root path for runtime_state telemetry.
	GetStartupPath           func() string
	Executor                 *ExecutorPool // agentctx.Tool executor with concurrency control
	Metrics                  *Metrics      // Metrics collector
	ToolOutput               ToolOutputLimits
	Compactor                Compactor     // Optional compactor for context-length recovery
	ToolCallCutoff           int           // Summarize oldest tool outputs when visible tool results exceed this
	ThinkingLevel            string        // off, minimal, low, medium, high, xhigh
	MaxLLMRetries            int           // Maximum number of retries for LLM calls
	RetryBaseDelay           time.Duration // Base delay for exponential backoff
	MaxConsecutiveToolCalls  int           // Loop guard: max consecutive identical tool call signature (0=default, <0=disabled)
	MaxToolCallsPerName      int           // Loop guard: max total tool calls per tool name in one run (0=default, <0=disabled)
	MaxTurns                 int           // Maximum conversation turns (0=default=unlimited)
	ContextWindow            int           // Context window for the model (0=use default 128000)
	TaskTrackingEnabled      bool          // Enable task tracking reminders (task_tracking)
	ContextManagementEnabled bool          // Enable context management reminders (context_management)
	LLMTotalTimeout          time.Duration // Total timeout for LLM request (default 10min)
	LLMFirstResponseTimeout  time.Duration // Timeout between streaming chunks (default 2min)
}

// getEffectiveModel returns the current model, using GetModel callback if available.
func getEffectiveModel(config *LoopConfig) llm.Model {
	if config.GetModel != nil {
		return config.GetModel()
	}
	return config.Model
}

// getEffectiveAPIKey returns the current API key, using GetAPIKey callback if available.
func getEffectiveAPIKey(config *LoopConfig) string {
	if config.GetAPIKey != nil {
		return config.GetAPIKey()
	}
	return config.APIKey
}

// DefaultLoopConfig returns a default LoopConfig with sensible values.
func DefaultLoopConfig() *LoopConfig {
	return &LoopConfig{
		ToolCallCutoff:           10,
		ThinkingLevel:            "high",
		MaxLLMRetries:            defaultLLMMaxRetries,
		RetryBaseDelay:           defaultRetryBaseDelay,
		TaskTrackingEnabled:      true,
		ContextManagementEnabled: true,
		Executor:                 NewExecutorPool(map[string]int{"maxConcurrentTools": 10, "queueTimeout": 60}),
		ToolOutput:               DefaultToolOutputLimits(),
		LLMTotalTimeout:          defaultLLMTotalTimeout,
		LLMFirstResponseTimeout:  defaultLLMFirstResponseTimeout,
	}
}

var streamAssistantResponseFn = streamAssistantResponse

type llmAttemptKeyType struct{}

var llmAttemptKey = llmAttemptKeyType{}

func shouldRetryLLMError(err error) bool {
	if err == nil {
		return false
	}
	if llm.IsContextLengthExceeded(err) {
		return false
	}
	if llm.IsRateLimit(err) {
		return true
	}
	var apiErr *llm.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			return false
		}
	}
	return true
}

func jitterDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	// +/-20% deterministic jitter from clock to avoid retry bursts.
	span := delay / 5
	if span <= 0 {
		return delay
	}
	offset := time.Duration(time.Now().UnixNano()%int64(2*span)) - span
	jittered := delay + offset
	if jittered <= 0 {
		return delay
	}
	return jittered
}

type llmErrorMeta struct {
	ErrorType  string
	StatusCode int
	RetryAfter time.Duration
}

func classifyLLMError(err error) llmErrorMeta {
	meta := llmErrorMeta{ErrorType: llmErrorTypeUnknown}
	if err == nil {
		return meta
	}

	var rateErr *llm.RateLimitError
	if errors.As(err, &rateErr) {
		meta.ErrorType = llmErrorTypeRateLimit
		meta.StatusCode = rateErr.StatusCode
		meta.RetryAfter = rateErr.RetryAfter
		return meta
	}

	var ctxErr *llm.ContextLengthExceededError
	if errors.As(err, &ctxErr) {
		meta.ErrorType = llmErrorTypeContextLimit
		meta.StatusCode = ctxErr.StatusCode
		return meta
	}

	var apiErr *llm.APIError
	if errors.As(err, &apiErr) {
		meta.StatusCode = apiErr.StatusCode
		switch {
		case apiErr.StatusCode >= 500:
			meta.ErrorType = llmErrorTypeServer
		case apiErr.StatusCode >= 400:
			meta.ErrorType = llmErrorTypeClient
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		meta.ErrorType = llmErrorTypeTimeout
	}
	if errors.Is(err, context.Canceled) {
		meta.ErrorType = llmErrorTypeCanceled
	}
	if llm.IsRateLimit(err) {
		meta.ErrorType = llmErrorTypeRateLimit
		if meta.RetryAfter <= 0 {
			meta.RetryAfter = llm.RetryAfter(err)
		}
	}
	if llm.IsContextLengthExceeded(err) {
		meta.ErrorType = llmErrorTypeContextLimit
	}

	if meta.ErrorType == llmErrorTypeUnknown {
		meta.ErrorType = inferLLMErrorTypeFromMessage(err.Error())
	}
	return meta
}

// RunLoop starts a new agent loop with the given prompts.
func RunLoop(
	ctx context.Context,
	prompts []agentctx.AgentMessage,
	agentCtx *agentctx.AgentContext,
	config *LoopConfig,
) *llm.EventStream[AgentEvent, []agentctx.AgentMessage] {
	stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
	)

	go func() {
		defer stream.End(nil)

		newMessages := append([]agentctx.AgentMessage{}, prompts...)
		currentCtx := &agentctx.AgentContext{
			SystemPrompt: agentCtx.SystemPrompt,
			Messages:     append(agentCtx.Messages, prompts...),
			Tools:        agentCtx.Tools,
			LLMContext:   agentCtx.LLMContext,
		}

		stream.Push(NewAgentStartEvent())
		stream.Push(NewTurnStartEvent())

		for _, msg := range prompts {
			stream.Push(NewMessageStartEvent(msg))
			stream.Push(NewMessageEndEvent(msg))
		}

		runInnerLoop(ctx, currentCtx, newMessages, config, stream)
	}()

	return stream
}

// runInnerLoop contains the core loop logic.
func runInnerLoop(
	ctx context.Context,
	agentCtx *agentctx.AgentContext,
	newMessages []agentctx.AgentMessage,
	config *LoopConfig,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
) {
	span := traceevent.StartSpan(ctx, "runInnerLoop", traceevent.CategoryEvent)
	defer span.End()

	const maxCompactionRecoveries = 1
	compactionRecoveries := 0
	loopGuard := newToolLoopGuard(config)
	malformedToolCallRecoveries := 0

	// Turn counter for MaxTurns limit
	turnCount := 0

	// Track if previous turn had tool calls for reminder timing
	previousHadToolCalls := false

	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		default:
		}

		// Set AllowReminders based on turn context:
		// - First turn (turnCount == 0): allow (user initiated)
		// - Subsequent turns: only if previous turn had tool calls
		// This prevents reminders from triggering unwanted LLM responses
		// when the assistant is about to end the conversation.
		agentCtx.AllowReminders = (turnCount == 0) || previousHadToolCalls

		// Check for max turns limit
		if config.MaxTurns > 0 && turnCount >= config.MaxTurns {
			slog.Info("[Loop] max turns limit reached",
				"turns", turnCount,
				"maxTurns", config.MaxTurns)
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}

		// Initialize ContextMgmtState if needed
		if agentCtx.ContextMgmtState == nil {
			agentCtx.ContextMgmtState = agentctx.DefaultContextMgmtState()
		}

		// Update current turn counter
		agentCtx.ContextMgmtState.SetCurrentTurn(turnCount + 1)

		turnCount++

		// Fallback auto-compact as safety net (only if LLM didn't handle it via context_management)
		// This is a last resort when context grows too large without LLM intervention.
		if config.Compactor != nil && config.Compactor.ShouldCompact(agentCtx.Messages) {
			before := len(agentCtx.Messages)
			compactionSpan := traceevent.StartSpan(ctx, "compaction", traceevent.CategoryEvent,
				traceevent.Field{Key: "source", Value: "pre_llm_threshold"},
				traceevent.Field{Key: "auto", Value: true},
				traceevent.Field{Key: "before_messages", Value: before},
				traceevent.Field{Key: "trigger", Value: "pre_llm_threshold"},
			)
			stream.Push(NewCompactionStartEvent(CompactionInfo{
				Auto:    true,
				Before:  before,
				Trigger: "pre_llm_threshold",
			}))

			compacted, compactErr := config.Compactor.Compact(agentCtx.Messages, agentCtx.LastCompactionSummary)
			if compactErr != nil {
				compactErr = WithErrorStack(compactErr)
				slog.Error("Pre-LLM compaction failed", "error", compactErr)
				compactionSpan.AddField("error", true)
				compactionSpan.AddField("error_message", compactErr.Error())
				if stack := ErrorStack(compactErr); stack != "" {
					compactionSpan.AddField("error_stack", stack)
				}
				compactionSpan.End()
				stream.Push(NewCompactionEndEvent(CompactionInfo{
					Auto:    true,
					Before:  before,
					Error:   compactErr.Error(),
					Trigger: "pre_llm_threshold",
				}))
			} else {
				if compacted != nil {
					agentCtx.Messages = compacted.Messages
					agentCtx.LastCompactionSummary = compacted.Summary
					// Persist changes to session storage
					if agentCtx.OnMessagesChanged != nil {
						if persistErr := agentCtx.OnMessagesChanged(); persistErr != nil {
							slog.Warn("[Agent] Failed to persist compacted messages", "error", persistErr)
						}
					}
				}
				// Set flag to inject overview.md for recovery on next request
				agentCtx.PostCompactRecovery = true
				after := len(agentCtx.Messages)
				compactionSpan.AddField("after_messages", after)
				compactionSpan.End()
				stream.Push(NewCompactionEndEvent(CompactionInfo{
					Auto:    true,
					Before:  before,
					After:   after,
					Trigger: "pre_llm_threshold",
				}))
			}
		}

		// Stream assistant response with retry logic
		msg, err := streamAssistantResponseWithRetry(ctx, agentCtx, config, stream)
		if err != nil {
			if llm.IsContextLengthExceeded(err) && config.Compactor != nil && compactionRecoveries < maxCompactionRecoveries {
				before := len(agentCtx.Messages)
				compactionSpan := traceevent.StartSpan(ctx, "compaction", traceevent.CategoryEvent,
					traceevent.Field{Key: "source", Value: "context_limit_recovery"},
					traceevent.Field{Key: "auto", Value: true},
					traceevent.Field{Key: "before_messages", Value: before},
					traceevent.Field{Key: "trigger", Value: "context_limit_recovery"},
				)
				stream.Push(NewCompactionStartEvent(CompactionInfo{
					Auto:    true,
					Before:  before,
					Trigger: "context_limit_recovery",
				}))
				compacted, compactErr := config.Compactor.Compact(agentCtx.Messages, agentCtx.LastCompactionSummary)
				if compactErr != nil {
					compactErr = WithErrorStack(compactErr)
					slog.Error("Compaction recovery failed", "error", compactErr)
					compactionSpan.AddField("error", true)
					compactionSpan.AddField("error_message", compactErr.Error())
					if stack := ErrorStack(compactErr); stack != "" {
						compactionSpan.AddField("error_stack", stack)
					}
					compactionSpan.End()
					stream.Push(NewCompactionEndEvent(CompactionInfo{
						Auto:    true,
						Before:  before,
						Error:   compactErr.Error(),
						Trigger: "context_limit_recovery",
					}))
				} else {
					compactionRecoveries++
					if compacted != nil {
						agentCtx.Messages = compacted.Messages
						agentCtx.LastCompactionSummary = compacted.Summary
						// Persist changes to session storage
						if agentCtx.OnMessagesChanged != nil {
							if persistErr := agentCtx.OnMessagesChanged(); persistErr != nil {
								slog.Warn("[Agent] Failed to persist compacted messages", "error", persistErr)
							}
						}
					}
					// Set flag to inject overview.md for recovery on next request
					agentCtx.PostCompactRecovery = true
					compactionSpan.AddField("after_messages", len(compacted.Messages))
					compactionSpan.End()
					stream.Push(NewCompactionEndEvent(CompactionInfo{
						Auto:    true,
						Before:  before,
						After:   len(compacted.Messages),
						Trigger: "context_limit_recovery",
					}))
					continue
				}
			}

			slog.Error("Error streaming response", "error", err)
			traceFields := []traceevent.Field{
				{Key: "error_message", Value: err.Error()},
			}
			if stack := ErrorStack(err); stack != "" {
				traceFields = append(traceFields, traceevent.Field{Key: "error_stack", Value: stack})
			}
			traceevent.Log(ctx, traceevent.CategoryEvent, "run_loop_error", traceFields...)
			stream.Push(NewErrorEvent(err))
			stream.Push(NewTurnEndEvent(msg, nil))
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}

		if msg == nil {
			// Message was nil (aborted)
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}

		agentCtx.Messages = append(agentCtx.Messages, *msg)
		newMessages = append(newMessages, *msg)

		// Update agentctx.LLMContext meta after successful LLM response
		if agentCtx.LLMContext != nil && msg.Usage != nil {
			// Use context window from config if available, otherwise use a default
			const defaultContextWindow = 200000 // matches internal/winai/interpreter.go default
			tokensMax := defaultContextWindow
			if config.ContextWindow > 0 {
				tokensMax = config.ContextWindow
			}
			agentCtx.LLMContext.SetMeta(
				msg.Usage.TotalTokens,
				tokensMax,
				len(agentCtx.Messages),
			)
			// Invalidate cache so next Load() will re-read
			agentCtx.LLMContext.InvalidateCache()
		}

		// Check for error or abort (special cases that end the loop immediately)
		if msg.StopReason == "error" || msg.StopReason == "aborted" {
			stream.Push(NewTurnEndEvent(msg, nil))
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}

		// Check for non-success stopReason and notify user
		// This handles network_error, rate_limit_error, timeout, and any other
		// error conditions that should be reported to the user instead of silent failure.
		if sanitized := sanitizeMessageForNonSuccessStopReason(msg); sanitized {
			slog.Warn("[Loop] LLM request ended with non-success stopReason", "stopReason", msg.StopReason)
			traceevent.Log(ctx, traceevent.CategoryEvent, "non_success_stop_reason_detected",
				traceevent.Field{Key: "stopReason", Value: msg.StopReason})
			// Update the message in both arrays to include the error notification
			agentCtx.Messages[len(agentCtx.Messages)-1] = *msg
			newMessages[len(newMessages)-1] = *msg
		}

		// Check for tool calls
		toolCalls := msg.ExtractToolCalls()
		hasMoreToolCalls := len(toolCalls) > 0
		if hasMoreToolCalls {
			malformedToolCallRecoveries = 0
		}
		if hasMoreToolCalls && loopGuard != nil {
			if blocked, reason := loopGuard.Observe(toolCalls); blocked {
				slog.Warn("[Loop] tool call loop guard triggered", "reason", reason)
				stream.Push(NewLoopGuardTriggeredEvent(LoopGuardInfo{Reason: reason}))
				traceevent.Log(ctx, traceevent.CategoryEvent, "tool_loop_guard_triggered",
					traceevent.Field{Key: "reason", Value: reason},
					traceevent.Field{Key: "call_count", Value: len(toolCalls)},
				)
				sanitizeMessageForToolLoopGuard(msg, reason)
				agentCtx.Messages[len(agentCtx.Messages)-1] = *msg
				newMessages[len(newMessages)-1] = *msg
				hasMoreToolCalls = false
			}
		}

		var toolResults []agentctx.AgentMessage
		if hasMoreToolCalls {
			toolResults = executeToolCalls(ctx, agentCtx, agentCtx.Tools, agentCtx.GetAllowedToolsMap(), msg, stream, config.Executor, config.Metrics, config.ToolOutput)
			for _, result := range toolResults {
				agentCtx.Messages = append(agentCtx.Messages, result)
				newMessages = append(newMessages, result)
			}
		}

		stream.Push(NewTurnEndEvent(msg, toolResults))

		// Check if LLM complied with context management protocol
		// If reminder was shown but LLM didn't call context_management, apply penalty
		if agentCtx.ContextMgmtState != nil {
			agentCtx.ContextMgmtState.CheckAndApplyCompliance()
		}
		if agentCtx.ContextMgmtState != nil {
			// Reset per-turn tracking for next turn
			agentCtx.ContextMgmtState.ResetTurnTracking()
		}

		// Update previousHadToolCalls for next iteration's reminder timing
		previousHadToolCalls = hasMoreToolCalls

		// If no more tool calls, end the conversation
		if !hasMoreToolCalls {
			if maybeRecoverMalformedToolCall(ctx, agentCtx, &newMessages, stream, msg, &malformedToolCallRecoveries) {
				// Recovery injected a user message, allow reminders for the recovery turn
				previousHadToolCalls = true
				continue
			}
			break
		}
	}

	stream.Push(NewAgentEndEvent(agentCtx.Messages))
}

// streamAssistantResponseWithRetry streams assistant's response with retry logic.
func streamAssistantResponseWithRetry(
	ctx context.Context,
	agentCtx *agentctx.AgentContext,
	config *LoopConfig,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
) (*agentctx.AgentMessage, error) {
	span := traceevent.StartSpan(ctx, "streamAssistantResponseWithRetry", traceevent.CategoryEvent)
	defer span.End()

	var lastErr error
	var isRateLimitError bool

	// Determine retry limits based on error type
	maxRetries := config.MaxLLMRetries
	if maxRetries < 0 {
		maxRetries = defaultLLMMaxRetries
	}
	baseDelay := config.RetryBaseDelay
	if baseDelay < defaultRetryBaseDelay {
		baseDelay = defaultRetryBaseDelay
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Use longer delays and more retries for rate limit errors
			if isRateLimitError {
				// Re-evaluate retry limits for rate limit
				rlMaxRetries := defaultRateLimitMaxRetries
				if config.MaxLLMRetries > defaultRateLimitMaxRetries {
					rlMaxRetries = config.MaxLLMRetries
				}

				// If we still have rate limit retries available, extend the loop
				if attempt > maxRetries && attempt <= rlMaxRetries {
					maxRetries = rlMaxRetries
				}

				baseDelay = defaultRateLimitBaseDelay
				delay := baseDelay * time.Duration(1<<(attempt-1))
				if delay > 30*time.Second {
					delay = 30 * time.Second // Cap at 30 seconds
				}

				// Respect provider backoff hint when available.
				retryAfter := llm.RetryAfter(lastErr)
				if retryAfter > delay {
					delay = retryAfter
				}
				if delay < 2*time.Second {
					delay = 2 * time.Second
				}
				delay = jitterDelay(delay)

				// Emit retry event to frontend
				stream.Push(NewLLMRetryEvent(LLMRetryInfo{
					Attempt:    attempt,
					MaxRetries: maxRetries,
					Delay:      delay,
					ErrorType:  "rate_limit",
					Error:      lastErr.Error(),
				}))

				slog.Info("[Loop] Retrying LLM call (rate limit)",
					"attempt", attempt,
					"maxRetries", maxRetries,
					"delay", delay)

				select {
				case <-time.After(delay):
					// Continue with retry
				case <-ctx.Done():
					traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
						traceevent.Field{Key: "attempt", Value: attempt},
						traceevent.Field{Key: "max_retries", Value: maxRetries},
						traceevent.Field{Key: "reason", Value: "context_done"},
					)
					if lastErr != nil {
						return nil, lastErr
					}
					cause := context.Cause(ctx)
					if cause == nil {
						cause = ctx.Err()
					}
					return nil, WithErrorStack(cause)
				}
			} else {
				// Standard retry for non-rate-limit errors
				delay := baseDelay * time.Duration(1<<(attempt-1))
				delay = jitterDelay(delay)

				slog.Info("[Loop] Retrying LLM call",
					"attempt", attempt,
					"maxRetries", maxRetries,
					"delay", delay,
					"errorType", classifyLLMError(lastErr).ErrorType)

				select {
				case <-time.After(delay):
					// Continue with retry
				case <-ctx.Done():
					traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
						traceevent.Field{Key: "attempt", Value: attempt},
						traceevent.Field{Key: "max_retries", Value: maxRetries},
						traceevent.Field{Key: "reason", Value: "context_done"},
					)
					if lastErr != nil {
						return nil, lastErr
					}
					cause := context.Cause(ctx)
					if cause == nil {
						cause = ctx.Err()
					}
					return nil, WithErrorStack(cause)
				}
			}
		}

		attemptCtx := context.WithValue(ctx, llmAttemptKey, attempt)
		msg, err := streamAssistantResponseFn(attemptCtx, agentCtx, config, stream)
		if err == nil {
			return msg, nil
		}

		// Check error type
		isRateLimitError = llm.IsRateLimit(err)

		if llm.IsContextLengthExceeded(err) {
			return nil, WithErrorStack(err)
		}

		lastErr = WithErrorStack(err)
		slog.Error("[Loop] LLM call failed",
			"attempt", attempt,
			"maxRetries", maxRetries,
			"isRateLimit", isRateLimitError,
			"error", err)

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
				traceevent.Field{Key: "attempt", Value: attempt},
				traceevent.Field{Key: "max_retries", Value: maxRetries},
				traceevent.Field{Key: "reason", Value: "context_done_after_error"},
			)
			return nil, lastErr
		}
		if !shouldRetryLLMError(lastErr) {
			meta := classifyLLMError(lastErr)
			traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
				traceevent.Field{Key: "attempt", Value: attempt},
				traceevent.Field{Key: "max_retries", Value: maxRetries},
				traceevent.Field{Key: "reason", Value: "non_retryable"},
				traceevent.Field{Key: "error_type", Value: meta.ErrorType},
				traceevent.Field{Key: "error_message", Value: lastErr.Error()},
			)
			return nil, lastErr
		}

		// For rate limit errors, allow more retries beyond initial maxRetries
		if isRateLimitError && attempt >= maxRetries {
			rlMaxRetries := defaultRateLimitMaxRetries
			if config.MaxLLMRetries > defaultRateLimitMaxRetries {
				rlMaxRetries = config.MaxLLMRetries
			}
			if attempt < rlMaxRetries {
				// Continue to retry rate limit errors
				maxRetries = rlMaxRetries
				continue
			}
		}
	}

	// All retries exhausted
	if lastErr != nil {
		meta := classifyLLMError(lastErr)
		exhaustedFields := []traceevent.Field{
			{Key: "max_retries", Value: maxRetries},
			{Key: "error_type", Value: meta.ErrorType},
			{Key: "error_message", Value: lastErr.Error()},
		}
		if meta.StatusCode > 0 {
			exhaustedFields = append(exhaustedFields, traceevent.Field{Key: "error_status_code", Value: meta.StatusCode})
		}
		traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_exhausted", exhaustedFields...)
	}
	return nil, lastErr
}

// streamAssistantResponse streams the assistant's response from the LLM.
func streamAssistantResponse(
	ctx context.Context,
	agentCtx *agentctx.AgentContext,
	config *LoopConfig,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
) (*agentctx.AgentMessage, error) {
	thinkingLevel := prompt.NormalizeThinkingLevel(config.ThinkingLevel)

	// Create timeout context for LLM calls
	llmTimeout := config.LLMTotalTimeout
	if llmTimeout == 0 {
		llmTimeout = defaultLLMTotalTimeout
	}
	llmCtx, llmCancel := context.WithTimeout(ctx, llmTimeout)
	defer llmCancel()

	var llmMessages []llm.LLMMessage

	selectedMessages, _ := selectMessagesForLLM(agentCtx)
	llmMessages = ConvertMessagesToLLM(ctx, selectedMessages)

	systemPrompt := agentCtx.SystemPrompt
	if instruction := prompt.ThinkingInstruction(thinkingLevel); instruction != "" {
		if strings.TrimSpace(systemPrompt) == "" {
			systemPrompt = instruction
		} else {
			systemPrompt = systemPrompt + "\n\n" + instruction
		}
	}

	// Build runtime appendix (llm context + context meta) as a user message
	// injected BEFORE the last user message for better LLM attention.
	// Placing runtime_state close to the decision point improves context management.
	//
	// NOTE: overview.md content is NOT injected by default. It's only injected:
	// 1. After compact (PostCompactRecovery = true) for recovery
	// 2. The LLM should use task_tracking tool to record state, which stays in tool output
	if agentCtx.LLMContext != nil {
		// Determine if we should inject overview.md content
		// Only inject after compact for recovery
		var content string
		postCompactRecovery := agentCtx.PostCompactRecovery
		if postCompactRecovery {
			// Invalidate cache to ensure we read the latest content
			agentCtx.LLMContext.InvalidateCache()
			loadedContent, err := agentCtx.LLMContext.Load()
			if err != nil {
				slog.Warn("[Loop] Failed to load llm context for recovery", "error", err)
			} else {
				content = loadedContent
			}
			// Reset the flag after injection
			agentCtx.PostCompactRecovery = false
		}

		// Refresh meta from approximate current context state.
		const defaultContextWindow = 200000 // matches internal/winai/interpreter.go default
		tokensMax := defaultContextWindow
		if config.ContextWindow > 0 {
			tokensMax = config.ContextWindow
		}
		tokensUsedApprox := EstimateConversationTokens(agentCtx.Messages)
		agentCtx.LLMContext.SetMeta(
			tokensUsedApprox,
			tokensMax,
			len(agentCtx.Messages),
		)

		meta := agentCtx.LLMContext.GetMeta()

		currentWorkdir := ""
		if config.GetWorkingDir != nil {
			currentWorkdir = config.GetWorkingDir()
		}
		startupPath := ""
		if config.GetStartupPath != nil {
			startupPath = config.GetStartupPath()
		}

		runtimeMetaSnapshot, _ := updateRuntimeMetaSnapshot(agentCtx, meta, defaultRuntimeMetaHeartbeatTurns, currentWorkdir, startupPath)
		runtimeAppendix := buildRuntimeUserAppendix(content, runtimeMetaSnapshot)

		// Always insert runtime_state when available so path telemetry is present from turn one.
		if runtimeAppendix != "" {
			runtimeMsg := llm.LLMMessage{
				Role:    "user",
				Content: runtimeAppendix,
			}
			// Insert runtime_state before the last user message for better attention
			llmMessages = insertBeforeLastUserMessage(llmMessages, runtimeMsg)
		}

	}

	staleCount := 0
	if len(agentCtx.Messages) > 0 {
		staleCount, _ = collectStaleToolOutputStats(agentCtx.Messages, recentToolResultsNoMetadata)
	}
	if agentCtx.LLMContext != nil {
		agentCtx.LLMContext.SetStaleToolCount(staleCount)
	}

	// Inject llm context reminder if LLM hasn't updated it for too many rounds
	// Only if:
	// 1. Task tracking is enabled
	// 2. AllowReminders is true (first turn or previous turn had tool calls)
	// This prevents reminders from triggering unwanted LLM responses when
	// the assistant is about to end the conversation.
	if agentCtx.TaskTrackingState != nil && agentCtx.TaskTrackingState.NeedsReminderMessage() && config.TaskTrackingEnabled && agentCtx.AllowReminders {
		reminderContent := agentCtx.TaskTrackingState.GetReminderUserMessage()
		reminderMsg := llm.LLMMessage{
			Role:    "user",
			Content: reminderContent,
		}
		llmMessages = append(llmMessages, reminderMsg)
		agentCtx.TaskTrackingState.SetWasReminded()

		// Trace event for context update reminder
		traceevent.Log(ctx, traceevent.CategoryEvent, "context_update_reminder",
			traceevent.Field{Key: "reminder_type", Value: "task_tracking"},
		)
	}

	// Inject decision reminder based on independent decision-pressure state.
	// Only if:
	// 1. Context management is enabled
	// 2. AllowReminders is true (first turn or previous turn had tool calls)
	if agentCtx.LLMContext != nil && agentCtx.ContextMgmtState != nil && config.ContextManagementEnabled && agentCtx.AllowReminders {
		meta := agentCtx.LLMContext.GetMeta()
		currentTurn := agentCtx.ContextMgmtState.GetCurrentTurn()
		showDecisionReminder, urgency := agentCtx.ContextMgmtState.ShouldShowDecisionReminder(
			currentTurn,
			meta.TokensPercent,
			staleCount,
		)
		if showDecisionReminder {
			// Minimal reminder - just nudge LLM to check runtime_state and stale outputs
			// LLM should autonomously decide what to truncate based on stale="N" attributes
			decisionReminderContent := `<agent:remind comment="system message by agent, not from real user">

💡 Context management may be needed. Check runtime_state and stale tool outputs.
</agent:remind>`
			decisionReminderMsg := llm.LLMMessage{
				Role:    "user",
				Content: decisionReminderContent,
			}
			llmMessages = append(llmMessages, decisionReminderMsg)

			// Record that a reminder was shown this turn (for proactive/reminded tracking)
			agentCtx.ContextMgmtState.RecordReminder(currentTurn, urgency)
			agentCtx.ContextMgmtState.MarkReminderShown()

			// Trace event for context decision reminder
			traceevent.Log(ctx, traceevent.CategoryEvent, "context_decision_reminder",
				traceevent.Field{Key: "reminder_type", Value: "context_management"},
				traceevent.Field{Key: "stale_tool_outputs", Value: staleCount},
				traceevent.Field{Key: "urgency", Value: urgency},
			)
		}
	}

	// Convert tools to LLM format
	llmTools := ConvertToolsToLLM(ctx, agentCtx.Tools)

	llmCtxParams := llm.LLMContext{
		SystemPrompt: systemPrompt,
		Messages:     llmMessages,
		Tools:        llmTools,
	}
	//	emitLLMRequestSnapshot(ctx, config.Model, llmCtxParams)

	// Stream LLM response
	llmStart := time.Now()
	model := getEffectiveModel(config)
	llmSpan := traceevent.StartSpan(ctx, "llm_call", traceevent.CategoryLLM,
		traceevent.Field{Key: "model", Value: model.ID},
		traceevent.Field{Key: "provider", Value: model.Provider},
		traceevent.Field{Key: "api", Value: model.API},
		traceevent.Field{Key: "attempt", Value: llmAttemptFromContext(ctx)},
		traceevent.Field{Key: "timeout_ms", Value: llmTimeout.Milliseconds()},
	)
	defer llmSpan.End()
	firstTokenRecorded := false
	firstTokenLatency := time.Duration(0)

	llmStream := llm.StreamLLM(
		llmCtx,
		model,
		llmCtxParams,
		getEffectiveAPIKey(config),
		config.LLMFirstResponseTimeout,
	)

	type toolCallState struct {
		id        string
		name      string
		callType  string
		arguments string
	}

	var partialMessage *agentctx.AgentMessage
	var textBuilder strings.Builder
	toolCalls := map[int]*toolCallState{}

	buildContent := func(text string, calls map[int]*toolCallState) []agentctx.ContentBlock {
		content := make([]agentctx.ContentBlock, 0, 1+len(calls))
		if text != "" {
			content = append(content, agentctx.TextContent{
				Type: "text",
				Text: text,
			})
		}

		if len(calls) == 0 {
			return content
		}

		indexes := make([]int, 0, len(calls))
		for idx := range calls {
			indexes = append(indexes, idx)
		}
		sort.Ints(indexes)

		for _, idx := range indexes {
			call := calls[idx]
			argsMap := make(map[string]any)
			if call.arguments != "" {
				if err := json.Unmarshal([]byte(call.arguments), &argsMap); err != nil {
					argsMap = make(map[string]any)
				}
			}

			content = append(content, agentctx.ToolCallContent{
				ID:        call.id,
				Type:      "toolCall",
				Name:      call.name,
				Arguments: argsMap,
			})
		}

		return content
	}

	for event := range llmStream.Iterator(ctx) {
		if event.Done {
			break
		}

		switch e := event.Value.(type) {
		case llm.LLMStartEvent:
			partialMessage = new(agentctx.AgentMessage)
			*partialMessage = agentctx.NewAssistantMessage()
			textBuilder.Reset()
			toolCalls = map[int]*toolCallState{}
			stream.Push(NewMessageStartEvent(*partialMessage))
			stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
				Type:         "text_start",
				ContentIndex: 0,
			}))

		case llm.LLMTextDeltaEvent:
			if partialMessage != nil {
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
				traceevent.Log(ctx, traceevent.CategoryLLM, "text_delta",
					traceevent.Field{Key: "content_index", Value: e.Index},
					traceevent.Field{Key: "delta", Value: e.Delta},
				)
				textBuilder.WriteString(e.Delta)
				partialMessage.Content = buildContent(textBuilder.String(), toolCalls)
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "text_delta",
					ContentIndex: e.Index,
					Delta:        e.Delta,
				}))
			}

		case llm.LLMThinkingDeltaEvent:
			if partialMessage != nil {
				if thinkingLevel == "off" {
					break
				}
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
				traceevent.Log(ctx, traceevent.CategoryLLM, "thinking_delta",
					traceevent.Field{Key: "content_index", Value: e.Index},
					traceevent.Field{Key: "delta", Value: e.Delta},
				)
				// Add thinking content to the message
				thinkingContent := agentctx.ThinkingContent{
					Type:     "thinking",
					Thinking: e.Delta,
				}
				partialMessage.Content = append(partialMessage.Content, thinkingContent)
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "thinking_delta",
					ContentIndex: e.Index,
					Delta:        e.Delta,
				}))
			}

		case llm.LLMToolCallDeltaEvent:
			if partialMessage != nil {
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
				traceevent.Log(ctx, traceevent.CategoryLLM, "tool_call_delta",
					traceevent.Field{Key: "content_index", Value: e.Index},
					traceevent.Field{Key: "tool_call_id", Value: e.ToolCall.ID},
					traceevent.Field{Key: "tool_name", Value: e.ToolCall.Function.Name},
				)
				call, ok := toolCalls[e.Index]
				if !ok {
					call = &toolCallState{}
					toolCalls[e.Index] = call
				}

				if e.ToolCall.ID != "" {
					call.id = e.ToolCall.ID
				}
				if e.ToolCall.Type != "" {
					call.callType = e.ToolCall.Type
				}
				if e.ToolCall.Function.Name != "" {
					call.name = e.ToolCall.Function.Name
				}
				if e.ToolCall.Function.Arguments != "" {
					// For Anthropic API, we send the complete accumulated arguments, not just the delta
					// So we should replace instead of concatenate
					call.arguments = e.ToolCall.Function.Arguments
				}

				partialMessage.Content = buildContent(textBuilder.String(), toolCalls)
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "toolcall_delta",
					ContentIndex: e.Index,
				}))
			}

		case llm.LLMDoneEvent:
			llmSpan.AddField("input_tokens", e.Usage.InputTokens)
			llmSpan.AddField("output_tokens", e.Usage.OutputTokens)
			llmSpan.AddField("total_tokens", e.Usage.TotalTokens)
			llmSpan.AddField("stop_reason", e.StopReason)
			elapsed := time.Since(llmStart)
			if elapsed > 0 {
				seconds := elapsed.Seconds()
				llmSpan.AddField("input_tokens_per_sec", float64(e.Usage.InputTokens)/seconds)
				llmSpan.AddField("output_tokens_per_sec", float64(e.Usage.OutputTokens)/seconds)
				llmSpan.AddField("total_tokens_per_sec", float64(e.Usage.TotalTokens)/seconds)
			}
			if firstTokenLatency > 0 {
				llmSpan.AddField("first_token_ms", firstTokenLatency.Milliseconds())
			}
			if partialMessage != nil {
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "text_end",
					ContentIndex: 0,
				}))
			}
			var finalMessage agentctx.AgentMessage
			model := getEffectiveModel(config)
			if partialMessage != nil {
				// Prefer the incrementally built message so thinking/tool-tag content
				// emitted via deltas is not lost when done payload omits it.
				finalMessage = *partialMessage
			} else if e.Message != nil {
				finalMessage = ConvertLLMMessageToAgent(*e.Message)
			} else {
				finalMessage = agentctx.NewAssistantMessage()
			}

			finalMessage.API = model.API
			finalMessage.Provider = model.Provider
			finalMessage.Model = model.ID
			finalMessage.Timestamp = time.Now().UnixMilli()
			finalMessage.StopReason = e.StopReason
			finalMessage.Usage = &agentctx.Usage{
				InputTokens:  e.Usage.InputTokens,
				OutputTokens: e.Usage.OutputTokens,
				TotalTokens:  e.Usage.TotalTokens,
			}

			// Try to inject tool calls from tagged text
			if updated, ok := injectToolCallsFromTaggedText(finalMessage); ok {
				finalMessage = updated
			} else if updated, ok := injectToolCallsFromThinking(finalMessage); ok {
				finalMessage = updated
			} else {
				// Check for incomplete tool calls and log for debugging
				text := finalMessage.ExtractText()
				if len(text) > 0 && strings.Contains(text, "<") {
					issues := DetectIncompleteToolCalls(text)
					traceevent.Log(ctx, traceevent.CategoryTool, "assistant_tool_tag_parse_failed",
						traceevent.Field{Key: "stop_reason", Value: e.StopReason},
						traceevent.Field{Key: "text_preview", Value: truncateLine(text, 500)},
						traceevent.Field{Key: "issues", Value: issues},
						traceevent.Field{Key: "issue_count", Value: len(issues)},
					)
				}
			}

			stream.Push(NewMessageEndEvent(finalMessage))
			return &finalMessage, nil

		case llm.LLMErrorEvent:
			errVal := e.Error
			if errVal == nil {
				errVal = errors.New("unknown llm error")
			}
			if errors.Is(errVal, context.DeadlineExceeded) {
				errVal = fmt.Errorf("llm request timeout after %s: %w", llmTimeout, errVal)
			}
			wrappedErr := WithErrorStack(errVal)
			meta := classifyLLMError(wrappedErr)
			llmSpan.AddField("error", wrappedErr.Error())
			llmSpan.AddField("error_type", meta.ErrorType)
			if meta.StatusCode > 0 {
				llmSpan.AddField("error_status_code", meta.StatusCode)
			}
			if meta.RetryAfter > 0 {
				llmSpan.AddField("retry_after_ms", meta.RetryAfter.Milliseconds())
			}
			llmSpan.AddField("retryable", shouldRetryLLMError(wrappedErr))
			if stack := ErrorStack(wrappedErr); stack != "" {
				llmSpan.AddField("error_stack", stack)
			}
			if firstTokenLatency > 0 {
				llmSpan.AddField("first_token_ms", firstTokenLatency.Milliseconds())
			}
			return nil, wrappedErr
		}
	}

	return partialMessage, nil
}

func llmAttemptFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	value := ctx.Value(llmAttemptKey)
	if attempt, ok := value.(int); ok {
		return attempt
	}
	return 0
}

func hashAny(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// executeToolCalls executes tool calls from an assistant message.
func executeToolCalls(
	ctx context.Context,
	agentCtx *agentctx.AgentContext,
	tools []agentctx.Tool,
	allowedTools map[string]bool,
	assistantMsg *agentctx.AgentMessage,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	executor *ExecutorPool,
	_ *Metrics,
	toolOutputLimits ToolOutputLimits,
) []agentctx.AgentMessage {
	toolCalls := assistantMsg.ExtractToolCalls()
	if len(toolCalls) == 0 {
		return nil
	}

	type toolExecutionPlan struct {
		index      int
		normalized agentctx.ToolCallContent
		tool       agentctx.Tool
		span       *traceevent.Span
	}
	type toolExecutionOutcome struct {
		plan     toolExecutionPlan
		content  []agentctx.ContentBlock
		err      error
		duration time.Duration
	}

	resultsByIndex := make([]*agentctx.AgentMessage, len(toolCalls))
	plans := make([]toolExecutionPlan, 0, len(toolCalls))
	toolsByName := make(map[string]agentctx.Tool, len(tools))
	for _, tool := range tools {
		toolsByName[tool.Name()] = tool
	}
	availableToolNames := make([]string, 0, len(toolsByName))
	for name := range toolsByName {
		availableToolNames = append(availableToolNames, name)
	}
	sort.Strings(availableToolNames)

	for i, tc := range toolCalls {
		rawName := strings.ToLower(strings.TrimSpace(tc.Name))
		normalized := normalizeToolCall(tc)
		toolSpan := traceevent.StartSpan(ctx, "tool_execution", traceevent.CategoryTool,
			traceevent.Field{Key: "tool", Value: normalized.Name},
			traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
			traceevent.Field{Key: "raw_name", Value: rawName},
		)
		if normalized.Name != rawName {
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_normalized",
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "normalized_name", Value: normalized.Name},
				traceevent.Field{Key: "raw_args", Value: tc.Arguments},
				traceevent.Field{Key: "normalized_args", Value: normalized.Arguments},
			)
		}
		if isGenericToolName(normalized.Name) {
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_unresolved",
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "normalized_name", Value: normalized.Name},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
				traceevent.Field{Key: "available_tools", Value: availableToolNames},
			)
			slog.Warn("[Loop] unresolved tool call name",
				"rawName", rawName,
				"normalizedName", normalized.Name,
				"availableTools", availableToolNames)
		}
		args, argErr := coerceToolArguments(normalized.Name, normalized.Arguments)
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_start",
			traceevent.Field{Key: "tool", Value: normalized.Name},
			traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
			traceevent.Field{Key: "args", Value: normalized.Arguments},
		)
		if argErr != nil {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", argErr.Error())
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_invalid_args",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "raw_args", Value: tc.Arguments},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
				traceevent.Field{Key: "error", Value: argErr.Error()},
			)
			errorMsg := buildInvalidToolArgsMessage(normalized.Name, argErr, assistantMsg.StopReason)
			result := agentctx.NewToolResultMessage(normalized.ID, normalized.Name, []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: errorMsg},
			}, true)
			stream.Push(NewToolExecutionStartEvent(normalized.ID, normalized.Name, normalized.Arguments))
			stream.Push(NewToolExecutionEndEvent(normalized.ID, normalized.Name, &result, true))
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: 0},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: argErr.Error()},
			)
			resultCopy := result
			resultsByIndex[i] = &resultCopy
			continue
		}

		normalized.Arguments = args
		stream.Push(NewToolExecutionStartEvent(normalized.ID, normalized.Name, normalized.Arguments))

		tool := toolsByName[normalized.Name]
		if tool == nil {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", "tool not found")
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_unresolved",
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "normalized_name", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
				traceevent.Field{Key: "available_tools", Value: availableToolNames},
			)
			slog.Warn("[Loop] tool not registered",
				"tool", normalized.Name,
				"rawName", rawName,
				"availableTools", availableToolNames)
			content := truncateToolContent(ctx, []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "agentctx.Tool not found"},
			}, toolOutputLimits, normalized.Name)
			result := agentctx.NewToolResultMessage(normalized.ID, normalized.Name, content, true)
			stream.Push(NewToolExecutionEndEvent(normalized.ID, normalized.Name, &result, true))
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: 0},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: "tool not found"},
			)
			resultCopy := result
			resultsByIndex[i] = &resultCopy
			continue
		}

		// Check if tool is allowed by whitelist
		if allowedTools != nil && !allowedTools[normalized.Name] {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", "tool not allowed")
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_not_allowed",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
			)
			slog.Warn("[Loop] tool not allowed by whitelist",
				"tool", normalized.Name,
				"toolCallID", normalized.ID)
			content := truncateToolContent(ctx, []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("agentctx.Tool %q is not allowed in this context", normalized.Name)},
			}, toolOutputLimits, normalized.Name)
			result := agentctx.NewToolResultMessage(normalized.ID, normalized.Name, content, true)
			stream.Push(NewToolExecutionEndEvent(normalized.ID, normalized.Name, &result, true))
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: 0},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: "tool not allowed"},
			)
			resultCopy := result
			resultsByIndex[i] = &resultCopy
			continue
		}

		plans = append(plans, toolExecutionPlan{
			index:      i,
			normalized: normalized,
			tool:       tool,
			span:       toolSpan,
		})
	}

	outcomes := make(chan toolExecutionOutcome, len(plans))
	var wg sync.WaitGroup
	toolExecCtx := agentctx.WithToolExecutionAgentContext(ctx, agentCtx)

	for _, plan := range plans {
		wg.Add(1)
		go func(plan toolExecutionPlan) {
			defer wg.Done()
			executionCtx := agentctx.WithToolExecutionCallID(toolExecCtx, plan.normalized.ID)

			start := time.Now()
			var content []agentctx.ContentBlock
			var err error
			if executor != nil {
				content, err = executor.Execute(executionCtx, plan.tool, plan.normalized.Arguments)
			} else {
				content, err = plan.tool.Execute(executionCtx, plan.normalized.Arguments)
			}

			outcomes <- toolExecutionOutcome{
				plan:     plan,
				content:  content,
				err:      err,
				duration: time.Since(start),
			}
		}(plan)
	}

	wg.Wait()
	close(outcomes)

	outcomeByIndex := make(map[int]toolExecutionOutcome, len(plans))
	for outcome := range outcomes {
		outcomeByIndex[outcome.plan.index] = outcome
	}

	for _, plan := range plans {
		outcome, ok := outcomeByIndex[plan.index]
		if !ok {
			continue
		}
		var result agentctx.AgentMessage
		if outcome.err != nil {
			content := truncateToolContent(ctx, []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: outcome.err.Error()},
			}, toolOutputLimits, plan.normalized.Name)
			result = agentctx.NewToolResultMessage(plan.normalized.ID, plan.normalized.Name, content, true)
			stream.Push(NewToolExecutionEndEvent(plan.normalized.ID, plan.normalized.Name, &result, true))
			plan.span.AddField("error", true)
			plan.span.AddField("error_message", outcome.err.Error())
			plan.span.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: plan.normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: plan.normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: outcome.duration.Milliseconds()},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: outcome.err.Error()},
			)
		} else {
			content := truncateToolContent(ctx, outcome.content, toolOutputLimits, plan.normalized.Name)
			result = agentctx.NewToolResultMessage(plan.normalized.ID, plan.normalized.Name, content, false)
			stream.Push(NewToolExecutionEndEvent(plan.normalized.ID, plan.normalized.Name, &result, false))
			plan.span.AddField("error", false)
			plan.span.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: plan.normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: plan.normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: outcome.duration.Milliseconds()},
				traceevent.Field{Key: "error", Value: false},
			)
		}

		resultCopy := result
		resultsByIndex[plan.index] = &resultCopy
	}

	results := make([]agentctx.AgentMessage, 0, len(toolCalls))
	for _, result := range resultsByIndex {
		if result != nil {
			results = append(results, *result)
		}
	}
	return results
}

func buildInvalidToolArgsMessage(toolName string, argErr error, stopReason string) string {
	if isLikelyTruncatedToolArguments(stopReason, argErr) {
		return buildTruncatedToolArgsMessage(toolName, argErr)
	}

	errorMsg := fmt.Sprintf("Invalid tool arguments for '%s': %v\n\nCorrect format:\n", toolName, argErr)
	switch toolName {
	case "read":
		errorMsg += `<read>
  <path>file.txt</path>
</read>`
	case "write":
		errorMsg += `<write>
  <path>file.txt</path>
  <content>content here</content>
</write>`
	case "edit":
		errorMsg += `<edit>
  <path>file.txt</path>
  <oldText>old text</oldText>
  <newText>new text</newText>
</edit>`
	case "bash":
		errorMsg += `<bash>
  <command>your command here</command>
</bash>

Alternatively:
<bash>command here</bash>`
	case "grep":
		errorMsg += `<grep>
  <pattern>search pattern</pattern>
  <path>optional path</path>
</grep>`
	}
	return errorMsg
}

func isLikelyTruncatedToolArguments(stopReason string, argErr error) bool {
	if argErr == nil {
		return false
	}
	if strings.TrimSpace(stopReason) != "length" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(argErr.Error())), "missing ")
}

func buildTruncatedToolArgsMessage(toolName string, argErr error) string {
	msg := fmt.Sprintf(
		"Tool call arguments for '%s' were truncated because the assistant response hit max_tokens (stopReason=length).\n\n"+
			"This is a truncation issue, not a normal schema mistake.\n"+
			"Please resend the SAME tool call with COMPLETE arguments (all required fields) in one response.\n"+
			"Validation error after truncation: %v\n\n"+
			"Expected format:\n",
		toolName,
		argErr,
	)

	switch toolName {
	case "read":
		msg += `<read>
  <path>file.txt</path>
</read>`
	case "write":
		msg += `<write>
  <path>file.txt</path>
  <content>content here</content>
</write>`
	case "edit":
		msg += `<edit>
  <path>file.txt</path>
  <oldText>old text</oldText>
  <newText>new text</newText>
</edit>`
	case "bash":
		msg += `<bash>
  <command>your command here</command>
</bash>

Alternatively:
<bash>command here</bash>`
	case "grep":
		msg += `<grep>
  <pattern>search pattern</pattern>
  <path>optional path</path>
</grep>`
	}

	return msg
}

// extractRecentMessages extracts recent messages from the message list.
// It keeps messages within the token budget, starting from the most recent.
func extractRecentMessages(messages []agentctx.AgentMessage, tokenBudget int) []agentctx.AgentMessage {
	if len(messages) == 0 {
		return messages
	}

	// First, filter to only agent-visible messages
	visible := make([]agentctx.AgentMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.IsAgentVisible() {
			visible = append(visible, msg)
		}
	}

	if len(visible) == 0 {
		return nil
	}

	// If budget is 0 or negative, return last message only
	if tokenBudget <= 0 {
		return visible[len(visible)-1:]
	}

	// Count tokens from the end, keeping messages within budget
	used := 0
	start := len(visible)

	for i := len(visible) - 1; i >= 0; i-- {
		msgTokens := EstimateMessageTokens(visible[i])
		if used+msgTokens > tokenBudget && start != len(visible) {
			break
		}
		used += msgTokens
		start = i
	}

	if start >= len(visible) {
		return visible
	}

	result := visible[start:]

	// Skip leading tool/toolResult messages to ensure valid message sequence.
	// agentctx.Tool messages must follow an assistant message with tool_calls.
	// If we truncated in the middle of a tool call sequence, drop the orphaned tool results.
	for len(result) > 0 && (result[0].Role == "tool" || result[0].Role == "toolResult") {
		result = result[1:]
	}

	return result
}

// EstimateConversationTokens estimates token count for messages.
func EstimateConversationTokens(messages []agentctx.AgentMessage) int {
	total := 0
	for _, msg := range messages {
		total += EstimateMessageTokens(msg)
	}
	return total
}

// extractActiveTurnMessages returns only messages in the active turn window.
// The window starts from the most recent agent-visible user message so prior
// history is excluded while current tool-call protocol context is preserved.
func extractActiveTurnMessages(messages []agentctx.AgentMessage, tokenBudget int) []agentctx.AgentMessage {
	if len(messages) == 0 {
		return nil
	}

	visible := make([]agentctx.AgentMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.IsAgentVisible() {
			visible = append(visible, msg)
		}
	}
	if len(visible) == 0 {
		return nil
	}

	start := len(visible) - 1
	for i := len(visible) - 1; i >= 0; i-- {
		if strings.EqualFold(visible[i].Role, "user") {
			start = i
			break
		}
	}

	active := visible[start:]
	if tokenBudget <= 0 {
		return active
	}
	return extractRecentMessages(active, tokenBudget)
}

func selectMessagesForLLM(agentCtx *agentctx.AgentContext) ([]agentctx.AgentMessage, string) {
	if agentCtx == nil {
		return nil, "empty_context"
	}
	if len(agentCtx.Messages) == 0 {
		return nil, "no_messages"
	}
	return agentCtx.Messages, "all_available_messages_no_runtime_clip"
}

func hasSuccessfulLLMContextWrite(messages []agentctx.AgentMessage, overviewPath string) bool {
	targetAbs := normalizePathForContains(overviewPath)
	targetRel := normalizePathForContains(filepath.ToSlash(filepath.Join(agentctx.LLMContextDir, agentctx.OverviewFile)))
	allowRelativeFallback := targetAbs == "" && targetRel != ""
	if targetAbs == "" && !allowRelativeFallback {
		return false
	}

	for _, msg := range messages {
		if msg.Role != "toolResult" || msg.IsError {
			continue
		}
		tool := strings.ToLower(strings.TrimSpace(msg.ToolName))
		if tool != "write" && tool != "edit" {
			continue
		}
		body := normalizePathForContains(msg.ExtractText())
		if body == "" {
			continue
		}
		if targetAbs != "" && strings.Contains(body, targetAbs) {
			return true
		}
		if allowRelativeFallback && strings.Contains(body, targetRel) {
			return true
		}
	}
	return false
}

func normalizeLLMContextContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.TrimSpace(content)
	lines := strings.Split(content, "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		normalized = append(normalized, strings.TrimRight(line, " \t"))
	}
	return strings.Join(normalized, "\n")
}

func normalizePathForContains(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = filepath.Clean(value)
	value = strings.ReplaceAll(value, "\\", "/")
	return strings.ToLower(value)
}

// insertBeforeLastUserMessage inserts a message before the last user message in the slice.
// If there are no user messages, it appends to the end.
// This is used to place runtime_state close to the decision point for better LLM attention.
func insertBeforeLastUserMessage(messages []llm.LLMMessage, msg llm.LLMMessage) []llm.LLMMessage {
	if len(messages) == 0 {
		return []llm.LLMMessage{msg}
	}

	// Find the last user message index
	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	// If no user message found, append to end
	if lastUserIdx == -1 {
		return append(messages, msg)
	}

	// Insert before the last user message
	result := make([]llm.LLMMessage, 0, len(messages)+1)
	result = append(result, messages[:lastUserIdx]...)
	result = append(result, msg)
	result = append(result, messages[lastUserIdx:]...)
	return result
}

func buildRuntimeUserAppendix(llmContextContent, runtimeMetaSnapshot string) string {
	sections := make([]string, 0, 3)
	if strings.TrimSpace(llmContextContent) != "" {
		sections = append(sections, fmt.Sprintf("<llm_context>\n%s\n</llm_context>", llmContextContent))
	}
	if strings.TrimSpace(runtimeMetaSnapshot) != "" {
		sections = append(sections, runtimeMetaSnapshot)
	}
	if len(sections) == 0 {
		return ""
	}
	sections = append(sections, `Remember: runtime_state is telemetry, not user intent.
Follow the Turn Protocol defined in the system prompt.
Path authority: use LLM Context Path/Detail dir from system prompt; ignore cwd-relative examples.`)
	return strings.Join(sections, "\n\n")
}

// buildRuntimeSystemAppendix is kept for backward-compatible tests/helpers.
// Runtime state is now injected as a user message, not appended to system prompt.
func buildRuntimeSystemAppendix(llmContextContent, runtimeMetaSnapshot string) string {
	return buildRuntimeUserAppendix(llmContextContent, runtimeMetaSnapshot)
}

func updateRuntimeMetaSnapshot(
	agentCtx *agentctx.AgentContext,
	meta agentctx.ContextMeta,
	heartbeatTurns int,
	currentWorkdir string,
	startupPath string,
) (string, bool) {
	if agentCtx == nil {
		return "", false
	}
	if heartbeatTurns <= 0 {
		heartbeatTurns = defaultRuntimeMetaHeartbeatTurns
	}

	agentCtx.RuntimeMetaTurns++
	band := runtimeTokenBand(meta.TokensPercent)

	shouldRefresh := strings.TrimSpace(agentCtx.RuntimeMetaSnapshot) == "" ||
		agentCtx.RuntimeMetaBand != band ||
		agentCtx.RuntimeMetaTurns >= heartbeatTurns

	if !shouldRefresh {
		return agentCtx.RuntimeMetaSnapshot, false
	}

	tokensUsedApprox := normalizeApprox(meta.TokensUsed)
	toolPressure := collectRuntimeToolPressure(agentCtx.Messages)
	toolOutputsSummary := buildToolOutputsSummary(agentCtx.Messages)
	actionHint := runtimeActionHint(band)
	fastPathAllowed := actionHint == "normal" && toolPressure.StaleCount == 0 && toolPressure.LargeCount == 0

	// Get or initialize ContextMgmtState
	if agentCtx.ContextMgmtState == nil {
		agentCtx.ContextMgmtState = agentctx.DefaultContextMgmtState()
	}
	state := agentCtx.ContextMgmtState
	stateSnapshot := state.Snapshot()

	// Calculate reminders_remaining (turns until next reminder)
	remindersRemaining := 0
	currentTurn := stateSnapshot.CurrentTurn

	// Account for skip period: if we're in skip, that's when the next reminder is due
	if state.SkipUntilTurn > 0 && currentTurn < state.SkipUntilTurn {
		// We're in a skip period - next reminder is at SkipUntilTurn
		remindersRemaining = state.SkipUntilTurn - currentTurn
	} else if stateSnapshot.ReminderFrequency > 0 {
		// Normal period - calculate based on reminder frequency
		remindersRemaining = stateSnapshot.LastReminderTurn + stateSnapshot.ReminderFrequency - currentTurn
		if remindersRemaining < 0 {
			remindersRemaining = 0
		}
	}

	// Build update metrics section
	var updateMetrics string
	if agentCtx.TaskTrackingState != nil {
		updateStats := agentCtx.TaskTrackingState.GetUpdateStats()
		if updateStats.Total > 0 {
			updateMetrics = fmt.Sprintf(`
context_metrics:
  update:
    total: %d
    autonomous: %d
    prompted: %d
    consciousness: %d%%
    score: %s
  decision:
    proactive: %d
    reminded: %d
    reminders_remaining: %d
    score: %s`,
				updateStats.Total,
				updateStats.Autonomous,
				updateStats.Prompted,
				updateStats.ConsciousPct,
				updateStats.Score,
				stateSnapshot.ProactiveDecisions,
				stateSnapshot.ReminderNeeded,
				remindersRemaining,
				stateSnapshot.Score)
		} else {
			updateMetrics = fmt.Sprintf(`
context_metrics:
  update:
    total: 0
    score: no_data
  decision:
    proactive: 0
    reminded: 0
    reminders_remaining: %d
    score: no_data_yet`, remindersRemaining)
		}
	}

	// runtime_state is purely informational - no directives or commands
	// Reminders are handled separately via NeedsReminderMessage/NeedsDecisionReminder
	snapshot := fmt.Sprintf(`<agent:runtime_state comment="telemetry snapshot, updated periodically"/>
context_meta:
  tokens_band: %s
  action_hint: %s
  tokens_used_approx: %d
  tokens_max: %d
  messages_in_history_bucket: %s
  llm_context_size_bucket: %s
workspace:
  current_workdir: %s
  startup_path: %s
tool_output_pressure:
  stale_tool_outputs_bucket: %s
  tool_outputs_summary: %s
  large_tool_outputs_bucket: %s
  largest_tool_output_bucket: %s%s
compact_decision_signals:
  tokens_percent: %.1f
  context_usage_percent: %.1f
  topic_shift_since_last_user: llm_judge
  phase_completed_recently: llm_judge
  llm_judge_hint: Compare the latest user intent with recent task thread and milestone status, then set COMPACT confidence accordingly.
decision:
  fast_path_allowed: %s`,
		band,
		actionHint,
		tokensUsedApprox,
		meta.TokensMax,
		runtimeMessageBucket(meta.MessagesInHistory),
		runtimeSizeBucket(meta.LLMContextSize),
		runtimeYAMLString(currentWorkdir),
		runtimeYAMLString(startupPath),
		runtimeCountBucket(toolPressure.StaleCount),
		toolOutputsSummary,
		runtimeCountBucket(toolPressure.LargeCount),
		runtimeToolOutputSizeBucket(toolPressure.LargestChars),
		updateMetrics,
		meta.TokensPercent,
		meta.TokensPercent,
		yesNo(fastPathAllowed),
	)

	agentCtx.RuntimeMetaSnapshot = snapshot
	agentCtx.RuntimeMetaBand = band
	agentCtx.RuntimeMetaTurns = 0

	return snapshot, true
}

func runtimeYAMLString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = "unknown"
	}
	return strconv.Quote(trimmed)
}

type runtimeToolPressure struct {
	StaleCount   int
	LargeCount   int
	LargestChars int
}

func collectRuntimeToolPressure(messages []agentctx.AgentMessage) runtimeToolPressure {
	pressure := runtimeToolPressure{}
	if len(messages) == 0 {
		return pressure
	}

	staleCount, _ := collectStaleToolOutputStats(messages, recentToolResultsNoMetadata)
	pressure.StaleCount = staleCount

	const largeOutputThresholdChars = 2000
	for _, msg := range messages {
		if !msg.IsAgentVisible() || msg.Role != "toolResult" {
			continue
		}

		size := len(msg.ExtractText())
		if size > pressure.LargestChars {
			pressure.LargestChars = size
		}
		if size >= largeOutputThresholdChars {
			pressure.LargeCount++
		}
	}

	return pressure
}

func runtimeTokenBand(percent float64) string {
	switch {
	case percent < 20:
		return "0-20"
	case percent < 40:
		return "20-40"
	case percent < 60:
		return "40-60"
	case percent < 75:
		return "60-75"
	default:
		return "75+"
	}
}

func runtimeActionHint(band string) string {
	switch band {
	case "0-20":
		return "normal"
	case "20-40":
		return "light_compression"
	case "40-60":
		return "medium_compression"
	case "60-75":
		return "heavy_compression"
	default:
		return "emergency_compression"
	}
}

func runtimeContextManagementHint(percent float64) string {
	// COMPACT is expensive (requires LLM call), only recommend when truly needed
	// ShouldCompact rejects when token usage < 75% of available threshold
	switch {
	case percent < 20:
		return "Low usage (10-20%% tone): stay on task, only TRUNCATE obviously stale/large tool outputs. COMPACT is NOT recommended at this level."
	case percent < 30:
		return "Mild pressure (20-30%% tone): proactively TRUNCATE stale outputs in batches (50-100 at once). COMPACT is optional and may be rejected."
	case percent < 50:
		return "Moderate pressure (30-50%% tone): TRUNCATE stale outputs, consider COMPACT only after completing current task phase."
	case percent < 65:
		return "High pressure (50-65%% tone): prepare for COMPACT, keep only active context and key decisions."
	case percent < 75:
		return "Critical pressure (65-75%% tone): COMPACT now, fallback auto-compaction is getting close."
	default:
		return "Emergency pressure (75%%+ tone): COMPACT immediately, forced fallback compaction may trigger next."
	}
}

func runtimeMessageBucket(count int) string {
	switch {
	case count <= 0:
		return "0"
	case count <= 10:
		return "1-10"
	case count <= 25:
		return "11-25"
	case count <= 50:
		return "26-50"
	case count <= 100:
		return "51-100"
	default:
		return "100+"
	}
}

func runtimeSizeBucket(size int) string {
	switch {
	case size <= 0:
		return "0"
	case size <= 1024:
		return "0-1KB"
	case size <= 4*1024:
		return "1-4KB"
	case size <= 16*1024:
		return "4-16KB"
	case size <= 64*1024:
		return "16-64KB"
	default:
		return "64KB+"
	}
}

func runtimeCountBucket(count int) string {
	switch {
	case count <= 0:
		return "0"
	case count <= 2:
		return "1-2"
	case count <= 5:
		return "3-5"
	case count <= 10:
		return "6-10"
	default:
		return "10+"
	}
}

func runtimeToolOutputSizeBucket(chars int) string {
	switch {
	case chars <= 0:
		return "0"
	case chars <= 512:
		return "1-512c"
	case chars <= 2048:
		return "513-2Kc"
	case chars <= 8192:
		return "2K-8Kc"
	default:
		return "8Kc+"
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func normalizeApprox(value int) int {
	if value <= 0 {
		return 0
	}
	if value < 1000 {
		return value
	}
	return (value / 1000) * 1000
}

// EstimateMessageTokens estimates token count for a message.
func EstimateMessageTokens(msg agentctx.AgentMessage) int {
	if !msg.IsAgentVisible() {
		return 0
	}

	charCount := 0
	for _, block := range msg.Content {
		switch b := block.(type) {
		case agentctx.TextContent:
			charCount += len(b.Text)
		case agentctx.ThinkingContent:
			charCount += len(b.Thinking)
		case agentctx.ToolCallContent:
			charCount += len(b.Name)
			if b.Arguments != nil {
				if argBytes, err := json.Marshal(b.Arguments); err == nil {
					charCount += len(argBytes)
				}
			}
		case agentctx.ImageContent:
			// Roughly estimate images as 1200 tokens (4800 chars)
			charCount += 4800
		}
	}

	if charCount == 0 {
		charCount = len(msg.ExtractText())
	}
	if charCount == 0 {
		return 0
	}

	// Rough approximation: 1 token per 4 characters
	return (charCount + 3) / 4
}

// randFloat64 returns a random float64 in [0, 1)
func randFloat64() float64 {
	return rand.Float64()
}
