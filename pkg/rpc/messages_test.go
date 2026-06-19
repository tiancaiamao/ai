package rpc

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestFormatMessagesForDisplay(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		msgs := []agentctx.AgentMessage{
			{Role: "user"},
			{Role: "assistant"},
			{Role: "user"},
		}
		// Can't easily set Content on AgentMessage from outside the package,
		// but we can still test count/index logic.
		result := FormatMessagesForDisplay(msgs, 2, 100)
		if result.Total != 3 {
			t.Errorf("Total = %d, want 3", result.Total)
		}
		if result.Showing != 2 {
			t.Errorf("Showing = %d, want 2", result.Showing)
		}
		if len(result.Messages) != 2 {
			t.Fatalf("len(Messages) = %d, want 2", len(result.Messages))
		}
		if result.Messages[0].Index != 1 {
			t.Errorf("Messages[0].Index = %d, want 1", result.Messages[0].Index)
		}
		if result.Messages[0].Role != "assistant" {
			t.Errorf("Messages[0].Role = %q, want assistant", result.Messages[0].Role)
		}
	})

	t.Run("count exceeds total", func(t *testing.T) {
		msgs := []agentctx.AgentMessage{
			{Role: "user"},
		}
		result := FormatMessagesForDisplay(msgs, 10, 100)
		if result.Showing != 1 {
			t.Errorf("Showing = %d, want 1", result.Showing)
		}
		if result.Messages[0].Index != 0 {
			t.Errorf("Index = %d, want 0", result.Messages[0].Index)
		}
	})

	t.Run("empty messages", func(t *testing.T) {
		result := FormatMessagesForDisplay(nil, 5, 100)
		if result.Total != 0 {
			t.Errorf("Total = %d, want 0", result.Total)
		}
		if len(result.Messages) != 0 {
			t.Errorf("len(Messages) = %d, want 0", len(result.Messages))
		}
	})
}
