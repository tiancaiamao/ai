package context

import (
	"encoding/json"
	"testing"
)

func TestConvertMessagesToLLMToolResultWithVideo(t *testing.T) {
	assistant := NewAssistantMessage()
	assistant.Content = []ContentBlock{
		ToolCallContent{ID: "call_1", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "/tmp/clip.mp4"}},
	}

	msgs := []AgentMessage{
		NewUserMessage("描述一下这个视频 /tmp/clip.mp4"),
		assistant,
		NewToolResultMessage("call_1", "read", []ContentBlock{
			TextContent{Type: "text", Text: "Read video file [video/mp4]"},
			VideoContent{Type: "video", Data: "AAAA", MimeType: "video/mp4"},
		}, false),
	}

	got := ConvertMessagesToLLM(msgs)
	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4 (user + assistant + tool + synthetic user)", len(got))
	}

	// Tool message stays text-only.
	toolMsg := got[2]
	if toolMsg.Role != "tool" || len(toolMsg.ContentParts) != 0 {
		t.Fatalf("tool message = %+v, want text-only tool message", toolMsg)
	}

	// Video is attached to a synthetic user message after the tool result.
	synthetic := got[3]
	if synthetic.Role != "user" {
		t.Fatalf("message[3] role = %q, want user", synthetic.Role)
	}
	if len(synthetic.ContentParts) == 0 || synthetic.ContentParts[0].Text != "Attached video(s) from tool result:" {
		t.Fatalf("synthetic user message text = %+v, want video label", synthetic.ContentParts)
	}
	wantURL := "data:video/mp4;base64,AAAA"
	found := false
	for _, cp := range synthetic.ContentParts {
		if cp.Type == "video_url" {
			found = true
			if cp.VideoURL == nil || cp.VideoURL.URL != wantURL {
				t.Errorf("video_url = %+v, want URL %q", cp.VideoURL, wantURL)
			}
		}
	}
	if !found {
		t.Fatal("no video_url content part in synthetic user message")
	}

	// Verify the exact wire format is video_url with a nested url object.
	b, err := json.Marshal(synthetic)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	foundWire := false
	for _, part := range wire.Content {
		if part["type"] == "video_url" {
			foundWire = true
			url, ok := part["video_url"].(map[string]any)["url"].(string)
			if !ok || url != wantURL {
				t.Errorf("wire video_url = %v, want %q", part["video_url"], wantURL)
			}
		}
	}
	if !foundWire {
		t.Fatalf("wire content has no video_url part: %s", b)
	}
}

func TestAgentMessageRoundTripVideoContent(t *testing.T) {
	original := AgentMessage{
		Role: "toolResult",
		Content: []ContentBlock{
			VideoContent{Type: "video", Data: "AAAA", MimeType: "video/mp4"},
		},
		ToolCallID: "call_1",
	}
	b, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AgentMessage
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Content) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(decoded.Content))
	}
	vc, ok := decoded.Content[0].(VideoContent)
	if !ok {
		t.Fatalf("content block = %T, want VideoContent", decoded.Content[0])
	}
	if vc.Data != "AAAA" || vc.MimeType != "video/mp4" || vc.Type != "video" {
		t.Errorf("decoded video = %+v", vc)
	}
}

func TestConvertMessagesToLLMImageAndVideoLabel(t *testing.T) {
	assistant := NewAssistantMessage()
	assistant.Content = []ContentBlock{
		ToolCallContent{ID: "call_1", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "/tmp/clip.mp4"}},
	}

	msgs := []AgentMessage{
		NewUserMessage("看这个"),
		assistant,
		NewToolResultMessage("call_1", "read", []ContentBlock{
			ImageContent{Type: "image", Data: "AAAA", MimeType: "image/png"},
			VideoContent{Type: "video", Data: "BBBB", MimeType: "video/mp4"},
		}, false),
	}

	got := ConvertMessagesToLLM(msgs)
	synthetic := got[len(got)-1]
	if len(synthetic.ContentParts) == 0 || synthetic.ContentParts[0].Text != "Attached image(s) and video(s) from tool result:" {
		t.Fatalf("synthetic user message text = %+v, want mixed label", synthetic.ContentParts)
	}
}
