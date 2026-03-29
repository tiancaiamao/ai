package tools

import (
	"context"
	"sync"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestLLMContextDecisionFiltersAlreadyTruncated(t *testing.T) {
	// Create test messages
	messages := []agentctx.AgentMessage{
		func() agentctx.AgentMessage {
			msg := agentctx.NewAssistantMessage()
			msg.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call_cm_test",
					Type: "toolCall",
					Name: "context_management",
					Arguments: map[string]any{
						"decision":     "truncate",
						"reasoning":    "Test filtering of already truncated outputs",
						"truncate_ids": "call_1,call_2,call_3,call_4",
					},
				},
			}
			return msg
		}(),

		// Regular tool output (not truncated)
		agentctx.NewToolResultMessage("call_1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "large content 1"},
		}, false),

		// Already truncated tool output
		agentctx.NewToolResultMessage("call_2", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: `<agent:tool name="read" chars="1000" truncated="true" />`},
		}, false),

		// Another regular tool output
		agentctx.NewToolResultMessage("call_3", "grep", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "large content 3"},
		}, false),

		// Another already truncated tool output
		agentctx.NewToolResultMessage("call_4", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: `<agent:tool name="bash" chars="2000" truncated="true" />`},
		}, false),
	}

	// Debug: check message content
	for i, msg := range messages {
		t.Logf("Message %d: Role=%s, ToolCallID=%s, Content=%s", i, msg.Role, msg.ToolCallID, msg.ExtractText())
	}

	// Test IsTruncatedAgentToolTag function
	for _, msg := range messages {
		if msg.Role != "toolResult" {
			continue
		}
		text := msg.ExtractText()
		isTruncated := agentctx.IsTruncatedAgentToolTag(text)
		t.Logf("ToolCallID=%s, IsTruncated=%v, Text=%s", msg.ToolCallID, isTruncated, text)
	}

	tool := NewContextManagementTool(nil)

	// Create agent context
	agentCtx := &agentctx.AgentContext{
		Messages:         messages,
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
		LLMContext:       nil,
	}

	// Wrap with agent context - this is what AgentLoop does when executing tools
	ctx := context.Background()
	ctx = agentctx.WithToolExecutionAgentContext(ctx, agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_cm_test")

	params := map[string]any{
		"decision":     "truncate",
		"reasoning":    "Test filtering of already truncated outputs",
		"truncate_ids": "call_1,call_2,call_3,call_4", // Include already truncated IDs
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Check result
	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	// Extract text from content block
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}
	t.Logf("Result: %s", resultText)

	// Verify: call_1 and call_3 should be truncated, call_2 and call_4 should remain truncated
	for _, msg := range agentCtx.Messages {
		if msg.Role != "toolResult" {
			continue
		}

		content := msg.ExtractText()
		isTruncated := agentctx.IsTruncatedAgentToolTag(content)

		switch msg.ToolCallID {
		case "call_1", "call_3":
			// Should be truncated now
			if !isTruncated {
				t.Errorf("Message %s should be truncated but is not: %s", msg.ToolCallID, content)
			}
		case "call_2", "call_4":
			// Were already truncated, should remain truncated
			if !isTruncated {
				t.Errorf("Message %s should still be truncated but is not: %s", msg.ToolCallID, content)
			}
		}
	}
}

