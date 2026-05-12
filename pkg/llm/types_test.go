package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLLMMessageMarshalJSONWithThinking(t *testing.T) {
	msg := LLMMessage{
		Role:     "assistant",
		Content:  "Hello",
		Thinking: "Let me think about this...",
		ToolCalls: []ToolCall{
			{
				ID:   "call-1",
				Type: "function",
				Function: FunctionCall{
					Name:      "read",
					Arguments: `{"path":"a.go"}`,
				},
			},
		},
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Verify reasoning_content is present in JSON output
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal to map failed: %v", err)
	}

	if _, ok := parsed["reasoning_content"]; !ok {
		t.Fatalf("expected reasoning_content field in JSON, got: %s", raw)
	}

	var reasoningContent string
	if err := json.Unmarshal(parsed["reasoning_content"], &reasoningContent); err != nil {
		t.Fatalf("unmarshal reasoning_content failed: %v", err)
	}
	if reasoningContent != "Let me think about this..." {
		t.Fatalf("expected reasoning_content 'Let me think about this...', got %q", reasoningContent)
	}

	// Verify content is also present
	if _, ok := parsed["content"]; !ok {
		t.Fatalf("expected content field in JSON, got: %s", raw)
	}

	// Verify tool_calls is present
	if _, ok := parsed["tool_calls"]; !ok {
		t.Fatalf("expected tool_calls field in JSON, got: %s", raw)
	}

	t.Logf("JSON: %s", raw)
}

func TestLLMMessageMarshalJSONWithoutThinking(t *testing.T) {
	msg := LLMMessage{
		Role:    "assistant",
		Content: "Hello",
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// When thinking is empty, reasoning_content should be omitted (omitempty)
	if strings.Contains(string(raw), "reasoning_content") {
		t.Fatalf("expected no reasoning_content field for empty thinking, got: %s", raw)
	}
}

func TestLLMMessageMarshalJSONToolResult(t *testing.T) {
	msg := LLMMessage{
		Role:       "tool",
		Content:    "file contents here",
		ToolCallID: "call-1",
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	// Tool messages should not have reasoning_content
	if strings.Contains(string(raw), "reasoning_content") {
		t.Fatalf("expected no reasoning_content for tool messages, got: %s", raw)
	}
}