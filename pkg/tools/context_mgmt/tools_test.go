package context_mgmt

import (
	"context"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func newTestAgentContext(messages []agentctx.AgentMessage) *agentctx.AgentContext {
	return &agentctx.AgentContext{
		RecentMessages: messages,
		AgentState: agentctx.AgentState{
			TotalTurns:    10,
			TokensLimit:   200000,
		},
	}
}

func makeToolResult(id, toolName, content string, truncated bool) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role:       "toolResult",
		ToolCallID: id,
		ToolName:   toolName,
		Truncated:  truncated,
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: content},
		},
	}
}

func makeUserMessage(text string) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role: "user",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: text},
		},
	}
}

// --- TruncateMessagesTool tests ---

func TestTruncateMessagesTool_FilterValidIDs(t *testing.T) {
	messages := make([]agentctx.AgentMessage, agentctx.RecentMessagesKeep+5)
	for i := range messages {
		if i < 5 {
			messages[i] = makeToolResult("old-"+string(rune('a'+i)), "bash", "big output "+string(rune('a'+i)), false)
		} else {
			messages[i] = makeUserMessage("msg")
		}
	}

	agentCtx := newTestAgentContext(messages)
	tool := NewTruncateMessagesTool(agentCtx)

	valid := tool.filterValidIDs([]string{"old-a", "old-b", "nonexistent"})
	if len(valid) != 2 {
		t.Errorf("expected 2 valid IDs, got %d: %v", len(valid), valid)
	}
}

func TestTruncateMessagesTool_ProtectedRegionCannotBeTruncated(t *testing.T) {
	messages := make([]agentctx.AgentMessage, agentctx.RecentMessagesKeep+5)
	for i := range messages {
		if i < 5 {
			messages[i] = makeToolResult("old-"+string(rune('a'+i)), "bash", "output", false)
		} else if i == len(messages)-1 {
			messages[i] = makeToolResult("protected-id", "bash", "recent output", false)
		} else {
			messages[i] = makeUserMessage("msg")
		}
	}

	agentCtx := newTestAgentContext(messages)
	tool := NewTruncateMessagesTool(agentCtx)

	valid := tool.filterValidIDs([]string{"protected-id"})
	if len(valid) != 0 {
		t.Errorf("protected ID should not be valid, got %d: %v", len(valid), valid)
	}
}

func TestTruncateMessagesTool_AlreadyTruncatedSkipped(t *testing.T) {
	messages := []agentctx.AgentMessage{
		makeToolResult("already-truncated", "bash", "output", true),
	}

	agentCtx := newTestAgentContext(messages)
	tool := NewTruncateMessagesTool(agentCtx)

	valid := tool.filterValidIDs([]string{"already-truncated"})
	if len(valid) != 0 {
		t.Errorf("already truncated ID should not be valid, got %d", len(valid))
	}
}

func TestTruncateMessagesTool_Execute(t *testing.T) {
	longOutput := make([]byte, 2000)
	for i := range longOutput {
		longOutput[i] = 'x'
	}

	// Build enough messages so that tc-1 falls outside the protected region
	messages := make([]agentctx.AgentMessage, 0, agentctx.RecentMessagesKeep+5)
	messages = append(messages, makeToolResult("tc-1", "bash", string(longOutput), false))
	for i := 0; i < agentctx.RecentMessagesKeep; i++ {
		messages = append(messages, makeUserMessage("msg"))
	}

	agentCtx := newTestAgentContext(messages)
	tool := NewTruncateMessagesTool(agentCtx)

	result, err := tool.Execute(context.Background(), map[string]any{"message_ids": "tc-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected result content")
	}

	if !agentCtx.RecentMessages[0].Truncated {
		t.Error("message should be marked as truncated")
	}
	if agentCtx.RecentMessages[0].OriginalSize != 2000 {
		t.Errorf("expected OriginalSize=2000, got %d", agentCtx.RecentMessages[0].OriginalSize)
	}
}

func TestTruncateMessagesTool_Execute_NoValidIDs(t *testing.T) {
	agentCtx := newTestAgentContext(nil)
	tool := NewTruncateMessagesTool(agentCtx)

	_, err := tool.Execute(context.Background(), map[string]any{"message_ids": "nonexistent"})
	if err == nil {
		t.Error("expected error for no valid IDs")
	}
}

func TestTruncateMessagesTool_Execute_MissingParam(t *testing.T) {
	agentCtx := newTestAgentContext(nil)
	tool := NewTruncateMessagesTool(agentCtx)

	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing message_ids")
	}
}

// --- UpdateLLMContextTool tests ---

func TestUpdateLLMContextTool_Execute(t *testing.T) {
	agentCtx := newTestAgentContext(nil)
	tool := NewUpdateLLMContextTool(agentCtx)

	newContext := "## Current Task\nFixing bug in foo.go"
	result, err := tool.Execute(context.Background(), map[string]any{"llm_context": newContext})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected result content")
	}
	if agentCtx.LLMContext != newContext {
		t.Errorf("LLMContext not updated, got: %s", agentCtx.LLMContext)
	}
	if agentCtx.AgentState.LastLLMContextUpdate != 10 {
		t.Errorf("LastLLMContextUpdate not set correctly, got: %d", agentCtx.AgentState.LastLLMContextUpdate)
	}
}

func TestUpdateLLMContextTool_Execute_Empty(t *testing.T) {
	agentCtx := newTestAgentContext(nil)
	tool := NewUpdateLLMContextTool(agentCtx)

	_, err := tool.Execute(context.Background(), map[string]any{"llm_context": ""})
	if err == nil {
		t.Error("expected error for empty llm_context")
	}
}

func TestUpdateLLMContextTool_Execute_MissingParam(t *testing.T) {
	agentCtx := newTestAgentContext(nil)
	tool := NewUpdateLLMContextTool(agentCtx)

	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Error("expected error for missing llm_context")
	}
}

// --- NoActionTool tests ---

func TestNoActionTool_Execute(t *testing.T) {
	agentCtx := newTestAgentContext(nil)
	tool := NewNoActionTool(agentCtx)

	result, err := tool.Execute(context.Background(), map[string]any{"reason": "context is fine"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected result content")
	}
	text := result[0].(agentctx.TextContent).Text
	if text != "No action taken: context is fine." {
		t.Errorf("unexpected result: %s", text)
	}
}

func TestNoActionTool_Execute_DefaultReason(t *testing.T) {
	agentCtx := newTestAgentContext(nil)
	tool := NewNoActionTool(agentCtx)

	result, _ := tool.Execute(context.Background(), map[string]any{})
	text := result[0].(agentctx.TextContent).Text
	if text != "No action taken: context is healthy." {
		t.Errorf("unexpected default reason: %s", text)
	}
}

// --- Registry tests ---

func TestGetMiniCompactTools(t *testing.T) {
	agentCtx := newTestAgentContext(nil)
	tools := GetMiniCompactTools(agentCtx)
	if len(tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(tools))
	}

	names := make(map[string]bool)
	for _, tool := range tools {
		names[tool.Name()] = true
	}
	for _, name := range []string{"truncate_messages", "update_llm_context", "no_action"} {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}
