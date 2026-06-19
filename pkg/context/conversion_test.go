package context

import (
	"context"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestConvertMessagesToLLM(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := ConvertMessagesToLLM(nil); len(got) != 0 {
			t.Errorf("expected empty, got %d", len(got))
		}
	})

	t.Run("basic user message", func(t *testing.T) {
		msgs := []AgentMessage{
			{Role: "user", Content: []ContentBlock{TextContent{Type: "text", Text: "hello"}}},
		}
		got := ConvertMessagesToLLM(msgs)
		if len(got) != 1 {
			t.Fatalf("got %d messages, want 1", len(got))
		}
		if got[0].Role != "user" || got[0].Content != "hello" {
			t.Errorf("got %+v", got[0])
		}
	})

	t.Run("toolResult role maps to tool", func(t *testing.T) {
		msgs := []AgentMessage{
			{
				Role:       "assistant",
				Content:    []ContentBlock{TextContent{Type: "text", Text: ""}},
				ToolCallID: "",
			},
			{
				Role:       "toolResult",
				ToolCallID: "tc1",
				Content:    []ContentBlock{TextContent{Type: "text", Text: "result"}},
			},
		}
		// Need proper tool call for sanitization to keep the tool message
		msgs[0].Content = []ContentBlock{
			TextContent{Type: "text", Text: "ok"},
			ToolCallContent{Type: "tool_call", ID: "tc1", Name: "bash", Arguments: map[string]any{"command": "ls"}},
		}
		got := ConvertMessagesToLLM(msgs)
		if len(got) != 2 {
			t.Fatalf("got %d messages, want 2", len(got))
		}
		if got[1].Role != "tool" {
			t.Errorf("role = %q, want tool", got[1].Role)
		}
		if got[1].ToolCallID != "tc1" {
			t.Errorf("ToolCallID = %q, want tc1", got[1].ToolCallID)
		}
	})

	t.Run("assistant with tool calls", func(t *testing.T) {
		msgs := []AgentMessage{
			{
				Role: "assistant",
				Content: []ContentBlock{
					TextContent{Type: "text", Text: "running"},
					ToolCallContent{Type: "tool_call", ID: "tc1", Name: "bash", Arguments: map[string]any{"command": "ls"}},
				},
			},
			{
				Role:       "toolResult",
				ToolCallID: "tc1",
				Content:    []ContentBlock{TextContent{Type: "text", Text: "done"}},
			},
		}
		got := ConvertMessagesToLLM(msgs)
		if len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
		if len(got[0].ToolCalls) != 1 {
			t.Fatalf("ToolCalls len = %d, want 1", len(got[0].ToolCalls))
		}
		if got[0].ToolCalls[0].ID != "tc1" {
			t.Errorf("ToolCall ID = %q, want tc1", got[0].ToolCalls[0].ID)
		}
	})

	t.Run("image content", func(t *testing.T) {
		msgs := []AgentMessage{
			{
				Role: "user",
				Content: []ContentBlock{
					ImageContent{Type: "image", Data: "base64data"},
				},
			},
		}
		got := ConvertMessagesToLLM(msgs)
		if len(got[0].ContentParts) != 1 {
			t.Fatalf("ContentParts len = %d, want 1", len(got[0].ContentParts))
		}
		if got[0].ContentParts[0].ImageURL.URL != "base64data" {
			t.Errorf("ImageURL = %v", got[0].ContentParts[0].ImageURL)
		}
	})

	t.Run("thinking content", func(t *testing.T) {
		msgs := []AgentMessage{
			{
				Role: "assistant",
				Content: []ContentBlock{
					ThinkingContent{Type: "thinking", Thinking: "hmm"},
					TextContent{Type: "text", Text: "answer"},
				},
			},
		}
		got := ConvertMessagesToLLM(msgs)
		if got[0].Thinking != "hmm" {
			t.Errorf("Thinking = %q, want hmm", got[0].Thinking)
		}
	})

	t.Run("agent-invisible filtered", func(t *testing.T) {
		hidden := false
		msgs := []AgentMessage{
			{Role: "user", Content: []ContentBlock{TextContent{Type: "text", Text: "visible"}}},
			{Role: "user", Content: []ContentBlock{TextContent{Type: "text", Text: "hidden"}}, Metadata: &MessageMetadata{AgentVisible: &hidden}},
		}
		got := ConvertMessagesToLLM(msgs)
		if len(got) != 1 {
			t.Fatalf("got %d, want 1 (invisible filtered)", len(got))
		}
	})
}

