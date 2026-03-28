package llm

import "testing"

func TestBuildAnthropicRequestUsesConfiguredMaxTokens(t *testing.T) {
	req := buildAnthropicRequest(Model{
		ID:        "test-model",
		MaxTokens: 123456,
	}, LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "hello"},
		},
	})

	got, ok := req["max_tokens"].(int)
	if !ok {
		t.Fatalf("expected int max_tokens, got %T", req["max_tokens"])
	}
	if got != 123456 {
		t.Fatalf("expected max_tokens=123456, got %d", got)
	}
}

func TestBuildAnthropicRequestUsesLargeDefaultMaxTokens(t *testing.T) {
	req := buildAnthropicRequest(Model{
		ID: "test-model",
	}, LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "hello"},
		},
	})

	got, ok := req["max_tokens"].(int)
	if !ok {
		t.Fatalf("expected int max_tokens, got %T", req["max_tokens"])
	}
	if got != defaultAnthropicMaxTokens {
		t.Fatalf("expected max_tokens=%d, got %d", defaultAnthropicMaxTokens, got)
	}
}
