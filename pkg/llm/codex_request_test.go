package llm

import "testing"

func TestResponsesHeadersIncludesOpenCodeSession(t *testing.T) {
	headers := responsesHeaders(Model{Provider: "opencode"}, "key", "session-123")
	if got := headers.Get("x-opencode-session"); got != "session-123" {
		t.Fatalf("x-opencode-session = %q, want session-123", got)
	}
}

func TestCodexRequestBodyIncludesAllTurnsReasoningContext(t *testing.T) {
	model := Model{ID: "gpt-5.6", API: "openai-codex-responses"}
	req := codexRequestBody(model, LLMContext{})
	reasoning, ok := req["reasoning"].(map[string]any)
	if !ok {
		t.Fatal("missing reasoning parameter")
	}
	if reasoning["context"] != "all_turns" {
		t.Errorf("reasoning.context = %v, want all_turns", reasoning["context"])
	}
}