func TestSanitizeToolCallProtocol(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := sanitizeToolCallProtocol(nil); len(got) != 0 {
			t.Errorf("expected empty")
		}
	})

	t.Run("orphan tool message dropped", func(t *testing.T) {
		msgs := []llm.LLMMessage{
			{Role: "user", Content: "hi"},
			{Role: "tool", ToolCallID: "orphan", Content: "result"},
		}
		got := sanitizeToolCallProtocol(msgs)
		if len(got) != 1 {
			t.Fatalf("got %d, want 1 (orphan dropped)", len(got))
		}
	})

	t.Run("paired tool call kept", func(t *testing.T) {
		msgs := []llm.LLMMessage{
			{Role: "assistant", Content: "ok", ToolCalls: []llm.ToolCall{{ID: "tc1", Type: "function"}}},
			{Role: "tool", ToolCallID: "tc1", Content: "result"},
		}
		got := sanitizeToolCallProtocol(msgs)
		if len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
	})

	t.Run("unresolved tool call stripped", func(t *testing.T) {
		msgs := []llm.LLMMessage{
			{Role: "assistant", Content: "", ToolCalls: []llm.ToolCall{{ID: "tc1", Type: "function"}}},
			{Role: "user", Content: "next"},
		}
		got := sanitizeToolCallProtocol(msgs)
		if len(got) != 1 {
			t.Fatalf("got %d, want 1", len(got))
		}
		if got[0].Role != "user" {
			t.Errorf("role = %q, want user", got[0].Role)
		}
	})

	t.Run("assistant with content and unresolved call kept", func(t *testing.T) {
		msgs := []llm.LLMMessage{
			{Role: "assistant", Content: "text", ToolCalls: []llm.ToolCall{{ID: "tc1", Type: "function"}}},
			{Role: "user", Content: "next"},
		}
		got := sanitizeToolCallProtocol(msgs)
		// assistant kept (has content) but tool call stripped, then user
		if len(got) != 2 {
			t.Fatalf("got %d, want 2", len(got))
		}
		if len(got[0].ToolCalls) != 0 {
			t.Errorf("tool calls should be stripped, got %d", len(got[0].ToolCalls))
		}
	})
}

type mockTool struct {
	name        string
	description string
	params      map[string]any
}

func (m mockTool) Name() string               { return m.name }
func (m mockTool) Description() string        { return m.description }
func (m mockTool) Parameters() map[string]any { return m.params }
func (m mockTool) Execute(_ context.Context, _ map[string]any) ([]ContentBlock, error) {
	return nil, nil
}

func TestConvertToolsToLLM(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := ConvertToolsToLLM(nil); len(got) != 0 {
			t.Errorf("expected empty")
		}
	})

	t.Run("dedup by name", func(t *testing.T) {
		tools := []Tool{
			mockTool{name: "bash", description: "run bash", params: map[string]any{"type": "object"}},
			mockTool{name: "bash", description: "duplicate", params: map[string]any{"type": "object"}},
			mockTool{name: "read", description: "read file", params: map[string]any{"type": "object"}},
		}
		got := ConvertToolsToLLM(tools)
		if len(got) != 2 {
			t.Fatalf("got %d tools, want 2 (deduped)", len(got))
		}
		if got[0].Function.Name != "bash" {
			t.Errorf("first tool = %q, want bash", got[0].Function.Name)
		}
	})

	t.Run("nil tool skipped", func(t *testing.T) {
		tools := []Tool{
			nil,
			mockTool{name: "bash", description: "run", params: nil},
		}
		got := ConvertToolsToLLM(tools)
		if len(got) != 1 {
			t.Fatalf("got %d, want 1", len(got))
		}
	})
}

func TestNewAssistantMessage(t *testing.T) {
	msg := NewAssistantMessage()
	if msg.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", msg.Role)
	}
	if msg.Metadata == nil || msg.Metadata.Kind != "assistant" {
		t.Errorf("Metadata.Kind not set correctly")
	}
}

func TestIsContentBlock(t *testing.T) {
	// Just verify they don't panic
	var b ContentBlock
	b = TextContent{Type: "text", Text: "hi"}
	b.IsContentBlock()
	b = ImageContent{Type: "image", Data: "..."}
	b.IsContentBlock()
	b = ToolCallContent{Type: "tool_call", ID: "1"}
	b.IsContentBlock()
	b = ThinkingContent{Type: "thinking", Thinking: "..."}
	b.IsContentBlock()
}
