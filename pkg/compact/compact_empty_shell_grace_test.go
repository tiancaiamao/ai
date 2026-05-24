package compact

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestEnsureToolCallPairingWithGrace_EmptyShell tests the empty shell bug fix
// in the grace period variant of the function
func TestEnsureToolCallPairingWithGrace_EmptyShell(t *testing.T) {
	config := &Config{
		GracePeriod: 1, // Protect 1 most recent tool result
	}

	compactor := NewCompactor(config, llm.Model{}, "", "", 0)

	oldMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        "call-1",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/file1.txt"},
				},
				agentctx.ToolCallContent{
					ID:        "call-2",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/file2.txt"},
				},
			},
		},
	}

	// Assistant message has tool calls that are in oldMessages
	recentMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				// Only tool calls, no text content - becomes empty shell
				agentctx.ToolCallContent{
					ID:        "call-1",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/file1.txt"},
				},
			},
		},
		{
			Role:       "toolResult",
			ToolCallID: "call-1",
			ToolName:   "read",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "file1 content"},
			},
		},
		agentctx.NewUserMessage("next user message"),
	}

	result := compactor.ensureToolCallPairingWithGrace(oldMessages, recentMessages)

	// Find the assistant message
	foundHiddenAssistant := false
	for _, msg := range result {
		if msg.Role == "assistant" {
			// Check if this message has any text content
			hasText := false
			for _, block := range msg.Content {
				if _, ok := block.(agentctx.TextContent); ok {
					hasText = true
					break
				}
			}
			if !hasText {
				// This is the assistant with only tool calls - should be hidden
				if msg.IsAgentVisible() {
					t.Error("BUG: Assistant message should be hidden when all tool calls are filtered and no text content remains")
				}
				foundHiddenAssistant = true
			}
		}
	}

	if !foundHiddenAssistant {
		t.Error("Assistant message should still exist (hidden, not deleted)")
	}
}

// TestEnsureToolCallPairingWithGrace_EmptyShellWithGraceProtected tests that
// tool results within grace period are protected even when their tool calls
// are in oldMessages
func TestEnsureToolCallPairingWithGrace_EmptyShellWithGraceProtected(t *testing.T) {
	config := &Config{
		GracePeriod: 1, // Protect 1 most recent tool result
	}

	compactor := NewCompactor(config, llm.Model{}, "", "", 0)

	oldMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        "call-1",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/file1.txt"},
				},
			},
		},
	}

	// Tool result is within grace period (most recent), so it should be protected
	recentMessages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("recent turn"),
		{
			Role:       "toolResult",
			ToolCallID: "call-1",
			ToolName:   "read",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "file1 content"},
			},
		},
	}

	result := compactor.ensureToolCallPairingWithGrace(oldMessages, recentMessages)

	// Tool result should be visible due to grace period
	toolResultVisible := false
	for _, msg := range result {
		if msg.Role == "toolResult" && msg.ToolCallID == "call-1" {
			if msg.IsAgentVisible() {
				toolResultVisible = true
			}
		}
	}

	if !toolResultVisible {
		t.Error("Tool result within grace period should be visible")
	}
}
