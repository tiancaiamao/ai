package agent

import (
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestIsSuccessfulStopReason(t *testing.T) {
	tests := []struct {
		reason   string
		expected bool
	}{
		{"stop", true},
		{"tool_calls", true},
		{"toolUse", true},
		{"length", true},
		{"", false}, // empty stopReason means incomplete response
		{"error", false},
		{"network_error", false},
		{"timeout", false},
		{"rate_limit", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := isSuccessfulStopReason(tt.reason)
			if got != tt.expected {
				t.Errorf("isSuccessfulStopReason(%q) = %v, want %v", tt.reason, got, tt.expected)
			}
		})
	}
}

func TestSanitizeMessageForNonSuccessStopReason(t *testing.T) {
	t.Run("nil message", func(t *testing.T) {
		sanitizeMessageForNonSuccessStopReason(nil) // should not panic
	})

	t.Run("successful stop returns false", func(t *testing.T) {
		msg := &agentctx.AgentMessage{StopReason: "stop"}
		if sanitizeMessageForNonSuccessStopReason(msg) {
			t.Error("expected false for successful stop")
		}
	})

	t.Run("network_error strips tool calls", func(t *testing.T) {
		msg := &agentctx.AgentMessage{
			StopReason: "network_error",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Text: "before"},
				agentctx.ToolCallContent{ID: "tc1", Name: "bash"},
				agentctx.TextContent{Text: "after"},
			},
		}
		changed := sanitizeMessageForNonSuccessStopReason(msg)
		if !changed {
			t.Fatal("expected true")
		}
		if len(msg.Content) != 3 { // text + error text
			t.Fatalf("expected 3 blocks (2 text + error), got %d", len(msg.Content))
		}
		// Tool call should be removed
		for _, block := range msg.Content {
			if _, ok := block.(agentctx.ToolCallContent); ok {
				t.Error("tool call should have been removed")
			}
		}
		// Error message should contain network error
		last := msg.Content[len(msg.Content)-1].(agentctx.TextContent)
		if !strings.Contains(last.Text, "Network error") {
			t.Errorf("expected Network error message, got %q", last.Text)
		}
	})

	t.Run("rate_limit_error", func(t *testing.T) {
		msg := &agentctx.AgentMessage{StopReason: "rate_limit_error"}
		sanitizeMessageForNonSuccessStopReason(msg)
		last := msg.Content[len(msg.Content)-1].(agentctx.TextContent)
		if !strings.Contains(last.Text, "Rate limit") {
			t.Errorf("expected Rate limit message, got %q", last.Text)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		msg := &agentctx.AgentMessage{StopReason: "timeout"}
		sanitizeMessageForNonSuccessStopReason(msg)
		last := msg.Content[len(msg.Content)-1].(agentctx.TextContent)
		if !strings.Contains(last.Text, "timed out") {
			t.Errorf("expected timeout message, got %q", last.Text)
		}
	})

	t.Run("sensitive content filter", func(t *testing.T) {
		msg := &agentctx.AgentMessage{StopReason: "sensitive"}
		sanitizeMessageForNonSuccessStopReason(msg)
		last := msg.Content[len(msg.Content)-1].(agentctx.TextContent)
		if !strings.Contains(last.Text, "Content filtered") || !strings.Contains(last.Text, "sensitive") {
			t.Errorf("expected content filtered message mentioning stop reason, got %q", last.Text)
		}
	})

	t.Run("error", func(t *testing.T) {
		msg := &agentctx.AgentMessage{StopReason: "error"}
		sanitizeMessageForNonSuccessStopReason(msg)
		last := msg.Content[len(msg.Content)-1].(agentctx.TextContent)
		if !strings.Contains(last.Text, "failed") {
			t.Errorf("expected error message, got %q", last.Text)
		}
	})

	t.Run("unknown stop reason", func(t *testing.T) {
		msg := &agentctx.AgentMessage{StopReason: "something_weird"}
		sanitizeMessageForNonSuccessStopReason(msg)
		last := msg.Content[len(msg.Content)-1].(agentctx.TextContent)
		if !strings.Contains(last.Text, "something_weird") {
			t.Errorf("expected unknown reason in message, got %q", last.Text)
		}
	})

	t.Run("rate_limit alias", func(t *testing.T) {
		msg := &agentctx.AgentMessage{StopReason: "rate_limit"}
		sanitizeMessageForNonSuccessStopReason(msg)
		last := msg.Content[len(msg.Content)-1].(agentctx.TextContent)
		if !strings.Contains(last.Text, "Rate limit") {
			t.Errorf("expected Rate limit message for rate_limit alias, got %q", last.Text)
		}
	})
}

func TestSanitizeMessageForToolLoopGuard(t *testing.T) {
	t.Run("nil message", func(t *testing.T) {
		sanitizeMessageForToolLoopGuard(nil, "test") // should not panic
	})

	t.Run("strips tool calls and adds reason", func(t *testing.T) {
		msg := &agentctx.AgentMessage{
			StopReason: "tool_calls",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Text: "text"},
				agentctx.ToolCallContent{ID: "tc1", Name: "bash"},
				agentctx.ToolCallContent{ID: "tc2", Name: "read"},
			},
		}
		sanitizeMessageForToolLoopGuard(msg, "repeated bash calls")
		// Tool calls removed, replaced by loop guard message
		for _, block := range msg.Content {
			if _, ok := block.(agentctx.ToolCallContent); ok {
				t.Error("tool calls should be removed")
			}
		}
		last := msg.Content[len(msg.Content)-1].(agentctx.TextContent)
		if !strings.Contains(last.Text, "Loop guard") {
			t.Error("expected Loop guard message")
		}
		if !strings.Contains(last.Text, "repeated bash calls") {
			t.Error("expected reason in loop guard message")
		}
		if msg.StopReason != "aborted" {
			t.Errorf("expected stopReason=aborted, got %q", msg.StopReason)
		}
	})
}
