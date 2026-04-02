package agent

import (
	"context"
	"errors"
	"time"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// Retry constants for LLM calls.
const (
	defaultLLMMaxRetries       = 1
	defaultRateLimitMaxRetries = 8  // More retries for rate limit errors
	defaultRetryBaseDelay      = 1 * time.Second
	defaultRateLimitBaseDelay  = 3 * time.Second // Longer base delay for rate limit
)

// shouldRetryLLMError determines if an LLM error is retryable.
// Rate limit and server errors (5xx) are retryable.
// Client errors (4xx) and context length exceeded are not.
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

// jitterDelay applies +/-20% deterministic jitter to avoid retry bursts.
func jitterDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
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

// llmErrorMeta provides structured metadata about an LLM error.
type llmErrorMeta struct {
	ErrorType  string
	StatusCode int
	RetryAfter time.Duration
}

// classifyLLMError classifies an error into structured metadata.
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
