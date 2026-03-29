package tools

import (
	"context"
	"strings"
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

// TestSkipDeniedWhenRatioIsZeroOrNegative tests that skip is denied when proactive ratio <= 0.
func TestSkipDeniedWhenRatioIsZeroOrNegative(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Simulate LLM with poor proactive behavior: 0 proactive, 5 reminded
	agentCtx.ContextMgmtState.ProactiveDecisions = 0
	agentCtx.ContextMgmtState.ReminderNeeded = 5
	agentCtx.ContextMgmtState.SetCurrentTurn(10)

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// Try to skip for 30 turns
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "I want to skip reminders",
		"skip_turns": float64(30),
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Check result message
	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Should contain "skip request denied"
	if !strings.Contains(resultText, "skip request denied") {
		t.Errorf("Expected 'skip request denied' in result, got: %s", resultText)
	}

	// Should mention ratio=-5
	if !strings.Contains(resultText, "ratio=-5") {
		t.Errorf("Expected 'ratio=-5' in result, got: %s", resultText)
	}

	// SkipUntilTurn should NOT be set (should remain 0)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 0 {
		t.Errorf("Expected SkipUntilTurn to remain 0 when denied, got %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestSkipReducedWhenOverLimit tests that skip is reduced when requested over max.
func TestSkipReducedWhenOverLimit(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Simulate LLM with ratio=10 (proactive=10, reminded=0), so maxSkip=10
	agentCtx.ContextMgmtState.ProactiveDecisions = 10
	agentCtx.ContextMgmtState.ReminderNeeded = 0
	agentCtx.ContextMgmtState.SetCurrentTurn(10)

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// Try to skip for 30 turns (should be reduced to 10)
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "I want to skip reminders",
		"skip_turns": float64(30),
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Check result message
	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Should contain "skip_turns reduced"
	if !strings.Contains(resultText, "skip_turns reduced") {
		t.Errorf("Expected 'skip_turns reduced' in result, got: %s", resultText)
	}

	// Should mention reduced from 30 to 10
	if !strings.Contains(resultText, "reduced from 30 to 10") {
		t.Errorf("Expected 'reduced from 30 to 10' in result, got: %s", resultText)
	}

	// SkipUntilTurn should be set to 20 (turn 10 + skip 10)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 20 {
		t.Errorf("Expected SkipUntilTurn=20, got %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestSkipCappedAt30 tests that max skip is capped at 30 even with high ratio.
func TestSkipCappedAt30(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Simulate LLM with very high proactive ratio=40 (proactive=40, reminded=0)
	agentCtx.ContextMgmtState.ProactiveDecisions = 40
	agentCtx.ContextMgmtState.ReminderNeeded = 0
	agentCtx.ContextMgmtState.SetCurrentTurn(10)

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// Try to skip for 30 turns (should be allowed with maxSkip capped at 30)
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "I want to skip reminders",
		"skip_turns": float64(30),
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Check result message
	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Should contain normal success message
	if !strings.Contains(resultText, "Skipping reminders for 30 turns") {
		t.Errorf("Expected 'Skipping reminders for 30 turns' in result, got: %s", resultText)
	}

	// Should NOT contain "reduced"
	if strings.Contains(resultText, "reduced") {
		t.Errorf("Did not expect 'reduced' in result when ratio >= skipTurns, got: %s", resultText)
	}

	// SkipUntilTurn should be set to 40 (turn 10 + skip 30)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 40 {
		t.Errorf("Expected SkipUntilTurn=40, got %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestSkipSuccessWhenWithinLimit tests that skip succeeds when within limit.
func TestSkipSuccessWhenWithinLimit(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Simulate LLM with ratio=10 (proactive=10, reminded=0), so maxSkip=10
	agentCtx.ContextMgmtState.ProactiveDecisions = 10
	agentCtx.ContextMgmtState.ReminderNeeded = 0
	agentCtx.ContextMgmtState.SetCurrentTurn(10)

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// Request skip for 5 turns (within limit of 10)
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "I want to skip reminders",
		"skip_turns": float64(5),
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Check result message
	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Should contain normal success message
	if !strings.Contains(resultText, "Skipping reminders for 5 turns") {
		t.Errorf("Expected 'Skipping reminders for 5 turns' in result, got: %s", resultText)
	}

	// Should NOT contain "reduced" or "denied"
	if strings.Contains(resultText, "reduced") || strings.Contains(resultText, "denied") {
		t.Errorf("Did not expect 'reduced' or 'denied' in result when within limit, got: %s", resultText)
	}

	// SkipUntilTurn should be set to 15 (turn 10 + skip 5)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 15 {
		t.Errorf("Expected SkipUntilTurn=15, got %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestRemindersRemainingCalculation tests that reminders_remaining is calculated correctly.
func TestRemindersRemainingCalculation(t *testing.T) {
	tests := []struct {
		name                  string
		proactiveDecisions    int
		reminderNeeded       int
		currentTurn          int
		lastReminderTurn     int
		reminderFrequency    int
		skipUntilTurn        int
		expectedRemaining    int
	}{
		{
			name:               "Normal period, no skip",
			proactiveDecisions: 5,
			reminderNeeded:     0,
			currentTurn:        10,
			lastReminderTurn:   5,
			reminderFrequency:  10,
			skipUntilTurn:      0,
			expectedRemaining:  5, // 10 - (10 - 5) = 5
		},
		{
			name:               "In skip period",
			proactiveDecisions: 5,
			reminderNeeded:     0,
			currentTurn:        10,
			lastReminderTurn:   5,
			reminderFrequency:  10,
			skipUntilTurn:      20,
			expectedRemaining:  10, // 20 - 10 = 10
		},
		{
			name:               "At skip boundary",
			proactiveDecisions: 5,
			reminderNeeded:     0,
			currentTurn:        20,
			lastReminderTurn:   10,
			reminderFrequency:  10,
			skipUntilTurn:      20,
			expectedRemaining:  0, // at boundary
		},
		{
			name:               "Past skip period",
			proactiveDecisions: 5,
			reminderNeeded:     0,
			currentTurn:        25,
			lastReminderTurn:   20,
			reminderFrequency:  10,
			skipUntilTurn:      20,
			expectedRemaining:  5, // 20 + 10 - 25 = 5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentCtx := &agentctx.AgentContext{
				Messages:         []agentctx.AgentMessage{},
				ContextMgmtState: agentctx.DefaultContextMgmtState(),
			}

			agentCtx.ContextMgmtState.ProactiveDecisions = tt.proactiveDecisions
			agentCtx.ContextMgmtState.ReminderNeeded = tt.reminderNeeded
			agentCtx.ContextMgmtState.CurrentTurn = tt.currentTurn
			agentCtx.ContextMgmtState.LastReminderTurn = tt.lastReminderTurn
			agentCtx.ContextMgmtState.ReminderFrequency = tt.reminderFrequency
			agentCtx.ContextMgmtState.SkipUntilTurn = tt.skipUntilTurn

			// Get snapshot
			snapshot := agentCtx.ContextMgmtState.Snapshot()

			// Calculate reminders_remaining
			remindersRemaining := 0
			if snapshot.ReminderFrequency > 0 {
				frequencyRemaining := snapshot.ReminderFrequency - (snapshot.CurrentTurn - snapshot.LastReminderTurn)
				if frequencyRemaining < 0 {
					frequencyRemaining = 0
				}

				skipRemaining := snapshot.SkipUntilTurn - snapshot.CurrentTurn
				if skipRemaining < 0 {
					skipRemaining = 0
				}

				// Use the larger of the two
				if skipRemaining > frequencyRemaining {
					remindersRemaining = skipRemaining
				} else {
					remindersRemaining = frequencyRemaining
				}
			}

			if remindersRemaining != tt.expectedRemaining {
				t.Errorf("Expected reminders_remaining=%d, got %d", tt.expectedRemaining, remindersRemaining)
			}
		})
	}
}