func TestContextManagementTruncateCleansOnlyCurrentToolCallArguments(t *testing.T) {
	olderAssistant := agentctx.NewAssistantMessage()
	olderAssistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_cm_old",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":     "truncate",
				"reasoning":    "old call",
				"truncate_ids": "old_1",
			},
		},
	}

	currentAssistant := agentctx.NewAssistantMessage()
	currentAssistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_cm_new",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":     "truncate",
				"reasoning":    "current call",
				"truncate_ids": "call_1,call_2",
			},
		},
	}

	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{
			olderAssistant,
			currentAssistant,
			agentctx.NewToolResultMessage("call_1", "read", []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "content 1"},
			}, false),
			agentctx.NewToolResultMessage("call_2", "grep", []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "content 2"},
			}, false),
		},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	tool := NewContextManagementTool(nil)

	execCtx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	execCtx = agentctx.WithToolExecutionCallID(execCtx, "call_cm_new")

	_, err := tool.Execute(execCtx, map[string]any{
		"decision":     "truncate",
		"reasoning":    "clean only current tool call",
		"truncate_ids": "call_1,call_2",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	olderCalls := agentCtx.Messages[0].ExtractToolCalls()
	if len(olderCalls) != 1 {
		t.Fatalf("expected 1 tool call in older assistant message, got %d", len(olderCalls))
	}
	if _, exists := olderCalls[0].Arguments["truncate_ids"]; !exists {
		t.Fatal("expected older context_management call to keep truncate_ids")
	}

	currentCalls := agentCtx.Messages[1].ExtractToolCalls()
	if len(currentCalls) != 1 {
		t.Fatalf("expected 1 tool call in current assistant message, got %d", len(currentCalls))
	}
	if _, exists := currentCalls[0].Arguments["truncate_ids"]; exists {
		t.Fatal("expected current context_management call to remove truncate_ids")
	}
	if got := currentCalls[0].Arguments["decision"]; got != "truncate" {
		t.Fatalf("expected decision to be preserved, got %#v", got)
	}
	if got := currentCalls[0].Arguments["reasoning"]; got != "current call" {
		t.Fatalf("expected reasoning to be preserved, got %#v", got)
	}
}

func TestContextManagementConcurrentTruncateCallsKeepBothCleanups(t *testing.T) {
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_cm_1",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":     "truncate",
				"reasoning":    "cleanup first",
				"truncate_ids": "call_1",
			},
		},
		agentctx.ToolCallContent{
			ID:   "call_cm_2",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":     "truncate",
				"reasoning":    "cleanup second",
				"truncate_ids": "call_2",
			},
		},
	}

	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{
			assistant,
			agentctx.NewToolResultMessage("call_1", "read", []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "content 1"},
			}, false),
			agentctx.NewToolResultMessage("call_2", "grep", []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "content 2"},
			}, false),
		},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}
	agentCtx.ContextMgmtState.SetCurrentTurn(1)

	tool := NewContextManagementTool(nil)

	runCall := func(callID, truncateIDs string) error {
		execCtx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
		execCtx = agentctx.WithToolExecutionCallID(execCtx, callID)
		_, err := tool.Execute(execCtx, map[string]any{
			"decision":     "truncate",
			"reasoning":    "concurrent cleanup",
			"truncate_ids": truncateIDs,
		})
		return err
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- runCall("call_cm_1", "call_1")
	}()
	go func() {
		defer wg.Done()
		errs <- runCall("call_cm_2", "call_2")
	}()
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}
	}

	toolCalls := agentCtx.Messages[0].ExtractToolCalls()
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 context_management tool calls, got %d", len(toolCalls))
	}
	for _, tc := range toolCalls {
		if _, exists := tc.Arguments["truncate_ids"]; exists {
			t.Fatalf("expected truncate_ids removed for %s", tc.ID)
		}
	}

	for _, msg := range agentCtx.Messages[1:] {
		if msg.Role != "toolResult" {
			continue
		}
		if !agentctx.IsTruncatedAgentToolTag(msg.ExtractText()) {
			t.Fatalf("expected tool result %s to be truncated", msg.ToolCallID)
		}
	}
}

// TestSkipDeniedWhenRatioZeroOrNegative tests that skip is denied when proactive ratio <= 0
func TestSkipDeniedWhenRatioZeroOrNegative(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}
	agentCtx.ContextMgmtState.SetCurrentTurn(10)

	// Simulate poor proactive behavior: proactive=0, remind=5, ratio=-5
	state := agentCtx.ContextMgmtState
	state.RecordDecision(1, "truncate", true)  // reminded
	state.RecordDecision(2, "truncate", true)  // reminded
	state.RecordDecision(3, "truncate", true)  // reminded
	state.RecordDecision(4, "truncate", true)  // reminded
	state.RecordDecision(5, "truncate", true)  // reminded

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip")

	resultBlocks, err := tool.Execute(ctx, map[string]any{
		"decision":  "skip",
		"reasoning": "Want to skip 30 turns",
		"skip_turns": 30.0,
	})

	if err != nil {
		t.Fatalf("Execute should not return error, got: %v", err)
	}

	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Verify error message contains "skip request denied"
	if !contains(resultText, "skip request denied") {
		t.Errorf("Expected result to contain 'skip request denied', got: %s", resultText)
	}

	// Verify ratio is -4 (1 proactive - 5 reminded = -4, MarkDecisionMade incremented proactive)
	if !contains(resultText, "ratio=-4") {
		t.Errorf("Expected result to mention ratio=-4, got: %s", resultText)
	}

	// Verify SkipUntilTurn is NOT set (should still be 0)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 0 {
		t.Errorf("Expected SkipUntilTurn to be 0 (not set), got: %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}

	// Verify decision tracking: MarkDecisionMade() increments ProactiveDecisions
	// When skip is denied, we accept this because it still marks a decision was made
	snapshot := agentCtx.ContextMgmtState.Snapshot()
	if snapshot.ProactiveDecisions != 1 { // MarkDecisionMade() increments this
		t.Errorf("Expected ProactiveDecisions=1 (MarkDecisionMade increments it), got: %d", snapshot.ProactiveDecisions)
	}
	if snapshot.ReminderNeeded != 5 {
		t.Errorf("Expected ReminderNeeded=5, got: %d", snapshot.ReminderNeeded)
	}
}

