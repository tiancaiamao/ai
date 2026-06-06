package llm

import (
	"encoding/json"
	"strings"
	"sync"
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

func TestLLMMessageMarshalJSONWithContentParts(t *testing.T) {
	msg := LLMMessage{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "text", Text: "hello"},
			{Type: "image_url", ImageURL: &struct {
				URL string `json:"url"`
			}{URL: "http://example.com/x.png"}},
		},
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	contentRaw, ok := parsed["content"]
	if !ok {
		t.Fatalf("expected content field, got: %s", raw)
	}

	var parts []map[string]any
	if err := json.Unmarshal(contentRaw, &parts); err != nil {
		t.Fatalf("expected content to be an array, got %s: %v", contentRaw, err)
	}
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	if parts[0]["type"] != "text" || parts[1]["type"] != "image_url" {
		t.Fatalf("unexpected parts: %#v", parts)
	}
}

// Tests for event accessor methods — covers GetEventType() on all variants.

func TestLLMEventTypes(t *testing.T) {
	tests := []struct {
		name  string
		event LLMEvent
		want  string
	}{
		{"start", LLMStartEvent{Partial: NewPartialMessage()}, "start"},
		{"text_delta", LLMTextDeltaEvent{Delta: "x", Index: 0}, "text_delta"},
		{"thinking_delta", LLMThinkingDeltaEvent{Delta: "y", Index: 1}, "thinking_delta"},
		{"tool_call_delta", LLMToolCallDeltaEvent{Index: 2, ToolCall: &ToolCall{ID: "t1"}}, "tool_call_delta"},
		{"done", LLMDoneEvent{Usage: Usage{TotalTokens: 42}, StopReason: "stop"}, "done"},
		{"error", LLMErrorEvent{Error: errFoo}, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.GetEventType(); got != tt.want {
				t.Errorf("GetEventType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// errFoo is a small sentinel reused across table-driven tests in this file.
var errFoo = newSentinel("foo")

func newSentinel(s string) error {
	return &sentinelErr{s: s}
}

type sentinelErr struct{ s string }

func (e *sentinelErr) Error() string { return e.s }

// PartialMessage methods — AppendText/AppendThinking/AppendToolCall merge paths
// and ToLLMMessage conversion including the thinking and tool-calls branches.

func TestPartialMessageAppendTextAndThinking(t *testing.T) {
	pm := NewPartialMessage()
	pm.AppendText("hello ")
	pm.AppendText("world")
	pm.AppendThinking("hmm")
	pm.AppendThinking(" more")

	msg := pm.ToLLMMessage()
	if msg.Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", msg.Role)
	}
	if msg.Content != "hello world" {
		t.Fatalf("expected 'hello world', got %q", msg.Content)
	}
	if msg.Thinking != "hmm more" {
		t.Fatalf("expected 'hmm more', got %q", msg.Thinking)
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("expected no tool calls, got %d", len(msg.ToolCalls))
	}
}

func TestPartialMessageToLLMMessageOmitsEmptyThinking(t *testing.T) {
	pm := NewPartialMessage()
	pm.AppendText("only text")
	msg := pm.ToLLMMessage()
	if msg.Thinking != "" {
		t.Fatalf("expected empty thinking, got %q", msg.Thinking)
	}
	if msg.Content != "only text" {
		t.Fatalf("expected 'only text', got %q", msg.Content)
	}
}

func TestPartialMessageAppendToolCallNewAndMerge(t *testing.T) {
	pm := NewPartialMessage()

	// First push creates the entry at index 0.
	pm.AppendToolCall(0, &ToolCall{
		ID:   "id-1",
		Type: "function",
		Function: FunctionCall{
			Name:      "read",
			Arguments: `{"path":"a"}`,
		},
	})

	// Second push merges into the existing entry: arguments are concatenated.
	pm.AppendToolCall(0, &ToolCall{
		Function: FunctionCall{
			Arguments: `{"path":"b"}`,
		},
	})

	// A different index creates a separate entry.
	pm.AppendToolCall(1, &ToolCall{
		ID:   "id-2",
		Type: "function",
		Function: FunctionCall{
			Name:      "write",
			Arguments: `{"x":1}`,
		},
	})

	msg := pm.ToLLMMessage()
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d (%+v)", len(msg.ToolCalls), msg.ToolCalls)
	}

	// Index ordering should follow insertion order (0 before 1).
	tc0 := msg.ToolCalls[0]
	if tc0.ID != "id-1" || tc0.Function.Name != "read" {
		t.Fatalf("unexpected first tool call: %+v", tc0)
	}
	if tc0.Function.Arguments != `{"path":"a"}{"path":"b"}` {
		t.Fatalf("expected merged arguments, got %q", tc0.Function.Arguments)
	}

	tc1 := msg.ToolCalls[1]
	if tc1.ID != "id-2" || tc1.Function.Name != "write" {
		t.Fatalf("unexpected second tool call: %+v", tc1)
	}
}

func TestPartialMessageAppendToolCallMergeOverwritesIDAndType(t *testing.T) {
	pm := NewPartialMessage()
	pm.AppendToolCall(0, &ToolCall{ID: "old", Type: "old_type"})
	pm.AppendToolCall(0, &ToolCall{ID: "new", Type: "new_type"})

	msg := pm.ToLLMMessage()
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.ID != "new" || tc.Type != "new_type" {
		t.Fatalf("expected ID=new Type=new_type, got %+v", tc)
	}
}

func TestPartialMessageConcurrentAppends(t *testing.T) {
	// Smoke test: concurrent AppendText calls must not race.
	pm := NewPartialMessage()
	const N = 50
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			pm.AppendText("a")
		}()
	}
	wg.Wait()

	msg := pm.ToLLMMessage()
	if len(msg.Content) != N {
		t.Fatalf("expected content len %d, got %d", N, len(msg.Content))
	}
}
