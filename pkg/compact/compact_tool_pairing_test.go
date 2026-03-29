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

// TestEnsureToolCallPairing_AssistantWithOldToolCalls tests that tool_calls in assistant messages
// that are in oldMessages are filtered out to prevent mismatch
func TestEnsureToolCallPairing_AssistantWithOldToolCalls(t *testing.T) {
	// Scenario: assistant message in recentMessages contains a tool_call whose ID is in oldMessages
	// The tool_call should be filtered out from the assistant message

	oldMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-old-1",
					Type: "toolCall",
					Name: "read",
					Arguments: map[string]any{"path": "/old.txt"},
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
					ID:   "call-old-1",
					Type: "toolCall",
					Name: "read",
					Arguments: map[string]any{"path": "/old.txt"},
				},
			},
		},
		{
			Role:       "toolResult",
			ToolCallID: "call-old-1",
			ToolName:   "read",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "old content"},
			},
		},
	}

	result := ensureToolCallPairing(oldMessages, recentMessages)

	// After fix: the tool_call should be filtered out from the assistant message
	// and the tool_result should be hidden
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}

	// Check assistant message - should have text but not the tool_call
	foundAssistant := false
	for _, msg := range result {
		if msg.Role == "assistant" {
			foundAssistant = true
			toolCalls := msg.ExtractToolCalls()
			if len(toolCalls) != 0 {
				t.Errorf("BUG: assistant message should have tool_calls filtered out, but found %d", len(toolCalls))
			}
			// Check that text content is preserved
			hasText := false
			for _, block := range msg.Content {
				if tc, ok := block.(agentctx.TextContent); ok && tc.Text != "" {
					hasText = true
					break
				}
			}
			if !hasText {
				t.Error("BUG: assistant message should preserve text content")
			}
		}
	}

	if !foundAssistant {
		t.Error("assistant message should be present in result")
	}

	// Check tool_result - should be hidden
	for _, msg := range result {
		if msg.Role == "toolResult" && msg.ToolCallID == "call-old-1" {
			if msg.IsAgentVisible() {
				t.Error("BUG: tool_result should be hidden when its tool_call is in oldMessages")
			}
		}
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

// TestEnsureToolCallPairingWithGrace_ProtectsRecentToolResults tests that grace period protects recent tool results
func TestEnsureToolCallPairingWithGrace_ProtectsRecentToolResults(t *testing.T) {
	config := &Config{
		GracePeriod: 1, // Protect 1 most recent tool result
	}

	compactor := NewCompactor(config, llm.Model{}, "", "", 0)

	// Scenario: tool_call in oldMessages, tool_result in recentMessages
	// With grace period = 1, the most recent tool_result should be protected (not archived)
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

	result := compactor.ensureToolCallPairingWithGrace(oldMessages, recentMessages)

	// The tool_result should be protected by grace period and remain visible
	toolResultFound := false
	for _, msg := range result {
		if msg.Role == "toolResult" && msg.ToolCallID == "call-123" {
			toolResultFound = true
			if !msg.IsAgentVisible() {
				t.Error("tool_result should be visible due to grace period protection")
			}
		}
	}

	if !toolResultFound {
		t.Error("tool_result should still be present in messages")
	}
}

// TestEnsureToolCallPairingWithGrace_OlderResultsArchived tests that older tool results are archived
func TestEnsureToolCallPairingWithGrace_OlderResultsArchived(t *testing.T) {
	config := &Config{
		GracePeriod: 1, // Protect only 1 most recent tool result
	}

	compactor := NewCompactor(config, llm.Model{}, "", "", 0)

	// Scenario: 2 tool_results in recentMessages, both with calls in oldMessages
	// First (older) should be archived, second (most recent) should be protected
	oldMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-old",
					Type: "toolCall",
					Name: "read",
					Arguments: map[string]any{"path": "/old.txt"},
				},
			},
		},
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-newer",
					Type: "toolCall",
					Name: "bash",
					Arguments: map[string]any{"command": "ls"},
				},
			},
		},
	}

	// Most recent message first
	recentMessages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("recent turn"),
		{
			Role:       "toolResult",
			ToolCallID: "call-newer",
			ToolName:   "bash",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "newer result"},
			},
		},
		{
			Role:       "toolResult",
			ToolCallID: "call-old",
			ToolName:   "read",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "older result"},
			},
		},
	}

	result := compactor.ensureToolCallPairingWithGrace(oldMessages, recentMessages)

	// Count visible vs hidden tool_results
	visibleCount := 0
	hiddenCount := 0
	for _, msg := range result {
		if msg.Role == "toolResult" {
			if msg.IsAgentVisible() {
				visibleCount++
			} else {
				hiddenCount++
			}
		}
	}

	// With grace period = 1, only the most recent (call-newer) should be visible
	if visibleCount != 1 {
		t.Errorf("expected 1 visible tool_result, got %d", visibleCount)
	}
	if hiddenCount != 1 {
		t.Errorf("expected 1 hidden tool_result, got %d", hiddenCount)
	}
}

