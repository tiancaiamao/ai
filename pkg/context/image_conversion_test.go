package context

import (
	"encoding/json"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestConvertMessagesToLLMToolResultWithImage(t *testing.T) {
	// Simulate: user asks to describe image → assistant calls read → tool returns image
	assistant := NewAssistantMessage()
	assistant.Content = []ContentBlock{
		ThinkingContent{Type: "thinking", Thinking: "用户想让我读取一个 PNG 文件并描述内容。"},
		ToolCallContent{ID: "call_1", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "/tmp/yeppy.png"}},
	}

	msgs := []AgentMessage{
		NewUserMessage("描述一下这张图片 /tmp/yeppy.png"),
		assistant,
		NewToolResultMessage("call_1", "read", []ContentBlock{
			TextContent{Type: "text", Text: "Read image file [image/png]"},
			ImageContent{Type: "image", Data: "iVBORw0KGgoAAAANSUhEUg...", MimeType: "image/png"},
		}, false),
	}

	got := ConvertMessagesToLLM(msgs)

	t.Logf("Number of messages converted: %d", len(got))
	for i, m := range got {
		// Verify no tool message has ContentParts
		if m.Role == "tool" {
			if len(m.ContentParts) > 0 {
				t.Errorf("tool message %d has %d ContentParts, want 0", i, len(m.ContentParts))
			}
			if m.Content != "Read image file [image/png]" {
				t.Errorf("tool message %d content = %q, want %q", i, m.Content, "Read image file [image/png]")
			}
		}

		// Marshal to JSON and log
		b, _ := json.Marshal(m)
		t.Logf("  message[%d] (role=%s): %s", i, m.Role, string(b))
	}

	// Now marshal the entire request to see exactly what the API receives
	type request struct {
		Model    string           `json:"model"`
		Messages []llm.LLMMessage `json:"messages"`
	}
	req := request{Model: "ollama/ornith:latest", Messages: got}
	b, _ := json.Marshal(req)
	t.Logf("Full request JSON: %s", string(b))

	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4 (user + assistant + tool + synthetic user)", len(got))
	}

	// Verify: the original user message (index 0) has only text, no images
	if got[0].Role != "user" {
		t.Errorf("message[0] role = %q, want user", got[0].Role)
	}
	if got[0].Content != "描述一下这张图片 /tmp/yeppy.png" {
		t.Errorf("message[0] content = %q, want original text", got[0].Content)
	}
	if len(got[0].ContentParts) > 0 {
		t.Errorf("message[0] should not have ContentParts (images go in synthetic msg), got %d", len(got[0].ContentParts))
	}

	// Verify: the synthetic user message (index 3) has the image
	if got[3].Role != "user" {
		t.Errorf("message[3] role = %q, want user (synthetic)", got[3].Role)
	}
	if got[3].Content != "" {
		t.Errorf("message[3] Content = %q, want empty (should use ContentParts)", got[3].Content)
	}
	hasImage := false
	hasText := false
	for _, cp := range got[3].ContentParts {
		if cp.Type == "image_url" {
			hasImage = true
			wantURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg..."
			if cp.ImageURL.URL != wantURL {
				t.Errorf("synthetic user message image URL = %q, want %q", cp.ImageURL.URL, wantURL)
			}
		}
		if cp.Type == "text" {
			hasText = true
			if cp.Text != "Attached image(s) from tool result:" {
				t.Errorf("synthetic user message text = %q, want %q", cp.Text, "Attached image(s) from tool result:")
			}
		}
	}
	if !hasImage {
		t.Error("synthetic user message has no image_url content part")
	}
	if !hasText {
		t.Error("synthetic user message has no text content part")
	}
}
