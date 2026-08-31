package compact

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestEnsureToolCallPairing_EmptyShell demonstrates the bug where an assistant message
// has both text content and old tool calls, only tool calls are filtered
func TestEnsureToolCallPairing_EmptyShellWithMixedContent(t *testing.T) {
	// Scenario: assistant has both text content and tool calls in oldMessages
	// Only tool calls should be filtered, text content should remain

	oldMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        "call-1",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/file.txt"},
				},
			},
		},
	}

	recentMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "Here's what I found:"},
				agentctx.ToolCallContent{
					ID:        "call-1",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/file.txt"},
				},
			},
		},
		{
			Role:       "toolResult",
			ToolCallID: "call-1",
			ToolName:   "read",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "file content"},
			},
		},
	}

	result := NewCompactor(&Config{}, llm.Model{}, "", "", 0, "").ensureToolCallPairingWithGrace(oldMessages, recentMessages)

	// Find assistant message
	foundAssistant := false
	for _, msg := range result {
		if msg.Role == "assistant" {
			foundAssistant = true
			// Assistant should still be visible (has text content)
			if !msg.IsAgentVisible() {
				t.Error("Assistant should remain visible when it has text content")
			}
			// Content should have 1 text block (tool call filtered)
			if len(msg.Content) != 1 {
				t.Errorf("Expected 1 content block (text), got %d", len(msg.Content))
			}
			// Verify it's text content
			if _, ok := msg.Content[0].(agentctx.TextContent); !ok {
				t.Error("Expected TextContent, got something else")
			}
		}
	}

	if !foundAssistant {
		t.Error("Assistant message should still exist")
	}
}
