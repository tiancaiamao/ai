package compact

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"
)

// TestEnsureToolCallPairing_EmptyShell demonstrates the bug where an assistant message
// becomes an empty shell when all its tool calls are filtered out and it has no text content
func TestEnsureToolCallPairing_EmptyShell(t *testing.T) {
	// Scenario: assistant in recentMessages has tool_calls whose IDs are in oldMessages
	// All tool_calls get filtered out, leaving empty content (empty shell bug)
	// Expected: entire message should be hidden instead of leaving empty shell

	oldMessages := []agentctx.AgentMessage{
		{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "old user message"},
			},
		},
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

	// Assistant message in recentMessages has same tool_calls as oldMessages
	// They will be filtered out, leaving empty content
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
				agentctx.ToolCallContent{
					ID:        "call-2",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/file2.txt"},
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
		{
			Role:       "toolResult",
			ToolCallID: "call-2",
			ToolName:   "read",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "file2 content"},
			},
		},
		agentctx.NewUserMessage("next user message"),
	}

	result := ensureToolCallPairing(oldMessages, recentMessages)

	// Find the assistant message that should be hidden
	// It should be the only assistant message with only tool calls (no text)
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

	// Verify tool_results are also hidden
	hiddenToolResults := 0
	for _, msg := range result {
		if msg.Role == "toolResult" && (msg.ToolCallID == "call-1" || msg.ToolCallID == "call-2") {
			if !msg.IsAgentVisible() {
				hiddenToolResults++
			}
		}
	}

	if hiddenToolResults != 2 {
		t.Errorf("Expected 2 hidden tool_results, got %d", hiddenToolResults)
	}
}

// TestEnsureToolCallPairing_EmptyShellWithMixedContent tests that when assistant
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

	result := ensureToolCallPairing(oldMessages, recentMessages)

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
