package llm

import "testing"

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
