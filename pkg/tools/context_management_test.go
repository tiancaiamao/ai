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
