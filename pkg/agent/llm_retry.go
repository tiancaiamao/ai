package agent

import (
	"context"
	"errors"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
	"log/slog"
)

const (
	defaultLLMMaxRetries       = 1
	defaultRateLimitMaxRetries = 8 // More retries for rate limit errors
	defaultRetryBaseDelay      = 1 * time.Second
	defaultRateLimitBaseDelay  = 3 * time.Second // Longer base delay for rate limit
)

type llmAttemptKeyType struct{}

var llmAttemptKey = llmAttemptKeyType{}

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