// TestEnsureToolCallPairingWithGrace_GracePeriodZeroFallsBack tests that GracePeriod=0 defaults to 1
func TestEnsureToolCallPairingWithGrace_GracePeriodZeroFallsBack(t *testing.T) {
	config := &Config{
		GracePeriod: 0, // Defaults to 1 internally
	}

	compactor := NewCompactor(config, llm.Model{}, "", "", 0)

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

	result := compactor.ensureToolCallPairingWithGrace(oldMessages, recentMessages)

	// When GracePeriod=0, it defaults to 1 internally, so the tool_result should be protected
	toolResultFound := false
	for _, msg := range result {
		if msg.Role == "toolResult" && msg.ToolCallID == "call-123" {
			toolResultFound = true
			// GracePeriod=0 defaults to 1, so tool_result should be visible
			if !msg.IsAgentVisible() {
				t.Error("tool_result should be visible when GracePeriod defaults to 1")
			}
		}
	}

	if !toolResultFound {
		t.Error("tool_result should still be present in messages")
	}
}

// TestEnsureToolCallPairingWithGrace_LargerGracePeriod protects multiple recent tool results
func TestEnsureToolCallPairingWithGrace_LargerGracePeriod(t *testing.T) {
	config := &Config{
		GracePeriod: 2, // Protect 2 most recent tool results
	}

	compactor := NewCompactor(config, llm.Model{}, "", "", 0)

	oldMessages := []agentctx.AgentMessage{
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-1",
					Type: "toolCall",
					Name: "read",
					Arguments: map[string]any{"path": "/1.txt"},
				},
			},
		},
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-2",
					Type: "toolCall",
					Name: "bash",
					Arguments: map[string]any{"command": "ls"},
				},
			},
		},
		{
			Role: "assistant",
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call-3",
					Type: "toolCall",
					Name: "grep",
					Arguments: map[string]any{"pattern": "test"},
				},
			},
		},
	}

	recentMessages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("recent turn"),
		{
			Role:       "toolResult",
			ToolCallID: "call-3",
			ToolName:   "grep",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "result 3"},
			},
		},
		{
			Role:       "toolResult",
			ToolCallID: "call-2",
			ToolName:   "bash",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "result 2"},
			},
		},
		{
			Role:       "toolResult",
			ToolCallID: "call-1",
			ToolName:   "read",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "result 1"},
			},
		},
	}

	result := compactor.ensureToolCallPairingWithGrace(oldMessages, recentMessages)

	// Count visible vs hidden tool_results
	visibleCount := 0
	hiddenCount := 0
	for _, msg := range result {
		if msg.Role == "toolResult" {
			if msg.IsAgentVisible() {
				visibleCount++
			} else {
				hiddenCount++
			}
		}
	}

	// With grace period = 2, 2 most recent should be visible, 1 older should be hidden
	if visibleCount != 2 {
		t.Errorf("expected 2 visible tool_results with GracePeriod=2, got %d", visibleCount)
	}
	if hiddenCount != 1 {
		t.Errorf("expected 1 hidden tool_result with GracePeriod=2, got %d", hiddenCount)
	}
}