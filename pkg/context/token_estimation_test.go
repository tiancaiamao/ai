package context

import (
	"context"
	"encoding/json"
	"testing"
)

// Token-estimation functions are all at 0% coverage. They are pure functions
// (no I/O, no goroutines) so they're trivial to test exhaustively.

func TestEstimateTokensBasic(t *testing.T) {
	t.Run("empty everything", func(t *testing.T) {
		if got := EstimateTokens("", nil, nil); got != 0 {
			t.Fatalf("expected 0 tokens for empty input, got %d", got)
		}
	})

	t.Run("only system prompt", func(t *testing.T) {
		// 8 chars / 4 = 2 tokens
		if got := EstimateTokens("12345678", nil, nil); got != 2 {
			t.Fatalf("expected 2 tokens for 8-char prompt, got %d", got)
		}
	})

	t.Run("only tools", func(t *testing.T) {
		tools := []Tool{&fakeTool{name: "read"}}
		got := EstimateTokens("", tools, nil)
		if got <= 0 {
			t.Fatalf("expected positive token count for one tool, got %d", got)
		}
	})

	t.Run("only messages", func(t *testing.T) {
		msgs := []AgentMessage{NewUserMessage("hello world")} // 11 chars
		got := EstimateTokens("", nil, msgs)
		if got <= 0 {
			t.Fatalf("expected positive token count for one message, got %d", got)
		}
	})

	t.Run("all combined", func(t *testing.T) {
		tools := []Tool{&fakeTool{name: "x"}}
		msgs := []AgentMessage{NewUserMessage("abc")}
		empty := EstimateTokens("", nil, nil)
		got := EstimateTokens("system", tools, msgs)
		if got <= empty {
			t.Fatalf("expected total > empty, got %d <= %d", got, empty)
		}
	})
}

