package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestEffectiveMaxRetries(t *testing.T) {
	tests := []struct {
		name        string
		maxRetries  int
		isRateLimit bool
		want        int
	}{
		{"default non-rate-limit", 0, false, 0},
		{"negative falls back to default", -1, false, defaultLLMMaxRetries},
		{"explicit non-rate-limit", 3, false, 3},
		{"rate-limit uses default cap", 0, true, defaultRateLimitMaxRetries},
		{"rate-limit config exceeds cap", 12, true, 12},
		{"rate-limit config below cap", 2, true, defaultRateLimitMaxRetries},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &LoopConfig{MaxLLMRetries: tt.maxRetries}
			got := effectiveMaxRetries(cfg, tt.isRateLimit)
			if got != tt.want {
				t.Errorf("effectiveMaxRetries(%d, %v) = %d, want %d",
					tt.maxRetries, tt.isRateLimit, got, tt.want)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	base := 1 * time.Second

	// Non-rate-limit exponential backoff: attempt 1 → base, 2 → 2x, 3 → 4x.
	for attempt, want := range map[int]time.Duration{
		1: base,
		2: 2 * base,
		3: 4 * base,
	} {
		got := retryDelay(attempt, false, nil, base)
		// jitter is ±20%; allow that band.
		lo := want - want/5
		hi := want + want/5
		if got < lo || got > hi {
			t.Errorf("retryDelay(%d, false) = %v, want within [%v, %v]", attempt, got, lo, hi)
		}
	}

	// Rate limit: floor is 2s even for small attempts.
	got := retryDelay(1, true, nil, base)
	if got < 2*time.Second {
		t.Errorf("rate-limit delay = %v, want >= 2s", got)
	}

	// Rate limit: Retry-After hint respected when larger.
	err := &llm.RateLimitError{RetryAfter: 10 * time.Second}
	got = retryDelay(1, true, err, base)
	if got < 10*time.Second-10*time.Second/5 {
		t.Errorf("rate-limit delay with retry-after = %v, want ~10s", got)
	}

	// Rate limit: capped at 30s for large attempts.
	got = retryDelay(10, true, nil, base)
	if got > 30*time.Second+30*time.Second/5 {
		t.Errorf("rate-limit delay = %v, want <= ~30s", got)
	}
}

func TestWaitForRetry(t *testing.T) {
	// Normal path: delay elapses.
	if err := waitForRetry(context.Background(), 1*time.Millisecond); err != nil {
		t.Errorf("waitForRetry normal = %v, want nil", err)
	}

	// Cancelled context returns ctx.Err.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("waitForRetry cancelled = %v, want context.Canceled", err)
	}
}

func TestClassifyLLMError(t *testing.T) {
	// nil error.
	if meta := classifyLLMError(nil); meta.ErrorType != llmErrorTypeUnknown {
		t.Errorf("nil error type = %q, want unknown", meta.ErrorType)
	}

	// Rate limit error.
	meta := classifyLLMError(&llm.RateLimitError{StatusCode: 429, RetryAfter: 5 * time.Second})
	if meta.ErrorType != llmErrorTypeRateLimit || meta.RetryAfter != 5*time.Second {
		t.Errorf("rate limit meta = %+v", meta)
	}

	// Context length error.
	meta = classifyLLMError(&llm.ContextLengthExceededError{StatusCode: 400})
	if meta.ErrorType != llmErrorTypeContextLimit {
		t.Errorf("context limit meta = %+v", meta)
	}

	// Server error.
	meta = classifyLLMError(&llm.APIError{StatusCode: 503, Message: "unavailable"})
	if meta.ErrorType != llmErrorTypeServer || meta.StatusCode != 503 {
		t.Errorf("server meta = %+v", meta)
	}

	// Client error.
	meta = classifyLLMError(&llm.APIError{StatusCode: 401, Message: "unauthorized"})
	if meta.ErrorType != llmErrorTypeClient {
		t.Errorf("client meta = %+v", meta)
	}

	// Deadline exceeded.
	meta = classifyLLMError(context.DeadlineExceeded)
	if meta.ErrorType != llmErrorTypeTimeout {
		t.Errorf("deadline meta = %+v", meta)
	}

	// Canceled.
	meta = classifyLLMError(context.Canceled)
	if meta.ErrorType != llmErrorTypeCanceled {
		t.Errorf("canceled meta = %+v", meta)
	}

	// Wrapped rate limit via message inference fallback.
	meta = classifyLLMError(errors.New("rate limit exceeded, slow down"))
	if meta.ErrorType != llmErrorTypeRateLimit {
		t.Errorf("message-inferred meta = %+v", meta)
	}

	// Unknown error message.
	meta = classifyLLMError(errors.New("something odd"))
	if meta.ErrorType != llmErrorTypeUnknown {
		t.Errorf("unknown meta = %+v", meta)
	}
}

func TestShouldRetryLLMError(t *testing.T) {
	if shouldRetryLLMError(nil) {
		t.Error("nil error should not retry")
	}
	if shouldRetryLLMError(&llm.ContextLengthExceededError{}) {
		t.Error("context length error should not retry")
	}
	if !shouldRetryLLMError(&llm.RateLimitError{}) {
		t.Error("rate limit error should retry")
	}
	if shouldRetryLLMError(&llm.APIError{StatusCode: 400}) {
		t.Error("4xx client error should not retry")
	}
	if !shouldRetryLLMError(&llm.APIError{StatusCode: 500}) {
		t.Error("5xx server error should retry")
	}
	if !shouldRetryLLMError(errors.New("some transient failure")) {
		t.Error("generic error should retry")
	}
}

func TestResolveBaseDelay(t *testing.T) {
	if got := resolveBaseDelay(&LoopConfig{}); got != defaultRetryBaseDelay {
		t.Errorf("default base delay = %v, want %v", got, defaultRetryBaseDelay)
	}
	if got := resolveBaseDelay(&LoopConfig{RetryBaseDelay: 500 * time.Millisecond}); got != defaultRetryBaseDelay {
		t.Errorf("small base delay = %v, want floor %v", got, defaultRetryBaseDelay)
	}
	custom := 5 * time.Second
	if got := resolveBaseDelay(&LoopConfig{RetryBaseDelay: custom}); got != custom {
		t.Errorf("custom base delay = %v, want %v", got, custom)
	}
}

func TestJitterDelay(t *testing.T) {
	if got := jitterDelay(0); got != 0 {
		t.Errorf("jitterDelay(0) = %v, want 0", got)
	}
	if got := jitterDelay(-time.Second); got != -time.Second {
		t.Errorf("jitterDelay(negative) = %v, want unchanged", got)
	}
	// Tiny delay where span rounds to 0 returns unchanged.
	tiny := time.Nanosecond
	if got := jitterDelay(tiny); got != tiny {
		t.Errorf("jitterDelay(tiny) = %v, want %v", got, tiny)
	}
	// Normal delay stays within ±20%.
	d := 10 * time.Second
	got := jitterDelay(d)
	if got < 8*time.Second || got > 12*time.Second {
		t.Errorf("jitterDelay(%v) = %v, want within [8s, 12s]", d, got)
	}
}

func TestLLMAttemptFromContext(t *testing.T) {
	if got := llmAttemptFromContext(nil); got != 0 {
		t.Errorf("nil ctx = %d, want 0", got)
	}
	if got := llmAttemptFromContext(context.Background()); got != 0 {
		t.Errorf("empty ctx = %d, want 0", got)
	}
	ctx := context.WithValue(context.Background(), llmAttemptKey, 3)
	if got := llmAttemptFromContext(ctx); got != 3 {
		t.Errorf("ctx with attempt = %d, want 3", got)
	}
	ctx = context.WithValue(context.Background(), llmAttemptKey, "not-int")
	if got := llmAttemptFromContext(ctx); got != 0 {
		t.Errorf("ctx with wrong type = %d, want 0", got)
	}
}