// TestSkipReducedWhenOverLimit tests that skip_turns is reduced when requested > maxSkip
func TestSkipReducedWhenOverLimit(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}
	agentCtx.ContextMgmtState.SetCurrentTurn(10)

	// Simulate proactive behavior: proactive=10, remind=0, ratio=10, maxSkip=10
	state := agentCtx.ContextMgmtState
	state.RecordDecision(1, "truncate", false) // proactive
	state.RecordDecision(2, "truncate", false) // proactive
	state.RecordDecision(3, "truncate", false) // proactive
	state.RecordDecision(4, "truncate", false) // proactive
	state.RecordDecision(5, "truncate", false) // proactive
	state.RecordDecision(6, "compact", false)  // proactive
	state.RecordDecision(7, "compact", false)  // proactive
	state.RecordDecision(8, "compact", false)  // proactive
	state.RecordDecision(9, "compact", false)  // proactive
	state.RecordDecision(10, "compact", false) // proactive

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip")

	resultBlocks, err := tool.Execute(ctx, map[string]any{
		"decision":  "skip",
		"reasoning": "Want to skip 30 turns",
		"skip_turns": 30.0,
	})

	if err != nil {
		t.Fatalf("Execute should not return error, got: %v", err)
	}

	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Verify message contains "reduced from 30 to 11"
	if !contains(resultText, "reduced from 30 to 11") {
		t.Errorf("Expected result to contain 'reduced from 30 to 11', got: %s", resultText)
	}

	// Verify SkipUntilTurn is set to 21 (turn 10 + maxSkip 11)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 21 {
		t.Errorf("Expected SkipUntilTurn=21, got: %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestSkipCappedAt30 tests that maxSkip is capped at 30 even with higher ratio
func TestSkipCappedAt30(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}
	agentCtx.ContextMgmtState.SetCurrentTurn(5)

	// Simulate very proactive behavior: proactive=40, remind=0, ratio=40, maxSkip=30 (capped)
	state := agentCtx.ContextMgmtState
	for i := 0; i < 40; i++ {
		state.RecordDecision(i+1, "truncate", false) // proactive
	}

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip")

	resultBlocks, err := tool.Execute(ctx, map[string]any{
		"decision":  "skip",
		"reasoning": "Want to skip 30 turns",
		"skip_turns": 30.0,
	})

	if err != nil {
		t.Fatalf("Execute should not return error, got: %v", err)
	}

	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// With ratio=40 and request=30, should succeed (not reduced)
	if contains(resultText, "reduced") {
		t.Errorf("Expected skip to succeed (not reduced), got: %s", resultText)
	}

	// Verify SkipUntilTurn is set to 35 (turn 5 + 30)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 35 {
		t.Errorf("Expected SkipUntilTurn=35, got: %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestSkipSuccessWithinLimit tests normal success when skipTurns <= maxSkip
func TestSkipSuccessWithinLimit(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}
	agentCtx.ContextMgmtState.SetCurrentTurn(10)

	// Simulate proactive behavior: proactive=10, remind=0, ratio=10, maxSkip=10
	state := agentCtx.ContextMgmtState
	state.RecordDecision(1, "truncate", false) // proactive
	state.RecordDecision(2, "truncate", false) // proactive
	state.RecordDecision(3, "truncate", false) // proactive
	state.RecordDecision(4, "truncate", false) // proactive
	state.RecordDecision(5, "truncate", false) // proactive
	state.RecordDecision(6, "compact", false)  // proactive
	state.RecordDecision(7, "compact", false)  // proactive
	state.RecordDecision(8, "compact", false)  // proactive
	state.RecordDecision(9, "compact", false)  // proactive
	state.RecordDecision(10, "compact", false) // proactive

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip")

	resultBlocks, err := tool.Execute(ctx, map[string]any{
		"decision":  "skip",
		"reasoning": "Want to skip 5 turns",
		"skip_turns": 5.0,
	})

	if err != nil {
		t.Fatalf("Execute should not return error, got: %v", err)
	}

	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Verify normal success message
	if !contains(resultText, "Skipping reminders for 5 turns") {
		t.Errorf("Expected result to contain 'Skipping reminders for 5 turns', got: %s", resultText)
	}

	// Verify no "reduced" message
	if contains(resultText, "reduced") {
		t.Errorf("Expected normal success (not reduced), got: %s", resultText)
	}

	// Verify SkipUntilTurn is set to 15 (turn 10 + 5)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 15 {
		t.Errorf("Expected SkipUntilTurn=15, got: %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestSkipBoundaryCases tests edge cases: ratio=0, ratio=1, etc.
func TestSkipBoundaryCases(t *testing.T) {
	testCases := []struct {
		name              string
		proactive         int
		reminded          int
		requestSkipTurns  int
		expectDeny        bool
		expectReduce      bool
		expectedMaxSkip    int
	}{
		{
			name:             "ratio=0 should deny",
			proactive:        4,
			reminded:         5,
			requestSkipTurns: 10,
			expectDeny:       true,
			expectReduce:     false,
			expectedMaxSkip:   0,
		},
		{
			name:             "ratio=1 should allow maxSkip=1",
			proactive:        5,
			reminded:         5,
			requestSkipTurns: 10,
			expectDeny:       false,
			expectReduce:     true,
			expectedMaxSkip:   1,
		},
		{
			name:             "ratio=1, request=1 should succeed",
			proactive:        5,
			reminded:         5,
			requestSkipTurns: 1,
			expectDeny:       false,
			expectReduce:     false,
			expectedMaxSkip:   1,
		},
		{
			name:             "ratio=2 should allow maxSkip=2",
			proactive:        6,
			reminded:         5,
			requestSkipTurns: 10,
			expectDeny:       false,
			expectReduce:     true,
			expectedMaxSkip:   2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			agentCtx := &agentctx.AgentContext{
				Messages:         []agentctx.AgentMessage{},
				ContextMgmtState: agentctx.DefaultContextMgmtState(),
			}
			agentCtx.ContextMgmtState.SetCurrentTurn(10)

			// Set up proactive/reminded counts
			state := agentCtx.ContextMgmtState
			for i := 0; i < tc.proactive; i++ {
				state.RecordDecision(i+1, "truncate", false)
			}
			for i := 0; i < tc.reminded; i++ {
				state.RecordDecision(tc.proactive+i+1, "truncate", true)
			}

			tool := NewContextManagementTool(nil)
			ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
			ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip")

			resultBlocks, err := tool.Execute(ctx, map[string]any{
				"decision":  "skip",
				"reasoning": tc.name,
				"skip_turns": float64(tc.requestSkipTurns),
			})

			if err != nil {
				t.Fatalf("Execute should not return error, got: %v", err)
			}

			if len(resultBlocks) == 0 {
				t.Fatal("Expected result blocks, got none")
			}

			resultText := ""
			if block, ok := resultBlocks[0].(agentctx.TextContent); ok {
				resultText = block.Text
			}

			if tc.expectDeny {
				if !contains(resultText, "skip request denied") {
					t.Errorf("Expected 'skip request denied', got: %s", resultText)
				}
				// Verify SkipUntilTurn NOT set
				if agentCtx.ContextMgmtState.SkipUntilTurn != 0 {
					t.Errorf("Expected SkipUntilTurn=0 (not set), got: %d", agentCtx.ContextMgmtState.SkipUntilTurn)
				}
			} else if tc.expectReduce {
				if !contains(resultText, "reduced") {
					t.Errorf("Expected 'reduced', got: %s", resultText)
				}
				// Verify SkipUntilTurn set correctly
				expectedSkipUntil := 10 + tc.expectedMaxSkip
				if agentCtx.ContextMgmtState.SkipUntilTurn != expectedSkipUntil {
					t.Errorf("Expected SkipUntilTurn=%d, got: %d", expectedSkipUntil, agentCtx.ContextMgmtState.SkipUntilTurn)
				}
			} else {
				if contains(resultText, "reduced") || contains(resultText, "denied") {
					t.Errorf("Expected normal success, got: %s", resultText)
				}
				// Verify SkipUntilTurn set correctly
				expectedSkipUntil := 10 + tc.requestSkipTurns
				if agentCtx.ContextMgmtState.SkipUntilTurn != expectedSkipUntil {
					t.Errorf("Expected SkipUntilTurn=%d, got: %d", expectedSkipUntil, agentCtx.ContextMgmtState.SkipUntilTurn)
				}
			}
		})
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