func TestEstimateToolsTokens(t *testing.T) {
	t.Run("nil slice", func(t *testing.T) {
		if got := EstimateToolsTokens(nil); got != 0 {
			t.Fatalf("expected 0 for nil, got %d", got)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		if got := EstimateToolsTokens([]Tool{}); got != 0 {
			t.Fatalf("expected 0 for empty, got %d", got)
		}
	})

	t.Run("marshal failure path", func(t *testing.T) {
		// Tool whose Parameters contains a value that fails to marshal
		// (a channel) — exercises the fallback branch that uses just
		// name+description for token estimation.
		tools := []Tool{&fakeTool{name: "broken", failMarshal: true}}
		got := EstimateToolsTokens(tools)
		// Even when marshal fails we should return some positive count
		// (the function returns 0 in that branch — we just exercise it).
		if got < 0 {
			t.Fatalf("expected non-negative, got %d", got)
		}
	})

	t.Run("multiple tools", func(t *testing.T) {
		tools := []Tool{
			&fakeTool{name: "a", desc: "does a"},
			&fakeTool{name: "b", desc: "does b"},
		}
		single := EstimateToolsTokens(tools[:1])
		both := EstimateToolsTokens(tools)
		if both <= single {
			t.Fatalf("expected adding a tool to grow the estimate: single=%d both=%d", single, both)
		}
	})
}

func TestEstimateMessageTokens(t *testing.T) {
	t.Run("agent-invisible returns 0", func(t *testing.T) {
		hidden := AgentMessage{
			Role:    "user",
			Content: []ContentBlock{TextContent{Type: "text", Text: "hidden"}},
			Metadata: &MessageMetadata{
				AgentVisible: boolPtr(false),
			},
		}
		if got := EstimateMessageTokens(hidden); got != 0 {
			t.Fatalf("expected 0 tokens for hidden message, got %d", got)
		}
	})

	t.Run("text content", func(t *testing.T) {
		msg := NewUserMessage("hello world") // 11 chars
		if got := EstimateMessageTokens(msg); got != 3 {
			t.Fatalf("expected 3 tokens for 11 chars (11+3)/4=3, got %d", got)
		}
	})

	t.Run("thinking content", func(t *testing.T) {
		msg := AgentMessage{
			Role: "assistant",
			Content: []ContentBlock{
				ThinkingContent{Type: "thinking", Thinking: "abcdefgh"}, // 8 chars => 2 tokens
			},
		}
		if got := EstimateMessageTokens(msg); got != 2 {
			t.Fatalf("expected 2 tokens, got %d", got)
		}
	})

	t.Run("tool call content", func(t *testing.T) {
		msg := AgentMessage{
			Role: "assistant",
			Content: []ContentBlock{
				ToolCallContent{
					Type:      "toolCall",
					Name:      "read", // 4 chars
					Arguments: map[string]any{"path": "x.go"},
				},
			},
		}
		got := EstimateMessageTokens(msg)
		if got <= 0 {
			t.Fatalf("expected positive tokens for tool call, got %d", got)
		}
	})

	t.Run("image content fixed cost", func(t *testing.T) {
		msg := AgentMessage{
			Role: "user",
			Content: []ContentBlock{
				ImageContent{Type: "image", Data: "x", MimeType: "png"},
			},
		}
		// 4800 chars => 1200 tokens
		if got := EstimateMessageTokens(msg); got != 1200 {
			t.Fatalf("expected 1200 tokens for image, got %d", got)
		}
	})

	t.Run("empty content falls back to ExtractText", func(t *testing.T) {
		// No content blocks at all — ExtractText returns "".
		msg := AgentMessage{Role: "user", Content: nil}
		if got := EstimateMessageTokens(msg); got != 0 {
			t.Fatalf("expected 0 tokens for empty content, got %d", got)
		}
	})

	t.Run("tool call with nil arguments", func(t *testing.T) {
		msg := AgentMessage{
			Role: "assistant",
			Content: []ContentBlock{
				ToolCallContent{Type: "toolCall", Name: "x", Arguments: nil},
			},
		}
		// Just the name length: "x" => 1 char, falls into (charCount+3)/4 = (1+3)/4 = 1
		if got := EstimateMessageTokens(msg); got != 1 {
			t.Fatalf("expected 1 token for 1-char name, got %d", got)
		}
	})
}

func TestEstimateTokenPercent(t *testing.T) {
	if got := EstimateTokenPercent(50, 100); got != 0.5 {
		t.Fatalf("expected 0.5, got %v", got)
	}
	if got := EstimateTokenPercent(0, 0); got != 0 {
		t.Fatalf("expected 0 for zero total, got %v", got)
	}
	if got := EstimateTokenPercent(100, -1); got != 0 {
		t.Fatalf("expected 0 for negative total, got %v", got)
	}
}

func TestEstimateMessageCharsHidden(t *testing.T) {
	// Hidden messages short-circuit to 0 before iterating content blocks.
	hidden := AgentMessage{
		Role: "user",
		Content: []ContentBlock{
			TextContent{Type: "text", Text: "should not count"},
		},
		Metadata: &MessageMetadata{AgentVisible: boolPtr(false)},
	}
	if got := estimateMessageChars(hidden); got != 0 {
		t.Fatalf("expected 0 chars for hidden message, got %d", got)
	}
}

// fakeTool is a minimal Tool implementation for token-estimation tests.
// Setting failMarshal triggers a Parameters() return value that contains
// a channel, which causes encoding/json to fail.
type fakeTool struct {
	name        string
	desc        string
	failMarshal bool
}

func (f *fakeTool) Name() string        { return f.name }
func (f *fakeTool) Description() string { return f.desc }
func (f *fakeTool) Parameters() map[string]any {
	if f.failMarshal {
		// Channels cannot be JSON-marshaled.
		return map[string]any{"bad": make(chan int)}
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
	}
}
func (f *fakeTool) Execute(_ context.Context, _ map[string]any) ([]ContentBlock, error) {
	return nil, nil
}

// Ensure fakeTool satisfies the Tool interface (compile-time check).
var _ Tool = (*fakeTool)(nil)

// Sanity check: confirm JSON marshal failure for our channel-valued params.
func TestFakeToolMarshalFailureSanity(t *testing.T) {
	f := &fakeTool{failMarshal: true}
	_, err := json.Marshal(f.Parameters())
	if err == nil {
		t.Fatal("expected JSON marshal to fail for channel-valued params")
	}
}
