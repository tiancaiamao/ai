package llm

import (
	"errors"
	"testing"
	"time"
)

func TestClassifyAPIErrorContextLength(t *testing.T) {
	payload := `{"error":{"message":"This model's maximum context length is 128000 tokens. However, your messages resulted in 130001 tokens."}}`
	err := ClassifyAPIError(400, payload)

	var ctxErr *ContextLengthExceededError
	if !errors.As(err, &ctxErr) {
		t.Fatalf("expected ContextLengthExceededError, got %T (%v)", err, err)
	}
	if ctxErr.StatusCode != 400 {
		t.Fatalf("expected status 400, got %d", ctxErr.StatusCode)
	}
}

func TestClassifyAPIErrorGeneric(t *testing.T) {
	payload := `{"error":{"message":"invalid auth token"}}`
	err := ClassifyAPIError(401, payload)

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != 401 {
		t.Fatalf("expected status 401, got %d", apiErr.StatusCode)
	}
}

func TestIsContextLengthStopReason(t *testing.T) {
	tests := []struct {
		name       string
		stopReason string
		want       bool
	}{
		{"model_context_window_exceeded", "model_context_window_exceeded", true},
		{"context_window_exceeded", "context_window_exceeded", true},
		{"context_length_exceeded", "context_length_exceeded", true},
		{"stop", "stop", false},
		{"tool_calls", "tool_calls", false},
		{"length", "length", false},
		{"empty", "", false},
		{"end_turn", "end_turn", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsContextLengthStopReason(tt.stopReason)
			if got != tt.want {
				t.Errorf("IsContextLengthStopReason(%q) = %v, want %v", tt.stopReason, got, tt.want)
			}
		})
	}
}

func TestIsContextLengthExceeded(t *testing.T) {
	if !IsContextLengthExceeded(&ContextLengthExceededError{Message: "context window exceeded"}) {
		t.Fatal("expected typed context length error to match")
	}
	if !IsContextLengthExceeded(errors.New("context window exceeded")) {
		t.Fatal("expected string context length error to match")
	}
	if IsContextLengthExceeded(errors.New("permission denied")) {
		t.Fatal("expected non-context error to not match")
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"nil error", nil, false},
		{"500 server error", &APIError{StatusCode: 500, Message: "internal"}, true},
		{"502 bad gateway", &APIError{StatusCode: 502, Message: "bad gateway"}, true},
		{"503 service unavailable", &APIError{StatusCode: 503, Message: "unavailable"}, true},
		{"504 gateway timeout", &APIError{StatusCode: 504, Message: "timeout"}, true},
		{"429 rate limit via APIError", &APIError{StatusCode: 429, Message: "rate limited"}, true},
		{"429 via RateLimitError", &RateLimitError{StatusCode: 429, Message: "rate limited"}, true},
		{"400 bad request", &APIError{StatusCode: 400, Message: "bad request"}, false},
		{"401 unauthorized", &APIError{StatusCode: 401, Message: "unauthorized"}, false},
		{"403 forbidden", &APIError{StatusCode: 403, Message: "forbidden"}, false},
		{"404 not found", &APIError{StatusCode: 404, Message: "not found"}, false},
		{"generic error", errors.New("something else"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRetryableError(tt.err)
			if got != tt.retryable {
				t.Errorf("IsRetryableError(%v) = %v, want %v", tt.err, got, tt.retryable)
			}
		})
	}
}

func TestClassifyAPIErrorRateLimit(t *testing.T) {
	err := ClassifyAPIErrorWithRetryAfter(429, `{"error":{"message":"Rate limit reached"}}`, 3*time.Second)

	var rlErr *RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected RateLimitError, got %T (%v)", err, err)
	}
	if rlErr.RetryAfter != 3*time.Second {
		t.Fatalf("expected retry-after 3s, got %v", rlErr.RetryAfter)
	}
	if !IsRateLimit(err) {
		t.Fatalf("expected IsRateLimit=true for %v", err)
	}
	if RetryAfter(err) != 3*time.Second {
		t.Fatalf("expected RetryAfter=3s, got %v", RetryAfter(err))
	}
}

// Cover the Error() formatters on all three error types so the
// status-code/empty-message branches get exercised.

func TestAPIErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  *APIError
		want string
	}{
		{"with status", &APIError{StatusCode: 500, Message: "boom"}, "API error (500): boom"},
		{"no status", &APIError{Message: "oops"}, "API error: oops"},
		{"empty message", &APIError{StatusCode: 503}, "API error (503): unknown API error"},
		{"only whitespace message", &APIError{StatusCode: 400, Message: "   "}, "API error (400): unknown API error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContextLengthExceededErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  *ContextLengthExceededError
		want string
	}{
		{"with status", &ContextLengthExceededError{StatusCode: 400, Message: "too long"}, "context length exceeded (400): too long"},
		{"no status", &ContextLengthExceededError{Message: "too long"}, "context length exceeded: too long"},
		{"empty message", &ContextLengthExceededError{StatusCode: 400}, "context length exceeded (400): context length exceeded"},
		{"only whitespace message", &ContextLengthExceededError{Message: "   "}, "context length exceeded: context length exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRateLimitErrorError(t *testing.T) {
	tests := []struct {
		name string
		err  *RateLimitError
		want string
	}{
		{
			"with status and retry-after",
			&RateLimitError{StatusCode: 429, Message: "slow down", RetryAfter: 5 * time.Second},
			"API error (429): slow down (retry after 5s)",
		},
		{
			"no status no retry-after",
			&RateLimitError{Message: "slow down"},
			"API error: slow down",
		},
		{
			"empty message with retry-after",
			&RateLimitError{StatusCode: 429, RetryAfter: 2 * time.Second},
			"API error (429): rate limit exceeded (retry after 2s)",
		},
		{
			"empty message no status",
			&RateLimitError{},
			"API error: rate limit exceeded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Cover the remaining branches of extractAPIErrorMessage: string error,
// detail fallback, type fallback, top-level message/detail, and malformed JSON.

func TestExtractAPIErrorMessage(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{"empty", "", ""},
		{"invalid JSON", "not json", ""},
		{"error.message", `{"error":{"message":"hello"}}`, "hello"},
		{"error.detail", `{"error":{"detail":"det"}}`, "det"},
		{"error.type", `{"error":{"type":"rate_limit"}}`, "rate_limit"},
		{"error as string", `{"error":"boom"}`, "boom"},
		{"error object without known fields", `{"error":{"foo":"bar"}}`, ""},
		{"top-level message", `{"message":"hi"}`, "hi"},
		{"top-level detail", `{"detail":"det"}`, "det"},
		{"message trims whitespace", `{"message":"  spaced  "}`, "spaced"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractAPIErrorMessage(tt.payload); got != tt.want {
				t.Errorf("extractAPIErrorMessage(%q) = %q, want %q", tt.payload, got, tt.want)
			}
		})
	}
}

// Cover the looksLikeRateLimit / looksLikeContextLengthExceeded heuristics
// via their public entry points, including the nil branch of IsRateLimit and
// the fall-through branch in ClassifyAPIError for an unrecognized payload.

func TestIsRateLimitHeuristics(t *testing.T) {
	if IsRateLimit(nil) {
		t.Fatal("IsRateLimit(nil) should be false")
	}
	if !IsRateLimit(errors.New("status code: 429 too many requests")) {
		t.Fatal("expected IsRateLimit=true for 429 string")
	}
	if !IsRateLimit(errors.New("throttle exceeded")) {
		t.Fatal("expected IsRateLimit=true for throttle string")
	}
	if IsRateLimit(errors.New("totally fine")) {
		t.Fatal("expected IsRateLimit=false for unrelated string")
	}
}

func TestRetryAfterOnNonRateLimit(t *testing.T) {
	if got := RetryAfter(nil); got != 0 {
		t.Fatalf("RetryAfter(nil) = %v, want 0", got)
	}
	if got := RetryAfter(errors.New("not a rate limit")); got != 0 {
		t.Fatalf("RetryAfter(non-RL) = %v, want 0", got)
	}
}

func TestIsContextLengthExceededNil(t *testing.T) {
	if IsContextLengthExceeded(nil) {
		t.Fatal("IsContextLengthExceeded(nil) should be false")
	}
}

func TestClassifyAPIErrorEmptyPayload(t *testing.T) {
	// No JSON, no message — should fall back to "unknown API error".
	err := ClassifyAPIError(500, "")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T (%v)", err, err)
	}
	if apiErr.StatusCode != 500 {
		t.Fatalf("expected status 500, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "unknown API error" {
		t.Fatalf("expected fallback message, got %q", apiErr.Message)
	}
}

func TestClassifyAPIErrorRateLimitByBody(t *testing.T) {
	// 500 status but body looks like a rate limit — should be classified as RateLimitError.
	err := ClassifyAPIError(500, `{"error":{"message":"rate limit exceeded"}}`)
	var rlErr *RateLimitError
	if !errors.As(err, &rlErr) {
		t.Fatalf("expected RateLimitError, got %T (%v)", err, err)
	}
}
