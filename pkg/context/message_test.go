package context

import (
	"encoding/json"
	"strings"
	"testing"
)

// ContentBlock markers are 0% covered — quick smoke tests for each type.
func TestIsTruncated(t *testing.T) {
	if (AgentMessage{Truncated: false}).IsTruncated() {
		t.Fatal("expected false")
	}
	if !(AgentMessage{Truncated: true}).IsTruncated() {
		t.Fatal("expected true")
	}
}

func TestNewCompactionSummaryMessage(t *testing.T) {
	msg := NewCompactionSummaryMessage("the summary")
	if msg.Role != "user" {
		t.Fatalf("expected role user, got %q", msg.Role)
	}
	if msg.Metadata == nil || msg.Metadata.Kind != "compactionSummary" {
		t.Fatalf("expected kind=compactionSummary, got %+v", msg.Metadata)
	}
	if !strings.Contains(msg.ExtractText(), "the summary") {
		t.Fatalf("expected text to contain summary, got %q", msg.ExtractText())
	}
	if !strings.Contains(msg.ExtractText(), "[Previous conversation summary]") {
		t.Fatalf("expected text to contain header, got %q", msg.ExtractText())
	}
}

func TestOutputHash(t *testing.T) {
	t.Run("non-toolResult returns empty", func(t *testing.T) {
		if (AgentMessage{Role: "user"}).OutputHash() != "" {
			t.Fatal("expected empty for non-toolResult")
		}
	})

	t.Run("empty content returns empty", func(t *testing.T) {
		m := AgentMessage{Role: "toolResult", Content: nil}
		if m.OutputHash() != "" {
			t.Fatal("expected empty for no text")
		}
	})

	t.Run("same input produces same hash", func(t *testing.T) {
		m1 := NewToolResultMessage("tc1", "read", []ContentBlock{TextContent{Type: "text", Text: "output"}}, false)
		m2 := NewToolResultMessage("tc1", "read", []ContentBlock{TextContent{Type: "text", Text: "output"}}, false)
		if m1.OutputHash() != m2.OutputHash() {
			t.Fatalf("expected equal hashes: %s vs %s", m1.OutputHash(), m2.OutputHash())
		}
	})

	t.Run("different input produces different hash", func(t *testing.T) {
		m1 := NewToolResultMessage("tc1", "read", []ContentBlock{TextContent{Type: "text", Text: "a"}}, false)
		m2 := NewToolResultMessage("tc1", "read", []ContentBlock{TextContent{Type: "text", Text: "b"}}, false)
		if m1.OutputHash() == m2.OutputHash() {
			t.Fatal("expected different hashes for different text")
		}
	})

	t.Run("hash is hex formatted", func(t *testing.T) {
		m := NewToolResultMessage("tc1", "read", []ContentBlock{TextContent{Type: "text", Text: "x"}}, false)
		h := m.OutputHash()
		if len(h) != 8 {
			t.Fatalf("expected 8 hex chars, got %d (%q)", len(h), h)
		}
		for _, c := range h {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Fatalf("expected hex, got %q", h)
			}
		}
	})
}

func TestExtractThinking(t *testing.T) {
	msg := AgentMessage{
		Role: "assistant",
		Content: []ContentBlock{
			TextContent{Type: "text", Text: "answer"},
			ThinkingContent{Type: "thinking", Thinking: "thought 1 "},
			ThinkingContent{Type: "thinking", Thinking: "thought 2"},
		},
	}
	got := msg.ExtractThinking()
	if got != "thought 1 thought 2" {
		t.Fatalf("expected concatenated thoughts, got %q", got)
	}
}

func TestExtractToolCalls(t *testing.T) {
	msg := AgentMessage{
		Role: "assistant",
		Content: []ContentBlock{
			TextContent{Type: "text", Text: "calling"},
			ToolCallContent{Type: "toolCall", ID: "t1", Name: "read"},
			ToolCallContent{Type: "toolCall", ID: "t2", Name: "write"},
		},
	}
	calls := msg.ExtractToolCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	if calls[0].ID != "t1" || calls[1].ID != "t2" {
		t.Fatalf("unexpected order: %+v", calls)
	}
}

func TestIsUserVisible(t *testing.T) {
	// Default visible.
	if !(AgentMessage{}).IsUserVisible() {
		t.Fatal("default should be visible")
	}
	// Explicitly hidden.
	hidden := AgentMessage{Metadata: &MessageMetadata{UserVisible: boolPtr(false)}}
	if hidden.IsUserVisible() {
		t.Fatal("expected hidden")
	}
}

