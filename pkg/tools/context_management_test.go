package tools

import (
	"context"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestContextManagementSkipDeniedWhenRatioNegative tests that skip is denied when proactive ratio <= 0
func TestContextManagementSkipDeniedWhenRatioNegative(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with poor proactive behavior
	// Note: MarkDecisionMade() will increment ProactiveDecisions by 1
	agentCtx.ContextMgmtState.ProactiveDecisions = 0
	agentCtx.ContextMgmtState.ReminderNeeded = 5
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	agentCtx.ContextMgmtState.ReminderFrequency = 5

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// Try to skip 30 turns despite poor proactive ratio
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing skip denial",
		"skip_turns": 30,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify error message contains "skip request denied"
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}
	if !strings.Contains(resultText, "skip request denied") {
		t.Errorf("Expected error message to contain 'skip request denied', got: %s", resultText)
	}

	// Verify ratio is negative (after MarkDecisionMade increments ProactiveDecisions to 1, ratio = 1 - 5 = -4)
	if !strings.Contains(resultText, "ratio=-4") {
		t.Errorf("Expected error message to show ratio=-4, got: %s", resultText)
	}

	// Verify SkipUntilTurn was NOT set (reminders continue normally)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 0 {
		t.Errorf("SkipUntilTurn should not be set when skip is denied, got: %d",
			agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestContextManagementSkipReducedWhenOverLimit tests that skip is reduced when over the proactive limit
func TestContextManagementSkipReducedWhenOverLimit(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with good proactive behavior but not excellent
	// Note: MarkDecisionMade() will increment ProactiveDecisions by 1
	agentCtx.ContextMgmtState.ProactiveDecisions = 10
	agentCtx.ContextMgmtState.ReminderNeeded = 0
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	agentCtx.ContextMgmtState.ReminderFrequency = 10

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// Try to skip 30 turns, but ratio will be 11 (after MarkDecisionMade increments to 11)
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing skip reduction",
		"skip_turns": 30,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify warning message contains "reduced from 30 to 11"
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}
	if !strings.Contains(resultText, "reduced from 30 to 11") {
		t.Errorf("Expected warning message to contain 'reduced from 30 to 11', got: %s", resultText)
	}

	// Verify ratio is shown correctly (11 after MarkDecisionMade)
	if !strings.Contains(resultText, "proactive ratio is 11") {
		t.Errorf("Expected warning message to show proactive ratio is 11, got: %s", resultText)
	}

	// Verify SkipUntilTurn was set with the reduced value (11 turns)
	expectedSkipUntil := 10 + 11 // current turn + maxSkip
	if agentCtx.ContextMgmtState.SkipUntilTurn != expectedSkipUntil {
		t.Errorf("SkipUntilTurn should be %d, got: %d",
			expectedSkipUntil, agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestContextManagementSkipCappedAt30 tests that skip is capped at 30 turns even with high ratio
func TestContextManagementSkipCappedAt30(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with excellent proactive behavior (ratio = 40)
	agentCtx.ContextMgmtState.ProactiveDecisions = 40
	agentCtx.ContextMgmtState.ReminderNeeded = 0
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	agentCtx.ContextMgmtState.ReminderFrequency = 30

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// Request skip of 30 turns (which should be allowed but capped at 30)
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing skip cap at 30",
		"skip_turns": 30,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify success message (no reduction needed)
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}
	if strings.Contains(resultText, "reduced") {
		t.Errorf("Expected no reduction when ratio >= 30, got: %s", resultText)
	}

	// Verify SkipUntilTurn was set with 30 (not 40)
	expectedSkipUntil := 10 + 30 // current turn + capped maxSkip
	if agentCtx.ContextMgmtState.SkipUntilTurn != expectedSkipUntil {
		t.Errorf("SkipUntilTurn should be %d (capped at 30), got: %d",
			expectedSkipUntil, agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestContextManagementSkipSuccessWhenWithinLimit tests normal skip when within proactive limit
func TestContextManagementSkipSuccessWhenWithinLimit(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with good proactive behavior
	agentCtx.ContextMgmtState.ProactiveDecisions = 10
	agentCtx.ContextMgmtState.ReminderNeeded = 0
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	agentCtx.ContextMgmtState.ReminderFrequency = 10

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// Request skip of 5 turns (within the limit of 10)
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing normal skip",
		"skip_turns": 5,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify success message
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}
	if !strings.Contains(resultText, "Skipping reminders for 5 turns") {
		t.Errorf("Expected success message to contain 'Skipping reminders for 5 turns', got: %s", resultText)
	}

	// Verify SkipUntilTurn was set correctly
	expectedSkipUntil := 10 + 5 // current turn + requested skip
	if agentCtx.ContextMgmtState.SkipUntilTurn != expectedSkipUntil {
		t.Errorf("SkipUntilTurn should be %d, got: %d",
			expectedSkipUntil, agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestContextManagementRemindersRemainingCalculation tests the calculation of reminders_remaining
func TestContextManagementRemindersRemainingCalculation(t *testing.T) {
	tests := []struct {
		name                 string
		currentTurn          int
		lastReminderTurn     int
		frequency            int
		skipUntilTurn        int
		expectedRemain       int
		proactiveDecisions   int
		reminderNeeded       int
	}{
		{
			name:             "normal period - reminder upcoming",
			currentTurn:      10,
			lastReminderTurn: 5,
			frequency:        10,
			skipUntilTurn:    0,
			expectedRemain:   5, // 5 + 10 - 10 = 5
			proactiveDecisions: 5,
			reminderNeeded: 2,
		},
		{
			name:             "normal period - reminder due now",
			currentTurn:      15,
			lastReminderTurn: 5,
			frequency:        10,
			skipUntilTurn:    0,
			expectedRemain:   0, // 5 + 10 - 15 = 0
			proactiveDecisions: 5,
			reminderNeeded: 2,
		},
		{
			name:             "in skip period",
			currentTurn:      10,
			lastReminderTurn: 5,
			frequency:        10,
			skipUntilTurn:    20,
			expectedRemain:   10, // 20 - 10 = 10
			proactiveDecisions: 5,
			reminderNeeded: 2,
		},
		{
			name:             "just before skip ends",
			currentTurn:      19,
			lastReminderTurn: 5,
			frequency:        10,
			skipUntilTurn:    20,
			expectedRemain:   1, // 20 - 19 = 1
			proactiveDecisions: 5,
			reminderNeeded: 2,
		},
		{
			name:             "after skip ends",
			currentTurn:      20,
			lastReminderTurn: 5,
			frequency:        10,
			skipUntilTurn:    20,
			expectedRemain:   0, // Skip ended, normal calc: 5 + 10 - 20 = -5 -> 0
			proactiveDecisions: 5,
			reminderNeeded: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentCtx := &agentctx.AgentContext{
				Messages:         []agentctx.AgentMessage{},
				ContextMgmtState: agentctx.DefaultContextMgmtState(),
			}

			agentCtx.ContextMgmtState.SetCurrentTurn(tt.currentTurn)
			agentCtx.LockContextManagement()
			agentCtx.ContextMgmtState.LastReminderTurn = tt.lastReminderTurn
			agentCtx.ContextMgmtState.ReminderFrequency = tt.frequency
			agentCtx.ContextMgmtState.SkipUntilTurn = tt.skipUntilTurn
			agentCtx.ContextMgmtState.ProactiveDecisions = tt.proactiveDecisions
			agentCtx.ContextMgmtState.ReminderNeeded = tt.reminderNeeded
			agentCtx.UnlockContextManagement()

			// Test our manual calculation (mirror of loop.go logic)
			state := agentCtx.ContextMgmtState
			stateSnapshot := state.Snapshot()

			remindersRemaining := 0
			currentTurn := stateSnapshot.CurrentTurn

			// Account for skip period: if we're in skip, that's when the next reminder is due
			if state.SkipUntilTurn > 0 && currentTurn < state.SkipUntilTurn {
				// We're in a skip period - next reminder is at SkipUntilTurn
				remindersRemaining = state.SkipUntilTurn - currentTurn
			} else if stateSnapshot.ReminderFrequency > 0 {
				// Normal period - calculate based on reminder frequency
				remindersRemaining = stateSnapshot.LastReminderTurn + stateSnapshot.ReminderFrequency - currentTurn
				if remindersRemaining < 0 {
					remindersRemaining = 0
				}
			}

			if remindersRemaining != tt.expectedRemain {
				t.Errorf("Expected reminders_remaining=%d, got %d (currentTurn=%d, lastReminder=%d, freq=%d, skipUntil=%d)",
					tt.expectedRemain, remindersRemaining, tt.currentTurn, tt.lastReminderTurn, tt.frequency, tt.skipUntilTurn)
			}
		})
	}
}

// TestContextManagementProactiveRatioZero tests edge case where ratio is exactly 0
func TestContextManagementProactiveRatioZero(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with ratio = 0 (proactive = reminded)
	// Note: MarkDecisionMade() will increment ProactiveDecisions by 1, making ratio = 1
	agentCtx.ContextMgmtState.ProactiveDecisions = 5
	agentCtx.ContextMgmtState.ReminderNeeded = 5
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	agentCtx.ContextMgmtState.ReminderFrequency = 5

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// Try to skip
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing zero ratio",
		"skip_turns": 10,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify skip is NOT denied (ratio becomes 1 after MarkDecisionMade)
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// With ratio = 1, skip should be reduced to 1
	if !strings.Contains(resultText, "reduced from 10 to 1") {
		t.Errorf("Expected skip to be reduced to 1 when ratio=1, got: %s", resultText)
	}

	if !strings.Contains(resultText, "proactive ratio is 1") {
		t.Errorf("Expected message to show proactive ratio is 1, got: %s", resultText)
	}
}

// Integration-style test: Test context management behavior with LLM in needs_improvement state
// This simulates the scenario described in the issue where LLM tries to escape reminders
func TestContextManagementIntegrationNeedsImprovementScenario(t *testing.T) {
	// Simulate LLM that keeps being reminded and never acts proactively
	agentCtx := &agentctx.AgentContext{
		Messages:         []agentctx.AgentMessage{},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// LLM has been reminded 10 times without acting proactively
	// Note: MarkDecisionMade() will increment ProactiveDecisions by 1, making ratio = -9
	agentCtx.ContextMgmtState.ProactiveDecisions = 0
	agentCtx.ContextMgmtState.ReminderNeeded = 10
	agentCtx.ContextMgmtState.SetCurrentTurn(50)
	agentCtx.ContextMgmtState.ReminderFrequency = 5 // needs_improvement = 5 turns

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)

	// LLM tries to skip for 30 turns to escape reminders
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Context looks okay, I'll skip",
		"skip_turns": 30,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify skip is denied
	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}
	if !strings.Contains(resultText, "skip request denied") {
		t.Errorf("Expected skip to be denied for non-proactive LLM, got: %s", resultText)
	}

	// Verify error message explains the problem
	if !strings.Contains(resultText, "not proactive enough") {
		t.Errorf("Expected message to explain LLM is not proactive enough, got: %s", resultText)
	}

	// After MarkDecisionMade, ProactiveDecisions = 1, ReminderNeeded = 10, ratio = -9
	if !strings.Contains(resultText, "ratio=-9") {
		t.Errorf("Expected message to show ratio=-9, got: %s", resultText)
	}

	// Verify reminders will still trigger
	if !strings.Contains(resultText, "You will still receive a remind") {
		t.Errorf("Expected message to say reminders will still trigger, got: %s", resultText)
	}

	// Verify SkipUntilTurn was NOT set
	if agentCtx.ContextMgmtState.SkipUntilTurn != 0 {
		t.Errorf("SkipUntilTurn should not be set when skip is denied, got: %d",
			agentCtx.ContextMgmtState.SkipUntilTurn)
	}

	// Now test: LLM makes another proactive decision (another call to skip)
	// After first call: ProactiveDecisions = 1, ReminderNeeded = 10, ratio = -9
	// After this call: ProactiveDecisions = 2, ReminderNeeded = 10, ratio = -8
	params["reasoning"] = "Trying again after being denied"
	resultBlocks, err = tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}
	if !strings.Contains(resultText, "skip request denied") {
		t.Errorf("Expected skip still denied with ratio=-8, got: %s", resultText)
	}
	if !strings.Contains(resultText, "ratio=-8") {
		t.Errorf("Expected message to show ratio=-8, got: %s", resultText)
	}
}

// TestContextManagementTruncateStillWorks tests that truncate functionality is not affected
func TestContextManagementTruncateStillWorks(t *testing.T) {
	// Create assistant message with context_management call
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_cm",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":     "truncate",
				"reasoning":    "Testing truncate",
				"truncate_ids": "call_read",
			},
		},
	}

	toolResult := agentctx.NewToolResultMessage("call_read", "read", []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "large content to truncate"},
	}, false)

	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{
			assistant,
			toolResult,
		},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	agentCtx.ContextMgmtState.SetCurrentTurn(1)

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_cm")

	params := map[string]any{
		"decision":     "truncate",
		"reasoning":    "Testing truncate",
		"truncate_ids": "call_read",
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}
	if !strings.Contains(resultText, "Truncated 1 tool output") {
		t.Errorf("Expected truncate to work, got: %s", resultText)
	}

	// Verify content was truncated
	// The tool result should be at index 1 in agentCtx.Messages
	msg := agentCtx.Messages[1]
	content := msg.ExtractText()
	t.Logf("Tool result content after truncate: %s", content)

	// Check if it matches the truncated tag pattern
	agentMsg := agentctx.IsTruncatedAgentToolTag(content)
	if !agentMsg {
		t.Errorf("Expected tool output to be truncated, but got content: %s", content)
	}
}