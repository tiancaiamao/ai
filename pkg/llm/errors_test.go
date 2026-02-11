package llm

import (
	"errors"
	"testing"
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
