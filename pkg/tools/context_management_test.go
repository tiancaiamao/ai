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

func TestContextManagementSkipLimitDenyWhenRatioZero(t *testing.T) {
	// Test case 1: ratio <= 0 should DENY skip
	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{
			agentctx.NewAssistantMessage(),
		},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with ratio <= 0 (no proactive decisions)
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	agentCtx.ContextMgmtState.RecordDecision(5, "compact", true) // was reminded

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip_test")

	// Try to skip 30 turns when ratio is 0
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing deny when ratio <= 0",
		"skip_turns": 30,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Check that skip was denied
	if len(resultBlocks) == 0 {
		t.Fatal("Expected result blocks, got none")
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Should contain deny message
	if !strings.Contains(resultText, "skip request denied") {
		t.Errorf("Expected deny message, got: %s", resultText)
	}

	// Should mention ratio
	if !strings.Contains(resultText, "ratio=") {
		t.Errorf("Expected ratio in message, got: %s", resultText)
	}

	// SkipUntilTurn should NOT be set (skip denied)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 0 {
		t.Errorf("Expected SkipUntilTurn to be 0 (skip denied), got %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

func TestContextManagementSkipLimitDenyWhenRatioNegative(t *testing.T) {
	// Test case: ratio < 0 should DENY skip
	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{
			agentctx.NewAssistantMessage(),
		},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with negative ratio (more reminders than proactive decisions)
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	agentCtx.ContextMgmtState.RecordDecision(5, "compact", true) // was reminded
	agentCtx.ContextMgmtState.RecordDecision(6, "compact", true) // was reminded
	agentCtx.ContextMgmtState.RecordDecision(7, "compact", true) // was reminded
	// proactive=0, reminded=3, ratio=-3

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip_test")

	// Try to skip 15 turns when ratio is -3
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing deny when ratio is negative",
		"skip_turns": 15,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Should contain deny message
	if !strings.Contains(resultText, "skip request denied") {
		t.Errorf("Expected deny message, got: %s", resultText)
	}

	// Should mention negative ratio (-3)
	if !strings.Contains(resultText, "ratio=-3") {
		t.Errorf("Expected ratio=-3 in message, got: %s", resultText)
	}

	// SkipUntilTurn should NOT be set
	if agentCtx.ContextMgmtState.SkipUntilTurn != 0 {
		t.Errorf("Expected SkipUntilTurn to be 0 (skip denied), got %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

func TestContextManagementSkipLimitReduceWhenExceedsMax(t *testing.T) {
	// Test case 2: skipTurns > maxSkip should REDUCE to maxSkip
	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{
			agentctx.NewAssistantMessage(),
		},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with ratio = 3 (max skip should be 3)
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	agentCtx.ContextMgmtState.RecordDecision(5, "compact", true)  // was reminded
	agentCtx.ContextMgmtState.RecordDecision(7, "truncate", false) // proactive
	agentCtx.ContextMgmtState.RecordDecision(9, "compact", false)  // proactive
	agentCtx.ContextMgmtState.RecordDecision(10, "truncate", false) // proactive
	// proactive=3, reminded=1, ratio=2, maxSkip=2

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip_test")

	// Try to skip 20 turns when max is 2
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing reduce when skipTurns > maxSkip",
		"skip_turns": 20,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Should contain reduce message
	if !strings.Contains(resultText, "skip turns reduced to") {
		t.Errorf("Expected reduce message, got: %s", resultText)
	}

	// Should mention proactive ratio
	if !strings.Contains(resultText, "proactive ratio") {
		t.Errorf("Expected proactive ratio in message, got: %s", resultText)
	}

	// SkipUntilTurn should be set to reduced value (12, which is turn 10 + 2)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 12 {
		t.Errorf("Expected SkipUntilTurn to be 12 (reduced from 20), got %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

func TestContextManagementSkipLimitSuccessWhenWithinMax(t *testing.T) {
	// Test case 3: skipTurns <= maxSkip should SUCCESS
	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{
			agentctx.NewAssistantMessage(),
		},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with ratio = 5 (max skip should be 5)
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	agentCtx.ContextMgmtState.RecordDecision(5, "compact", true)  // was reminded
	agentCtx.ContextMgmtState.RecordDecision(7, "truncate", false) // proactive
	agentCtx.ContextMgmtState.RecordDecision(9, "compact", false)  // proactive
	agentCtx.ContextMgmtState.RecordDecision(10, "truncate", false) // proactive
	agentCtx.ContextMgmtState.RecordDecision(11, "compact", false)  // proactive
	agentCtx.ContextMgmtState.RecordDecision(12, "truncate", false) // proactive
	// Before: proactive=5, reminded=1, ratio=4
	// After MarkDecisionMade(): proactive=6, reminded=1, ratio=5, maxSkip=5

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip_test")

	// Skip 3 turns when max is 5 (should succeed)
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing success when skipTurns <= maxSkip",
		"skip_turns": 3,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Should contain success message
	if !strings.Contains(resultText, "Deferred for 3 turns") {
		t.Errorf("Expected success message, got: %s", resultText)
	}

	// Should NOT contain deny or reduce messages
	if strings.Contains(resultText, "skip request denied") || strings.Contains(resultText, "skip turns reduced") {
		t.Errorf("Unexpected deny/reduce message in success case: %s", resultText)
	}

	// SkipUntilTurn should be set to 13 (turn 10 + 3)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 13 {
		t.Errorf("Expected SkipUntilTurn to be 13, got %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}

func TestContextManagementSkipLimitMaxClampedAt30(t *testing.T) {
	// Test case: max_skip should be clamped at 30 when ratio is very high
	agentCtx := &agentctx.AgentContext{
		Messages: []agentctx.AgentMessage{
			agentctx.NewAssistantMessage(),
		},
		ContextMgmtState: agentctx.DefaultContextMgmtState(),
	}

	// Set up state with very high ratio (50 proactive, 0 reminded)
	agentCtx.ContextMgmtState.SetCurrentTurn(10)
	for i := 0; i < 50; i++ {
		agentCtx.ContextMgmtState.RecordDecision(5+i, "compact", false) // all proactive
	}
	// proactive=50, reminded=0, ratio=50, but maxSkip should be clamped to 30

	tool := NewContextManagementTool(nil)
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentCtx)
	ctx = agentctx.WithToolExecutionCallID(ctx, "call_skip_test")

	// Request 30 turns (the max allowed)
	// With ratio=50, maxSkip=30, and request=30, this should succeed (not reduce)
	params := map[string]any{
		"decision":  "skip",
		"reasoning": "Testing max skip clamped at 30",
		"skip_turns": 30,
	}

	resultBlocks, err := tool.Execute(ctx, params)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	resultText := ""
	if tc, ok := resultBlocks[0].(agentctx.TextContent); ok {
		resultText = tc.Text
	}

	// Should be a normal defer since 30 <= maxSkip (which is clamped to 30)
	if !strings.Contains(resultText, "Deferred for 30 turns") {
		t.Errorf("Expected defer message, got: %s", resultText)
	}

	// SkipUntilTurn should be set to 40 (turn 10 + 30)
	if agentCtx.ContextMgmtState.SkipUntilTurn != 40 {
		t.Errorf("Expected SkipUntilTurn to be 40, got %d", agentCtx.ContextMgmtState.SkipUntilTurn)
	}
}
