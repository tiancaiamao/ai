package agent

import "testing"

func TestInferLLMErrorTypeFromMessage(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected string
	}{
		{"empty", "", llmErrorTypeUnknown},
		{"rate limit", "rate limit exceeded", llmErrorTypeRateLimit},
		{"429 status", "HTTP 429 Too Many Requests", llmErrorTypeRateLimit},
		{"quota", "quota exceeded", llmErrorTypeRateLimit},
		{"timeout", "context deadline exceeded", llmErrorTypeTimeout},
		{"timed out", "request timed out", llmErrorTypeTimeout},
		{"timeout keyword", "timeout waiting for response", llmErrorTypeTimeout},
		{"context length", "context length exceeded", llmErrorTypeContextLimit},
		{"context window", "context window too large", llmErrorTypeContextLimit},
		{"token limit", "token limit reached", llmErrorTypeContextLimit},
		{"connection", "connection refused", llmErrorTypeNetwork},
		{"dns", "dns resolution failed", llmErrorTypeNetwork},
		{"dial tcp", "dial tcp: connection refused", llmErrorTypeNetwork},
		{"no such host", "no such host", llmErrorTypeNetwork},
		{"eof", "unexpected eof", llmErrorTypeNetwork},
		{"server error 500", "api error (500) internal", llmErrorTypeServer},
		{"service unavailable", "service unavailable", llmErrorTypeServer},
		{"bad gateway", "bad gateway", llmErrorTypeServer},
		// "gateway timeout" contains "timeout" which matches the timeout
		// branch before the server branch — documented actual behavior.
		{"gateway timeout", "gateway timeout", llmErrorTypeTimeout},
		{"client error 400", "api error (400) bad request", llmErrorTypeClient},
		{"unauthorized", "unauthorized access", llmErrorTypeClient},
		{"forbidden", "forbidden", llmErrorTypeClient},
		{"context canceled", "context canceled", llmErrorTypeCanceled},
		{"cancelled", "operation cancelled", llmErrorTypeCanceled},
		{"unknown error", "something went wrong", llmErrorTypeUnknown},
		{"case insensitive", "RATE LIMIT EXCEEDED", llmErrorTypeRateLimit},
		{"whitespace", "  quota exceeded  ", llmErrorTypeRateLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferLLMErrorTypeFromMessage(tt.message)
			if got != tt.expected {
				t.Errorf("inferLLMErrorTypeFromMessage(%q) = %q, want %q", tt.message, got, tt.expected)
			}
		})
	}
}
