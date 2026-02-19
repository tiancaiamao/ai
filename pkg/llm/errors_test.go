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
