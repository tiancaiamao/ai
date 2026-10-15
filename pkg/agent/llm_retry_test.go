package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// retryTestToken marks the context of a retry test so its stub only reacts to
// its own invocation. Other tests in the package can leave agent loops running
// that share the package-global streamAssistantResponseFn; without this guard
// they would inflate the attempt counter.
type retryTestToken struct{}

// stubStream returns an EventStream suitable for driving
// streamAssistantResponseWithRetry in tests.
func stubStream() *llm.EventStream[AgentEvent, []agentctx.AgentMessage] {
	return llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(AgentEvent) bool { return false },
		func(AgentEvent) []agentctx.AgentMessage { return nil },
	)
}

// benignAssistantMsg is returned to unrelated callers so a leftover loop is
// not disturbed by the test stub.
func benignAssistantMsg() *agentctx.AgentMessage {
	msg := agentctx.NewAssistantMessage()
	msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "ok"}}
	msg.StopReason = "stop"
	return &msg
}

// TestRetryOnReasoningContentRequired verifies a transient reasoning_content
// 400 (a 4xx that would normally be fatal) is retried and recovers.
func TestRetryOnReasoningContentRequired(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	token := retryTestToken{}
	var mu sync.Mutex
	attempts := 0
	streamAssistantResponseFn = func(
		ctx context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		if ctx.Value(token) == nil {
			return benignAssistantMsg(), nil
		}
		mu.Lock()
		attempts++
		attempt := attempts
		mu.Unlock()
		if attempt == 1 {
			return nil, &llm.ReasoningContentRequiredError{
				StatusCode: 400,
				Message:    "The reasoning_content in the thinking mode must be passed back to the API.",
			}
		}
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "recovered"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	config := &LoopConfig{MaxLLMRetries: defaultLLMMaxRetries, RetryBaseDelay: defaultRetryBaseDelay}
	ctx := context.WithValue(context.Background(), token, true)
	msg, err := streamAssistantResponseWithRetry(ctx, &agentctx.AgentContext{}, config, stubStream())
	if err != nil {
		t.Fatalf("expected recovery after retry, got error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts (1 retry), got %d", attempts)
	}
	if msg == nil || msg.ExtractText() != "recovered" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

// TestRetryOnReasoningContentRequiredExhausted verifies the retry budget is
// bounded (does not loop forever) when the gateway keeps rejecting.
func TestRetryOnReasoningContentRequiredExhausted(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	token := retryTestToken{}
	var mu sync.Mutex
	attempts := 0
	streamAssistantResponseFn = func(
		ctx context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		if ctx.Value(token) == nil {
			return benignAssistantMsg(), nil
		}
		mu.Lock()
		attempts++
		mu.Unlock()
		return nil, &llm.ReasoningContentRequiredError{StatusCode: 400, Message: "still missing reasoning_content"}
	}

	config := &LoopConfig{MaxLLMRetries: 0, RetryBaseDelay: defaultRetryBaseDelay}
	ctx := context.WithValue(context.Background(), token, true)
	_, err := streamAssistantResponseWithRetry(ctx, &agentctx.AgentContext{}, config, stubStream())
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	want := defaultReasoningContentMaxRetries + 1
	if attempts != want {
		t.Fatalf("expected %d attempts, got %d", want, attempts)
	}
}

func TestEffectiveMaxRetries(t *testing.T) {
	tests := []struct {
		name       string
		maxRetries int
		errorType  string
		want       int
	}{
		{"default non-rate-limit", 0, llmErrorTypeClient, 0},
		{"negative falls back to default", -1, llmErrorTypeClient, defaultLLMMaxRetries},
		{"explicit non-rate-limit", 3, llmErrorTypeClient, 3},
		{"rate-limit uses default cap", 0, llmErrorTypeRateLimit, defaultRateLimitMaxRetries},
		{"rate-limit config exceeds cap", 12, llmErrorTypeRateLimit, 12},
		{"rate-limit config below cap", 2, llmErrorTypeRateLimit, defaultRateLimitMaxRetries},
		{"reasoning-content uses default cap", 0, llmErrorTypeReasoningContent, defaultReasoningContentMaxRetries},
		{"reasoning-content config below cap", 1, llmErrorTypeReasoningContent, defaultReasoningContentMaxRetries},
		{"reasoning-content config exceeds cap", 5, llmErrorTypeReasoningContent, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &LoopConfig{MaxLLMRetries: tt.maxRetries}
			got := effectiveMaxRetries(cfg, tt.errorType)
			if got != tt.want {
				t.Errorf("effectiveMaxRetries(%d, %q) = %d, want %d",
					tt.maxRetries, tt.errorType, got, tt.want)
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

	// Reasoning-content-required gateway rejection (a 400 that is retried).
	meta = classifyLLMError(&llm.ReasoningContentRequiredError{StatusCode: 400, Message: "The reasoning_content in the thinking mode must be passed back to the API."})
	if meta.ErrorType != llmErrorTypeReasoningContent || meta.StatusCode != 400 {
		t.Errorf("reasoning-content meta = %+v", meta)
	}

	// A wrapped/plain-string reasoning_content error (no typed wrapper) must
	// still classify as the dedicated type via the message fallback.
	meta = classifyLLMError(errors.New("API error (400): The reasoning_content in the thinking mode must be passed back to the API."))
	if meta.ErrorType != llmErrorTypeReasoningContent {
		t.Errorf("wrapped reasoning-content meta = %+v", meta)
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
	// The reasoning_content 400 is a transient gateway condition and must retry
	// even though it is a 4xx.
	if !shouldRetryLLMError(&llm.ReasoningContentRequiredError{StatusCode: 400}) {
		t.Error("reasoning_content-required 400 should retry")
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
