package main

import (
	"encoding/json"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/rpc"
)

func TestFormatMessagesForDisplay_BasicUserMessage(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("Hello, world!"),
	}

	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if result.Total != 1 {
		t.Errorf("Expected Total=1, got %d", result.Total)
	}
	if result.Showing != 1 {
		t.Errorf("Expected Showing=1, got %d", result.Showing)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(result.Messages))
	}
	m := result.Messages[0]
	if m.Index != 0 {
		t.Errorf("Expected Index=0, got %d", m.Index)
	}
	if m.Role != "user" {
		t.Errorf("Expected Role=user, got %s", m.Role)
	}
	if m.Preview != "Hello, world!" {
		t.Errorf("Expected Preview='Hello, world!', got '%s'", m.Preview)
	}
}

func TestFormatMessagesForDisplay_Truncation(t *testing.T) {
	longText := strings.Repeat("a", 300)
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage(longText),
	}

	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if len(result.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(result.Messages))
	}
	expected := strings.Repeat("a", 200) + "..."
	if result.Messages[0].Preview != expected {
		t.Errorf("Expected preview truncated to 200 chars + '...', got length %d", len(result.Messages[0].Preview))
	}
}

func TestFormatMessagesForDisplay_CountLimit(t *testing.T) {
	msgs := make([]agentctx.AgentMessage, 50)
	for i := range msgs {
		msgs[i] = agentctx.NewUserMessage("msg")
	}

	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if result.Total != 50 {
		t.Errorf("Expected Total=50, got %d", result.Total)
	}
	if result.Showing != 20 {
		t.Errorf("Expected Showing=20, got %d", result.Showing)
	}
	if len(result.Messages) != 20 {
		t.Fatalf("Expected 20 messages, got %d", len(result.Messages))
	}
	// First message should have index 30 (50-20)
	if result.Messages[0].Index != 30 {
		t.Errorf("Expected first message Index=30, got %d", result.Messages[0].Index)
	}
	// Last message should have index 49
	if result.Messages[19].Index != 49 {
		t.Errorf("Expected last message Index=49, got %d", result.Messages[19].Index)
	}
}

func TestFormatMessagesForDisplay_CountExceedsTotal(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("msg1"),
		agentctx.NewUserMessage("msg2"),
	}

	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if result.Total != 2 {
		t.Errorf("Expected Total=2, got %d", result.Total)
	}
	if result.Showing != 2 {
		t.Errorf("Expected Showing=2, got %d", result.Showing)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(result.Messages))
	}
}

func TestFormatMessagesForDisplay_EmptyMessages(t *testing.T) {
	msgs := []agentctx.AgentMessage{}

	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if result.Total != 0 {
		t.Errorf("Expected Total=0, got %d", result.Total)
	}
	if result.Showing != 0 {
		t.Errorf("Expected Showing=0, got %d", result.Showing)
	}
	if len(result.Messages) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(result.Messages))
	}
}

func TestFormatMessagesForDisplay_ToolCalls(t *testing.T) {
	msg := agentctx.NewAssistantMessage()
	msg.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:   "call_1",
			Type: "toolCall",
			Name: "bash",
			Arguments: map[string]any{
				"command": "ls -la",
			},
		},
		agentctx.ToolCallContent{
			ID:   "call_2",
			Type: "toolCall",
			Name: "read",
			Arguments: map[string]any{
				"path": "/tmp/test.txt",
			},
		},
		agentctx.TextContent{
			Type: "text",
			Text: "Let me check the files",
		},
	}

	msgs := []agentctx.AgentMessage{msg}
	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if len(result.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(result.Messages))
	}
	m := result.Messages[0]
	if m.Role != "assistant" {
		t.Errorf("Expected Role=assistant, got %s", m.Role)
	}
	if m.Preview != "Let me check the files" {
		t.Errorf("Expected Preview='Let me check the files', got '%s'", m.Preview)
	}
	if len(m.ToolCalls) != 2 {
		t.Fatalf("Expected 2 tool calls, got %d", len(m.ToolCalls))
	}
	if m.ToolCalls[0] != "bash" {
		t.Errorf("Expected ToolCalls[0]='bash', got '%s'", m.ToolCalls[0])
	}
	if m.ToolCalls[1] != "read" {
		t.Errorf("Expected ToolCalls[1]='read', got '%s'", m.ToolCalls[1])
	}
}

func TestFormatMessagesForDisplay_ToolResult(t *testing.T) {
	msg := agentctx.NewToolResultMessage("call_1", "bash", []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "file1.txt\nfile2.txt"},
	}, false)

	msgs := []agentctx.AgentMessage{msg}
	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if len(result.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(result.Messages))
	}
	m := result.Messages[0]
	if m.Role != "toolResult" {
		t.Errorf("Expected Role=toolResult, got %s", m.Role)
	}
	if m.ToolName != "bash" {
		t.Errorf("Expected ToolName='bash', got '%s'", m.ToolName)
	}
	if m.Preview != "file1.txt\nfile2.txt" {
		t.Errorf("Expected Preview='file1.txt\\nfile2.txt', got '%s'", m.Preview)
	}
	if m.IsError {
		t.Error("Expected IsError=false")
	}
}

func TestFormatMessagesForDisplay_ToolResultError(t *testing.T) {
	msg := agentctx.NewToolResultMessage("call_1", "bash", []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "command not found"},
	}, true)

	msgs := []agentctx.AgentMessage{msg}
	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if len(result.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(result.Messages))
	}
	if !result.Messages[0].IsError {
		t.Error("Expected IsError=true")
	}
}

func TestFormatMessagesForDisplay_ThinkingFallback(t *testing.T) {
	msg := agentctx.NewAssistantMessage()
	msg.Content = []agentctx.ContentBlock{
		agentctx.ThinkingContent{
			Type:     "thinking",
			Thinking: "I should analyze the problem first",
		},
	}

	msgs := []agentctx.AgentMessage{msg}
	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if len(result.Messages) != 1 {
		t.Fatalf("Expected 1 message, got %d", len(result.Messages))
	}
	m := result.Messages[0]
	if m.Preview != "(thinking) I should analyze the problem first" {
		t.Errorf("Expected thinking preview, got '%s'", m.Preview)
	}
}

func TestFormatMessagesForDisplay_MixedMessages(t *testing.T) {
	userMsg := agentctx.NewUserMessage("Fix the bug")
	assistantMsg := agentctx.NewAssistantMessage()
	assistantMsg.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:        "call_1",
			Type:      "toolCall",
			Name:      "bash",
			Arguments: map[string]any{"command": "go test ./..."},
		},
	}
	toolResultMsg := agentctx.NewToolResultMessage("call_1", "bash", []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "PASS"},
	}, false)

	msgs := []agentctx.AgentMessage{userMsg, assistantMsg, toolResultMsg}
	result := rpc.FormatMessagesForDisplay(msgs, 20, 200)

	if result.Total != 3 {
		t.Errorf("Expected Total=3, got %d", result.Total)
	}
	if result.Showing != 3 {
		t.Errorf("Expected Showing=3, got %d", result.Showing)
	}

	// Verify JSON serialization matches expected structure
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}
	var parsed rpc.MessagesResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}
	if parsed.Total != 3 {
		t.Errorf("Round-trip: Expected Total=3, got %d", parsed.Total)
	}
}
