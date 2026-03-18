package compact

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestEnsureToolCallPairing_Bug demonstrates the bug where tool_call is in oldMessages
// but its tool_result is visible in recentMessages, causing "tool call and result not match"
func TestEnsureToolCallPairing_Bug(t *testing.T) {
	// Scenario: tool_call in oldMessages, tool_result in recentMessages
	// The tool_call will be summarized (not visible), so tool_result should also be hidden
	
	oldMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-123",
					Type: "toolCall",
					Name: "read",
					Arguments: map[string]any{"path": "/test.txt"},
				},
			},
		},
	}

	recentMessages := []agentctx.AgentMessage{
		{
			Role:       "toolResult",
			ToolCallID: "call-123",
			ToolName:   "read",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "file content here"},
			},
		},
		agentctx.NewUserMessage("next user message"),
	}

	result := ensureToolCallPairing(oldMessages, recentMessages)

	// After fix: tool_result should be hidden because its tool_call is in oldMessages (will be summarized)
	// The bug was that tool_result remained visible, causing "tool call and result not match"
	
	// Check that tool_result is hidden
	toolResultFound := false
	for _, msg := range result {
		if msg.Role == "toolResult" && msg.ToolCallID == "call-123" {
			toolResultFound = true
			if msg.IsAgentVisible() {
				t.Error("BUG: tool_result should be hidden when its tool_call is in oldMessages (will be summarized)")
			}
		}
	}
	
	if !toolResultFound {
		t.Error("tool_result should still be present in messages (just hidden)")
	}
}

// TestEnsureToolCallPairing_CorrectPairing tests that correctly paired messages stay visible
func TestEnsureToolCallPairing_CorrectPairing(t *testing.T) {
	// Scenario: both tool_call and tool_result in recentMessages - should stay visible
	recentMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-456",
					Type: "toolCall",
					Name: "write",
					Arguments: map[string]any{"path": "/test.txt", "content": "hello"},
				},
			},
		},
		{
			Role:       "toolResult",
			ToolCallID: "call-456",
			ToolName:   "write",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "ok"},
			},
		},
	}

	// Empty oldMessages - no tool_calls to be summarized
	oldMessages := []agentctx.AgentMessage{}
	
	result := ensureToolCallPairing(oldMessages, recentMessages)

	// Both should remain visible
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
	
	for _, msg := range result {
		if !msg.IsAgentVisible() {
			t.Error("messages with tool_call in recentMessages should stay visible")
		}
	}
}

// TestEnsureToolCallPairing_NoToolCallsInOldMessages tests edge case
func TestEnsureToolCallPairing_NoToolCallsInOldMessages(t *testing.T) {
	// Scenario: oldMessages has no tool_calls, recentMessages has tool_result
	oldMessages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("some old message"),
	}

	recentMessages := []agentctx.AgentMessage{
		{
			Role:       "toolResult",
			ToolCallID: "call-789",
			ToolName:   "read",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "result"},
			},
		},
	}

	result := ensureToolCallPairing(oldMessages, recentMessages)

	// Tool_result should stay visible (its call might be in an even older message, 
	// or it's orphaned - either way it's not being summarized)
	if len(result) != 1 {
		t.Errorf("expected 1 message, got %d", len(result))
	}
	
	if !result[0].IsAgentVisible() {
		t.Error("tool_result should stay visible when oldMessages has no tool_calls")
	}
}

// TestEnsureToolCallPairing_OrphanToolCall tests the fix: tool_call in recentMessages
// but its tool_result is in oldMessages (will be summarized)
func TestEnsureToolCallPairing_OrphanToolCall(t *testing.T) {
	// Scenario: tool_result in oldMessages (will be summarized), tool_call in recentMessages
	// This is the reverse of the original bug - the tool_call should be hidden
	
	oldMessages := []agentctx.AgentMessage{
		{
			Role:       "toolResult",
			ToolCallID: "call-orphan",
			ToolName:   "bash",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "command output"},
			},
		},
	}

	recentMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-orphan",
					Type: "toolCall",
					Name: "bash",
					Arguments: map[string]any{"command": "ls"},
				},
			},
		},
		agentctx.NewUserMessage("next user message"),
	}

	result := ensureToolCallPairing(oldMessages, recentMessages)

	// After fix: tool_call should be hidden because its tool_result is in oldMessages (will be summarized)
	// This prevents "tool call result does not match" error
	
	// Check that tool_call is hidden
	assistantMsgFound := false
	for _, msg := range result {
		if msg.Role == "assistant" {
			assistantMsgFound = true
			if msg.IsAgentVisible() {
				t.Error("BUG: tool_call should be hidden when its tool_result is in oldMessages (will be summarized)")
			}
		}
	}
	
	if !assistantMsgFound {
		t.Error("assistant message should still be present in messages (just hidden)")
	}
	
	// User message should still be visible
	userMsgVisible := false
	for _, msg := range result {
		if msg.Role == "user" && msg.IsAgentVisible() {
			userMsgVisible = true
		}
	}
	
	if !userMsgVisible {
		t.Error("user message should stay visible")
	}
}

// TestFullCompactPreservesPairing tests the full compaction flow preserves tool_call/tool_result pairing
func TestFullCompactPreservesPairing(t *testing.T) {
	config := &Config{
		MaxMessages:        10,
		MaxTokens:          1000,
		KeepRecent:         2,
		KeepRecentTokens:   100,
		AutoCompact:        true,
	}

	compactor := NewCompactor(config, llm.Model{}, "", "", 0)

	// Create messages: 1 user + tool_call + tool_result + 1 user
	// The tool_call and tool_result should end up in the same visibility state after compaction
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("first message"),
		// tool_call
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-full-1",
					Type: "toolCall",
					Name: "bash",
					Arguments: map[string]any{"command": "ls"},
				},
			},
		},
		// tool_result
		{
			Role:       "toolResult",
			ToolCallID: "call-full-1",
			ToolName:   "bash",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "file1\nfile2\nfile3"},
			},
		},
		agentctx.NewUserMessage("last message"),
	}

	result, err := compactor.Compact(messages, "")
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	// After compaction, check that all tool_results have their corresponding tool_calls visible
	// Collect all visible tool_call IDs
	visibleToolCalls := make(map[string]bool)
	for _, msg := range result.Messages {
		if msg.Role == "assistant" && msg.IsAgentVisible() {
			for _, tc := range msg.ExtractToolCalls() {
				visibleToolCalls[tc.ID] = true
			}
		}
	}

	// Check all visible tool_results have their tool_calls visible
	for _, msg := range result.Messages {
		if msg.Role == "toolResult" && msg.IsAgentVisible() {
			if !visibleToolCalls[msg.ToolCallID] {
				t.Errorf("BUG: tool_result with id %s is visible but its tool_call is not visible", msg.ToolCallID)
			}
		}
	}
}