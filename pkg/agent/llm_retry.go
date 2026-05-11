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

// effectiveMaxRetries returns the maximum number of retry attempts for the
// given configuration and error type. Rate-limit errors are allowed more
// retries than other transient errors.
func effectiveMaxRetries(config *LoopConfig, isRateLimit bool) int {
	maxRetries := config.MaxLLMRetries
	if maxRetries < 0 {
		maxRetries = defaultLLMMaxRetries
	}
	if isRateLimit {
		rlMax := defaultRateLimitMaxRetries
		if config.MaxLLMRetries > defaultRateLimitMaxRetries {
			rlMax = config.MaxLLMRetries
		}
		return rlMax
	}
	return maxRetries
}

// retryDelay computes the delay before the next retry attempt using
// exponential backoff with jitter. Rate-limit errors use longer base delays
// and respect provider Retry-After hints.
func retryDelay(attempt int, isRateLimit bool, lastErr error, baseDelay time.Duration) time.Duration {
	if isRateLimit {
		baseDelay = defaultRateLimitBaseDelay
	}

	delay := baseDelay * time.Duration(1<<(attempt-1))

	if isRateLimit {
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
		// Respect provider backoff hint when available.
		if retryAfter := llm.RetryAfter(lastErr); retryAfter > delay {
			delay = retryAfter
		}
		if delay < 2*time.Second {
			delay = 2 * time.Second
		}
	}

	return jitterDelay(delay)
}

// waitForRetry blocks for the given delay, returning an error if the context
// is cancelled first. Returns nil when the delay elapses successfully.
func waitForRetry(ctx context.Context, delay time.Duration) error {
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// resolveBaseDelay returns the effective base delay from config, applying
// the default floor when unset or too low.
func resolveBaseDelay(config *LoopConfig) time.Duration {
	if config.RetryBaseDelay >= defaultRetryBaseDelay {
		return config.RetryBaseDelay
	}
	return defaultRetryBaseDelay
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
	baseDelay := resolveBaseDelay(config)

	for attempt := 0; ; attempt++ {
		// Before each call (except the first), decide whether to retry.
		if attempt > 0 {
			meta := classifyLLMError(lastErr)
			maxRetries := effectiveMaxRetries(config, meta.ErrorType == llmErrorTypeRateLimit)

			if attempt > maxRetries {
				// All retries exhausted
				if lastErr != nil {
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

			isRateLimit := meta.ErrorType == llmErrorTypeRateLimit
			delay := retryDelay(attempt, isRateLimit, lastErr, baseDelay)
			errorTypeLabel := meta.ErrorType
			if isRateLimit {
				errorTypeLabel = "rate_limit"
			}

			// Emit retry event to frontend
			stream.Push(NewLLMRetryEvent(LLMRetryInfo{
				Attempt:    attempt,
				MaxRetries: maxRetries,
				Delay:      delay,
				ErrorType:  errorTypeLabel,
				Error:      lastErr.Error(),
			}))

			logLabel := "Retrying LLM call"
			if isRateLimit {
				logLabel = "Retrying LLM call (rate limit)"
			}
			slog.Info("[Loop] "+logLabel,
				"attempt", attempt,
				"maxRetries", maxRetries,
				"delay", delay,
				"errorType", meta.ErrorType)

			if err := waitForRetry(ctx, delay); err != nil {
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
					cause = err
				}
				return nil, WithErrorStack(cause)
			}
		}

		attemptCtx := context.WithValue(ctx, llmAttemptKey, attempt)
		msg, err := streamAssistantResponseFn(attemptCtx, agentCtx, config, stream)
		if err == nil {
			return msg, nil
		}

		if llm.IsContextLengthExceeded(err) {
			return nil, WithErrorStack(err)
		}

		lastErr = WithErrorStack(err)

		meta := classifyLLMError(lastErr)
		slog.Error("[Loop] LLM call failed",
			"attempt", attempt,
			"isRateLimit", meta.ErrorType == llmErrorTypeRateLimit,
			"error", err)

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
				traceevent.Field{Key: "attempt", Value: attempt},
				traceevent.Field{Key: "reason", Value: "context_done_after_error"},
			)
			return nil, lastErr
		}
		if !shouldRetryLLMError(lastErr) {
			traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
				traceevent.Field{Key: "attempt", Value: attempt},
				traceevent.Field{Key: "reason", Value: "non_retryable"},
				traceevent.Field{Key: "error_type", Value: meta.ErrorType},
				traceevent.Field{Key: "error_message", Value: lastErr.Error()},
			)
			return nil, lastErr
		}
	}
}
