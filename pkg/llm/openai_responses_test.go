package llm

import (
	"testing"
)

func TestParseProxyURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    string // expected host
	}{
		{"full url", "http://proxy:8080", false, "proxy:8080"},
		{"https", "https://proxy:8080", false, "proxy:8080"},
		{"no scheme", "proxy:8080", false, "proxy:8080"},
		{"invalid", "://bad", true, ""},
		{"empty", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProxyURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseProxyURL(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && got.Host != tt.want {
				t.Errorf("parseProxyURL(%q).Host = %q, want %q", tt.input, got.Host, tt.want)
			}
		})
	}
}

func TestBuildOpenAIResponsesRequest(t *testing.T) {
	t.Run("with system prompt", func(t *testing.T) {
		model := Model{
			ID:       "test-model",
			Provider: "openai",
		}
		ctx := LLMContext{
			SystemPrompt: "You are a test assistant.",
		}
		req := buildOpenAIResponsesRequest(model, ctx)
		input, ok := req["input"].([]map[string]any)
		if !ok {
			t.Fatal("input should be []map[string]any")
		}
		if len(input) != 1 {
			t.Fatalf("expected 1 input entry, got %d", len(input))
		}
		if input[0]["role"] != "system" {
			t.Errorf("role = %q, want 'system'", input[0]["role"])
		}
	})

	t.Run("reasoning model uses developer role", func(t *testing.T) {
		model := Model{Reasoning: true}
		ctx := LLMContext{SystemPrompt: "test"}
		req := buildOpenAIResponsesRequest(model, ctx)
		input := req["input"].([]map[string]any)
		if input[0]["role"] != "developer" {
			t.Errorf("role = %q, want 'developer' for reasoning model", input[0]["role"])
		}
	})

	t.Run("empty prompt no system entry", func(t *testing.T) {
		model := Model{}
		ctx := LLMContext{}
		req := buildOpenAIResponsesRequest(model, ctx)
		input := req["input"].([]map[string]any)
		if len(input) != 0 {
			t.Errorf("expected 0 input entries for empty prompt, got %d", len(input))
		}
	})

	t.Run("model is set in request", func(t *testing.T) {
		model := Model{ID: "my-model"}
		req := buildOpenAIResponsesRequest(model, LLMContext{})
		if req["model"] != "my-model" {
			t.Errorf("model = %q, want 'my-model'", req["model"])
		}
	})
}
