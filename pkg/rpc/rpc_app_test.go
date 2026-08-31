package rpc

import (
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestStripImageDataFromMessage(t *testing.T) {
	bigData := strings.Repeat("A", 5000)

	t.Run("nil message", func(t *testing.T) {
		stripImageDataFromMessage(nil) // should not panic
	})

	t.Run("no images", func(t *testing.T) {
		msg := &agentctx.AgentMessage{
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Text: "hello"},
			},
		}
		stripImageDataFromMessage(msg)
		if tc, ok := msg.Content[0].(agentctx.TextContent); !ok || tc.Text != "hello" {
			t.Errorf("non-image content should be unchanged")
		}
	})

	t.Run("strips image data", func(t *testing.T) {
		msg := &agentctx.AgentMessage{
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Text: "before"},
				agentctx.ImageContent{Type: "image", Data: bigData, MimeType: "image/png"},
				agentctx.TextContent{Text: "after"},
			},
		}
		stripImageDataFromMessage(msg)
		if len(msg.Content) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(msg.Content))
		}
		ic, ok := msg.Content[1].(agentctx.ImageContent)
		if !ok {
			t.Fatalf("expected ImageContent at index 1")
		}
		if ic.Data == bigData {
			t.Errorf("image data should have been stripped")
		}
		if !strings.Contains(ic.Data, "5000 bytes") {
			t.Errorf("replacement should mention original size, got %q", ic.Data)
		}
		if ic.MimeType != "image/png" {
			t.Errorf("MimeType should be preserved")
		}
		// Text blocks unchanged
		if tc, ok := msg.Content[0].(agentctx.TextContent); !ok || tc.Text != "before" {
			t.Errorf("text before image should be unchanged")
		}
	})

	t.Run("empty data not stripped", func(t *testing.T) {
		msg := &agentctx.AgentMessage{
			Content: []agentctx.ContentBlock{
				agentctx.ImageContent{Type: "image", Data: "", MimeType: "image/png"},
			},
		}
		stripImageDataFromMessage(msg)
		ic := msg.Content[0].(agentctx.ImageContent)
		if ic.Data != "" {
			t.Errorf("empty data should not be modified")
		}
	})
}

func TestStripImageDataFromEvent(t *testing.T) {
	bigData := strings.Repeat("B", 3000)

	t.Run("nil event", func(t *testing.T) {
		stripImageDataFromEvent(nil) // should not panic
	})

	t.Run("strips result", func(t *testing.T) {
		ev := &agent.AgentEvent{
			Result: &agentctx.AgentMessage{
				Content: []agentctx.ContentBlock{
					agentctx.ImageContent{Data: bigData},
				},
			},
		}
		stripImageDataFromEvent(ev)
		ic := ev.Result.Content[0].(agentctx.ImageContent)
		if ic.Data == bigData {
			t.Errorf("result image data should be stripped")
		}
	})

	t.Run("strips tool results", func(t *testing.T) {
		ev := &agent.AgentEvent{
			ToolResults: []agentctx.AgentMessage{
				{Content: []agentctx.ContentBlock{agentctx.ImageContent{Data: bigData}}},
			},
		}
		stripImageDataFromEvent(ev)
		ic := ev.ToolResults[0].Content[0].(agentctx.ImageContent)
		if ic.Data == bigData {
			t.Errorf("tool result image data should be stripped")
		}
	})

	t.Run("strips messages", func(t *testing.T) {
		ev := &agent.AgentEvent{
			Messages: []agentctx.AgentMessage{
				{Content: []agentctx.ContentBlock{agentctx.ImageContent{Data: bigData}}},
			},
		}
		stripImageDataFromEvent(ev)
		ic := ev.Messages[0].Content[0].(agentctx.ImageContent)
		if ic.Data == bigData {
			t.Errorf("message image data should be stripped")
		}
	})

	t.Run("strips message field", func(t *testing.T) {
		ev := &agent.AgentEvent{
			Message: &agentctx.AgentMessage{
				Content: []agentctx.ContentBlock{agentctx.ImageContent{Data: bigData}},
			},
		}
		stripImageDataFromEvent(ev)
		ic := ev.Message.Content[0].(agentctx.ImageContent)
		if ic.Data == bigData {
			t.Errorf("message field image data should be stripped")
		}
	})
}
