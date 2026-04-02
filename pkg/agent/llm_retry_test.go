package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestShouldRetryLLMError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantRetry bool
	}{
		{
			name:      "nil error",
			err:       nil,
			wantRetry: false,
		},
		{
			name:      "rate limit error",
			err:       &llm.RateLimitError{StatusCode: 429, Message: "too many requests"},
			wantRetry: true,
		},
		{
			name:      "context length exceeded",
			err:       &llm.ContextLengthExceededError{StatusCode: 400, Message: "context too long"},
			wantRetry: false,
		},
		{
			name:      "500 server error",
			err:       &llm.APIError{StatusCode: 500, Message: "internal server error"},
			wantRetry: true,
		},
		{
			name:      "502 bad gateway",
			err:       &llm.APIError{StatusCode: 502, Message: "bad gateway"},
			wantRetry: true,
		},
		{
			name:      "503 service unavailable",
			err:       &llm.APIError{StatusCode: 503, Message: "service unavailable"},
			wantRetry: true,
		},
		{
			name:      "401 unauthorized - no retry",
			err:       &llm.APIError{StatusCode: 401, Message: "unauthorized"},
			wantRetry: false,
		},
		{
			name:      "403 forbidden - no retry",
			err:       &llm.APIError{StatusCode: 403, Message: "forbidden"},
			wantRetry: false,
		},
		{
			name:      "generic network error",
			err:       errors.New("connection reset by peer"),
			wantRetry: true,
		},
		{
			name:      "generic unknown error",
			err:       errors.New("something went wrong"),
			wantRetry: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRetryLLMError(tt.err)
			if got != tt.wantRetry {
				t.Errorf("shouldRetryLLMError() = %v, want %v", got, tt.wantRetry)
			}
		})
	}
}

func TestClassifyLLMError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantErrorType  string
		wantStatusCode int
		wantRetryAfter time.Duration
	}{
		{
			name:           "nil error",
			err:            nil,
			wantErrorType:  llmErrorTypeUnknown,
			wantStatusCode: 0,
		},
		{
			name:           "rate limit error",
			err:            &llm.RateLimitError{StatusCode: 429, Message: "rate limited", RetryAfter: 5 * time.Second},
			wantErrorType:  llmErrorTypeRateLimit,
			wantStatusCode: 429,
			wantRetryAfter: 5 * time.Second,
		},
		{
			name:           "context length exceeded",
			err:            &llm.ContextLengthExceededError{StatusCode: 400, Message: "too many tokens"},
			wantErrorType:  llmErrorTypeContextLimit,
			wantStatusCode: 400,
		},
		{
			name:           "500 server error",
			err:            &llm.APIError{StatusCode: 500, Message: "internal server error"},
			wantErrorType:  llmErrorTypeServer,
			wantStatusCode: 500,
		},
		{
			name:           "401 client error",
			err:            &llm.APIError{StatusCode: 401, Message: "unauthorized"},
			wantErrorType:  llmErrorTypeClient,
			wantStatusCode: 401,
		},
		{
			name:           "timeout error",
			err:            contextDeadlineExceededStub{},
			wantErrorType:  llmErrorTypeTimeout,
			wantStatusCode: 0,
		},
		{
			name:           "canceled error",
			err:            contextCanceledStub{},
			wantErrorType:  llmErrorTypeCanceled,
			wantStatusCode: 0,
		},
		{
			name:           "network error inferred from message",
			err:            errors.New("connection refused: dial tcp failed"),
			wantErrorType:  llmErrorTypeNetwork,
			wantStatusCode: 0,
		},
		{
			name:           "rate limit inferred from message",
			err:            errors.New("API error (429): rate limit exceeded"),
			wantErrorType:  llmErrorTypeRateLimit,
			wantStatusCode: 0,
		},
		{
			name:           "context length inferred from message",
			err:            errors.New("prompt is too long: maximum context length exceeded"),
			wantErrorType:  llmErrorTypeContextLimit,
			wantStatusCode: 0,
		},
		{
			name:           "server error inferred from message",
			err:            errors.New("API error (500): internal server error"),
			wantErrorType:  llmErrorTypeServer,
			wantStatusCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyLLMError(tt.err)
			if got.ErrorType != tt.wantErrorType {
				t.Errorf("ErrorType = %q, want %q", got.ErrorType, tt.wantErrorType)
			}
			if got.StatusCode != tt.wantStatusCode {
				t.Errorf("StatusCode = %d, want %d", got.StatusCode, tt.wantStatusCode)
			}
			if got.RetryAfter != tt.wantRetryAfter {
				t.Errorf("RetryAfter = %v, want %v", got.RetryAfter, tt.wantRetryAfter)
			}
		})
	}
}

// Stub types for context errors — implement Unwrap so errors.Is matches standard sentinel values.
type contextDeadlineExceededStub struct{}

func (contextDeadlineExceededStub) Error() string   { return "context deadline exceeded" }
func (contextDeadlineExceededStub) Unwrap() error    { return context.DeadlineExceeded }
func (contextDeadlineExceededStub) Timeout() bool    { return true }
func (contextDeadlineExceededStub) Deadline() (time.Time, bool) { return time.Time{}, false }

type contextCanceledStub struct{}

func (contextCanceledStub) Error() string { return "context canceled" }
func (contextCanceledStub) Unwrap() error  { return context.Canceled }

func TestJitterDelay(t *testing.T) {
	tests := []struct {
		name  string
		delay time.Duration
	}{
		{"1s", 1 * time.Second},
		{"3s", 3 * time.Second},
		{"10s", 10 * time.Second},
		{"30s", 30 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Run jitter multiple times to verify it stays within +/-20% bounds
			for i := 0; i < 100; i++ {
				result := jitterDelay(tt.delay)
				lower := tt.delay * 80 / 100 // 80%
				upper := tt.delay * 120 / 100 // 120%
				if result < lower || result > upper {
					t.Errorf("jitterDelay(%v) = %v, expected within [%v, %v]", tt.delay, result, lower, upper)
				}
			}
		})
	}

	// Edge cases
	t.Run("zero", func(t *testing.T) {
		if got := jitterDelay(0); got != 0 {
			t.Errorf("jitterDelay(0) = %v, want 0", got)
		}
	})

	t.Run("negative", func(t *testing.T) {
		if got := jitterDelay(-1 * time.Second); got != -1*time.Second {
			t.Errorf("jitterDelay(-1s) = %v, want -1s", got)
		}
	})

	t.Run("1ns", func(t *testing.T) {
		// Very small durations should return the original (span rounds to 0)
		if got := jitterDelay(1 * time.Nanosecond); got < 0 {
			t.Errorf("jitterDelay(1ns) = %v, want >= 0", got)
		}
	})
}

func TestRetryConstants(t *testing.T) {
	// Verify constants match main branch values
	if defaultLLMMaxRetries != 1 {
		t.Errorf("defaultLLMMaxRetries = %d, want 1", defaultLLMMaxRetries)
	}
	if defaultRateLimitMaxRetries != 8 {
		t.Errorf("defaultRateLimitMaxRetries = %d, want 8", defaultRateLimitMaxRetries)
	}
	if defaultRetryBaseDelay != 1*time.Second {
		t.Errorf("defaultRetryBaseDelay = %v, want 1s", defaultRetryBaseDelay)
	}
	if defaultRateLimitBaseDelay != 3*time.Second {
		t.Errorf("defaultRateLimitBaseDelay = %v, want 3s", defaultRateLimitBaseDelay)
	}
}