func TestWithVisibility(t *testing.T) {
	msg := AgentMessage{Role: "user"}
	out := msg.WithVisibility(true, false)
	if !out.IsAgentVisible() {
		t.Fatal("expected agent-visible")
	}
	if out.IsUserVisible() {
		t.Fatal("expected user-hidden")
	}
	// Ensure original is untouched.
	if msg.Metadata != nil {
		t.Fatal("expected original metadata to remain nil")
	}
}

func TestWithKind(t *testing.T) {
	msg := AgentMessage{Role: "user"}
	out := msg.WithKind("custom-kind")
	if out.Metadata == nil || out.Metadata.Kind != "custom-kind" {
		t.Fatalf("expected kind='custom-kind', got %+v", out.Metadata)
	}
	// WithKind is a value receiver — original is unmodified (Metadata stays nil).
	if msg.Metadata != nil {
		t.Fatal("expected original metadata to remain nil (value receiver)")
	}
}

func TestAgentMessageUnmarshalJSONAllBlockTypes(t *testing.T) {
	// Construct a JSON message containing all 4 content block types.
	jsonStr := `{
		"role": "assistant",
		"content": [
			{"type": "text", "text": "hello"},
			{"type": "image", "data": "base64", "mimeType": "png"},
			{"type": "toolCall", "id": "t1", "name": "read", "arguments": {"path": "x.go"}},
			{"type": "thinking", "thinking": "hmm"}
		],
		"timestamp": 12345,
		"metadata": {"kind": "assistant"},
		"api": "openai",
		"provider": "zai",
		"model": "gpt-4",
				"usage": {"totalTokens": 100},
		"stopReason": "stop",
		"toolCallId": "t1",
		"toolName": "read",
		"isError": false,
		"truncated": true,
		"truncated_at": 7,
		"original_size": 999
	}`

	var m AgentMessage
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if m.Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", m.Role)
	}
	if len(m.Content) != 4 {
		t.Fatalf("expected 4 blocks, got %d", len(m.Content))
	}
	if _, ok := m.Content[0].(TextContent); !ok {
		t.Fatalf("expected TextContent at index 0, got %T", m.Content[0])
	}
	if _, ok := m.Content[1].(ImageContent); !ok {
		t.Fatalf("expected ImageContent at index 1, got %T", m.Content[1])
	}
	if _, ok := m.Content[2].(ToolCallContent); !ok {
		t.Fatalf("expected ToolCallContent at index 2, got %T", m.Content[2])
	}
	if _, ok := m.Content[3].(ThinkingContent); !ok {
		t.Fatalf("expected ThinkingContent at index 3, got %T", m.Content[3])
	}
	if !m.Truncated || m.TruncatedAt != 7 || m.OriginalSize != 999 {
		t.Fatalf("expected truncation fields preserved, got %+v", m)
	}
	if m.Usage == nil || m.Usage.TotalTokens != 100 {
		t.Fatalf("expected usage preserved, got %+v", m.Usage)
	}
}

func TestAgentMessageUnmarshalJSONEdgeCases(t *testing.T) {
	t.Run("malformed JSON returns error", func(t *testing.T) {
		var m AgentMessage
		if err := m.UnmarshalJSON([]byte(`not json`)); err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})

	t.Run("block with malformed type-field is skipped", func(t *testing.T) {
		// type field can't be unmarshaled into a string from object — should continue.
		jsonStr := `{"role":"user","content":[{"type":{"nested":"bad"}},{"type":"text","text":"ok"}]}`
		var m AgentMessage
		if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if len(m.Content) != 1 {
			t.Fatalf("expected 1 block (malformed skipped), got %d", len(m.Content))
		}
	})

	t.Run("unknown block type is skipped", func(t *testing.T) {
		jsonStr := `{"role":"user","content":[{"type":"unknown","foo":"bar"},{"type":"text","text":"ok"}]}`
		var m AgentMessage
		if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if len(m.Content) != 1 {
			t.Fatalf("expected 1 block (unknown skipped), got %d", len(m.Content))
		}
	})

	t.Run("empty content array", func(t *testing.T) {
		jsonStr := `{"role":"user","content":[]}`
		var m AgentMessage
		if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if len(m.Content) != 0 {
			t.Fatalf("expected 0 blocks, got %d", len(m.Content))
		}
	})

	t.Run("block that fails inner unmarshal is skipped", func(t *testing.T) {
		// "text" type but with text field being an object — inner unmarshal fails.
		jsonStr := `{"role":"user","content":[{"type":"text","text":{"nested":"bad"}}]}`
		var m AgentMessage
		if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if len(m.Content) != 0 {
			t.Fatalf("expected 0 blocks (inner unmarshal failed), got %d", len(m.Content))
		}
	})
}
