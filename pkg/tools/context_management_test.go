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

func TestContextManagementRemindedDecisionUpdatesCountersAndInvalidatesRuntimeSnapshot(t *testing.T) {
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_cm_reminded",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":  "compact",
				"reasoning": "reminded decision",
			},
		},
	}

	state := agentctx.DefaultContextMgmtState()
	state.SetCurrentTurn(5)
	state.RecordReminder(5, "high")
	state.MarkReminderShown()

	agentCtx := &agentctx.AgentContext{
		Messages:            []agentctx.AgentMessage{assistant},
		ContextMgmtState:    state,
		RuntimeMetaSnapshot: "<agent:runtime_state/>",
		RuntimeMetaBand:     "0-20",
		RuntimeMetaTurns:    2,
	}

	tool := NewContextManagementTool(nil)
	execCtx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	execCtx = agentctx.WithToolExecutionCallID(execCtx, "call_cm_reminded")

	resultBlocks, err := tool.Execute(execCtx, map[string]any{
		"decision":  "compact",
		"reasoning": "reminded decision",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if state.ProactiveDecisions != 0 {
		t.Fatalf("expected proactive decisions to remain 0, got %d", state.ProactiveDecisions)
	}
	if state.ReminderNeeded != 1 {
		t.Fatalf("expected reminder-needed decisions to be 1, got %d", state.ReminderNeeded)
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}
	if !strings.Contains(resultText, "proactive=0, reminded=1") {
		t.Fatalf("expected updated stats in tool result, got: %s", resultText)
	}

	if agentCtx.RuntimeMetaSnapshot != "" {
		t.Fatalf("expected runtime snapshot cache to be invalidated, got: %q", agentCtx.RuntimeMetaSnapshot)
	}
	if agentCtx.RuntimeMetaTurns != 0 {
		t.Fatalf("expected runtime meta turns reset to 0, got: %d", agentCtx.RuntimeMetaTurns)
	}
}

// TestContextManagementSkipDenied tests that skip is denied when ratio <= 0
func TestContextManagementSkipDenied(t *testing.T) {
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_cm_skip",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":   "skip",
				"reasoning":  "Test skip denied",
				"skip_turns": 30,
			},
		},
	}

	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{assistant},
		ContextMgmtState: func() *agentctx.ContextMgmtState {
			state := agentctx.DefaultContextMgmtState()
			state.ProactiveDecisions = 0
			state.ReminderNeeded = 5
			state.ReminderFrequency = 10
			state.CurrentTurn = 10
			return state
		}(),
	}

	tool := NewContextManagementTool(nil)

	execCtx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	execCtx = agentctx.WithToolExecutionCallID(execCtx, "call_cm_skip")

	resultBlocks, err := tool.Execute(execCtx, map[string]any{
		"decision":   "skip",
		"reasoning":  "Test skip denied",
		"skip_turns": 30.0,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Check that skip was denied with proper message
	// ratio = 0 - 5 = -5
	if !strings.Contains(resultText, "⚠️ skip request denied") {
		t.Errorf("Expected skip denied message, got: %s", resultText)
	}
	if !strings.Contains(resultText, "ratio=-5") {
		t.Errorf("Expected ratio message, got: %s", resultText)
	}
	if !strings.Contains(resultText, "not allowed to skip 30 turns") {
		t.Errorf("Expected turns message, got: %s", resultText)
	}
	if !strings.Contains(resultText, "within 10 turns (turn 20)") {
		t.Errorf("Expected reminder timing message, got: %s", resultText)
	}
if !strings.Contains(resultText, "You must make proactive context management decisions") {
			t.Errorf("Expected proactive encouragement message, got: %s", resultText)
		}

	// Verify that skip was NOT actually applied
	if agentCtx.ContextMgmtState.SkipUntilTurn != 0 {
		t.Errorf("Expected SkipUntilTurn to remain 0, got: %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestContextManagementSkipReduced tests that skip is reduced when skipTurns > maxSkip
func TestContextManagementSkipReduced(t *testing.T) {
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_cm_skip",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":   "skip",
				"reasoning":  "Test skip reduced",
				"skip_turns": 20,
			},
		},
	}

	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{assistant},
		ContextMgmtState: func() *agentctx.ContextMgmtState {
			state := agentctx.DefaultContextMgmtState()
			state.ProactiveDecisions = 5
			state.ReminderNeeded = 3
			state.ReminderFrequency = 10
			state.CurrentTurn = 10
			return state
		}(),
	}

	tool := NewContextManagementTool(nil)

	execCtx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	execCtx = agentctx.WithToolExecutionCallID(execCtx, "call_cm_skip")

	resultBlocks, err := tool.Execute(execCtx, map[string]any{
		"decision":   "skip",
		"reasoning":  "Test skip reduced",
		"skip_turns": 20,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Check that skip was reduced with proper message
	if !strings.Contains(resultText, "⚠️ skip_turns reduced from 20 to 2") {
		t.Errorf("Expected skip reduced message, got: %s", resultText)
	}
	if !strings.Contains(resultText, "proactive ratio is low") {
		t.Errorf("Expected proactive ratio low message, got: %s", resultText)
	}

	// Verify that reduced skip was applied (maxSkip = 5 - 3 = 2)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 12 {
		t.Errorf("Expected SkipUntilTurn to be 12, got: %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestContextManagementSkipSuccess tests that skip succeeds when within limits
func TestContextManagementSkipSuccess(t *testing.T) {
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_cm_skip",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":   "skip",
				"reasoning":  "Test skip success",
				"skip_turns": 5,
			},
		},
	}

	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{assistant},
		ContextMgmtState: func() *agentctx.ContextMgmtState {
			state := agentctx.DefaultContextMgmtState()
			state.ProactiveDecisions = 10
			state.ReminderNeeded = 2
			state.ReminderFrequency = 10
			state.CurrentTurn = 10
			return state
		}(),
	}

	tool := NewContextManagementTool(nil)

	execCtx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	execCtx = agentctx.WithToolExecutionCallID(execCtx, "call_cm_skip")

	resultBlocks, err := tool.Execute(execCtx, map[string]any{
		"decision":   "skip",
		"reasoning":  "Test skip success",
		"skip_turns": 5,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Check that skip succeeded normally (no reduction since 5 <= 8)
	if !strings.Contains(resultText, "Skipping reminders for 5 turns") {
		t.Errorf("Expected success message, got: %s", resultText)
	}
	if !strings.Contains(resultText, "Next reminder at turn 15") {
		t.Errorf("Expected next reminder message, got: %s", resultText)
	}

	// Verify that skip was applied
	if agentCtx.ContextMgmtState.SkipUntilTurn != 15 {
		t.Errorf("Expected SkipUntilTurn to be 15, got: %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestContextManagementSkipExactMaxSkip tests that skip succeeds when skipTurns == maxSkip
func TestContextManagementSkipExactMaxSkip(t *testing.T) {
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_cm_skip",
			Type: "toolCall",
			Name: "context_management",
			Arguments: map[string]any{
				"decision":   "skip",
				"reasoning":  "Test skip exact max",
				"skip_turns": 3,
			},
		},
	}

	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{assistant},
		ContextMgmtState: func() *agentctx.ContextMgmtState {
			state := agentctx.DefaultContextMgmtState()
			state.ProactiveDecisions = 5
			state.ReminderNeeded = 2
			state.ReminderFrequency = 10
			state.CurrentTurn = 10
			return state
		}(),
	}

	tool := NewContextManagementTool(nil)

	execCtx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	execCtx = agentctx.WithToolExecutionCallID(execCtx, "call_cm_skip")

	resultBlocks, err := tool.Execute(execCtx, map[string]any{
		"decision":   "skip",
		"reasoning":  "Test skip exact max",
		"skip_turns": 3,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Check that skip succeeded normally (no reduction since 3 == 3)
	if !strings.Contains(resultText, "Skipping reminders for 3 turns") {
		t.Errorf("Expected success message, got: %s", resultText)
	}

	// Verify that skip was applied
	if agentCtx.ContextMgmtState.SkipUntilTurn != 13 {
		t.Errorf("Expected SkipUntilTurn to be 13, got: %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

// TestContextManagementSkipEdgeCases tests edge cases (zero proactive, max limits, etc.)
func TestContextManagementSkipEdgeCases(t *testing.T) {
	tests := []struct {
		name               string
		proactiveDecisions int
		reminderNeeded     int
		requestedSkip      int
		expectedResult     string
		expectedSkipUntil  int
	}{
		{
			name:               "zero proactive, many reminders needed",
			proactiveDecisions: 0,
			reminderNeeded:     10,
			requestedSkip:      15,
			expectedResult:     "skip request denied",
			expectedSkipUntil:  0,
		},
		{
			name:               "equal proactive and reminders",
			proactiveDecisions: 5,
			reminderNeeded:     5,
			requestedSkip:      10,
			expectedResult:     "skip request denied",
			expectedSkipUntil:  0,
		},
		{
			name:               "max skip capped at 30",
			proactiveDecisions: 50,
			reminderNeeded:     0,
			requestedSkip:      50,
			expectedResult:     "Skipping reminders for 30 turns",
			expectedSkipUntil:  40,
		},
		{
			name:               "min skip capped at 1",
			proactiveDecisions: 1,
			reminderNeeded:     0,
			requestedSkip:      0,
			expectedResult:     "Skipping reminders for 1 turns",
			expectedSkipUntil:  11,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assistant := agentctx.NewAssistantMessage()
			assistant.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:   "call_cm_skip",
					Type: "toolCall",
					Name: "context_management",
					Arguments: map[string]any{
						"decision":   "skip",
						"reasoning":  tt.name,
						"skip_turns": tt.requestedSkip,
					},
				},
			}

			agentCtx := &agentctx.AgentContext{
				Messages: []agentctx.AgentMessage{assistant},
				ContextMgmtState: func() *agentctx.ContextMgmtState {
					state := agentctx.DefaultContextMgmtState()
					state.ProactiveDecisions = tt.proactiveDecisions
					state.ReminderNeeded = tt.reminderNeeded
					state.ReminderFrequency = 10
					state.CurrentTurn = 10
					return state
				}(),
			}

			tool := NewContextManagementTool(nil)

			execCtx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
			execCtx = agentctx.WithToolExecutionCallID(execCtx, "call_cm_skip")

			resultBlocks, err := tool.Execute(execCtx, map[string]any{
				"decision":   "skip",
				"reasoning":  tt.name,
				"skip_turns": tt.requestedSkip,
			})
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}

			if len(resultBlocks) == 0 {
				t.Fatal("Expected result blocks, got none")
			}

			resultText := ""
			if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
				resultText = tc.Text
			}

			if !strings.Contains(resultText, tt.expectedResult) {
				t.Errorf("Expected result to contain '%s', got: %s", tt.expectedResult, resultText)
			}

			if agentCtx.ContextMgmtState.SkipUntilTurn != tt.expectedSkipUntil {
				t.Errorf("Expected SkipUntilTurn to be %d, got: %d", tt.expectedSkipUntil, agentCtx.ContextMgmtState.SkipUntilTurn)
			}
		})
	}
}
