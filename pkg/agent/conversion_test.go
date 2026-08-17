package agent

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestConvertLLMMessageToAgent_TextOnly(t *testing.T) {
	msg := llm.LLMMessage{Content: "hello world"}
	result := ConvertLLMMessageToAgent(msg)
	if result.Role != "assistant" {
		t.Errorf("Role = %q, want assistant", result.Role)
	}
	if len(result.Content) != 1 {
		t.Fatalf("Content len = %d, want 1", len(result.Content))
	}
	tc, ok := result.Content[0].(context.TextContent)
	if !ok {
		t.Fatalf("Content[0] is %T, want TextContent", result.Content[0])
	}
	if tc.Text != "hello world" {
		t.Errorf("Text = %q, want 'hello world'", tc.Text)
	}
}

func TestConvertLLMMessageToAgent_ThinkingAndText(t *testing.T) {
	msg := llm.LLMMessage{Thinking: "reasoning", Content: "answer"}
	result := ConvertLLMMessageToAgent(msg)
	if len(result.Content) != 2 {
		t.Fatalf("Content len = %d, want 2", len(result.Content))
	}
	thinking, ok := result.Content[0].(context.ThinkingContent)
	if !ok {
		t.Fatalf("Content[0] is %T, want ThinkingContent", result.Content[0])
	}
	if thinking.Thinking != "reasoning" {
		t.Errorf("Thinking = %q, want 'reasoning'", thinking.Thinking)
	}
	text, ok := result.Content[1].(context.TextContent)
	if !ok {
		t.Fatalf("Content[1] is %T, want TextContent", result.Content[1])
	}
	if text.Text != "answer" {
		t.Errorf("Text = %q, want 'answer'", text.Text)
	}
}

func TestConvertLLMMessageToAgent_ToolCalls(t *testing.T) {
	msg := llm.LLMMessage{
		Content: "using tool",
		ToolCalls: []llm.ToolCall{
			{
				ID: "call_1",
				Function: llm.FunctionCall{
					Name:      "bash",
					Arguments: `{"command":"ls"}`,
				},
			},
			{
				ID: "call_2",
				Function: llm.FunctionCall{
					Name:      "read",
					Arguments: `{"path":"/tmp/x"}`,
				},
			},
		},
	}
	result := ConvertLLMMessageToAgent(msg)
	if len(result.Content) != 3 {
		t.Fatalf("Content len = %d, want 3 (text + 2 toolCalls)", len(result.Content))
	}
	tc1, ok := result.Content[1].(context.ToolCallContent)
	if !ok {
		t.Fatalf("Content[1] is %T, want ToolCallContent", result.Content[1])
	}
	if tc1.Name != "bash" || tc1.ID != "call_1" {
		t.Errorf("ToolCall 1: name=%q id=%q, want bash/call_1", tc1.Name, tc1.ID)
	}
	if tc1.Arguments["command"] != "ls" {
		t.Errorf("ToolCall 1 args command = %v, want 'ls'", tc1.Arguments["command"])
	}
}

func TestConvertLLMMessageToAgent_InvalidArgsJSON(t *testing.T) {
	msg := llm.LLMMessage{
		ToolCalls: []llm.ToolCall{
			{
				ID: "call_bad",
				Function: llm.FunctionCall{
					Name:      "bash",
					Arguments: `not-json`,
				},
			},
		},
	}
	result := ConvertLLMMessageToAgent(msg)
	if len(result.Content) != 1 {
		t.Fatalf("Content len = %d, want 1", len(result.Content))
	}
	tc, ok := result.Content[0].(context.ToolCallContent)
	if !ok {
		t.Fatalf("Content[0] is %T, want ToolCallContent", result.Content[0])
	}
	if tc.Arguments == nil {
		t.Error("Arguments should not be nil even for invalid JSON")
	}
}

func TestConvertLLMMessageToAgent_EmptyMessage(t *testing.T) {
	msg := llm.LLMMessage{}
	result := ConvertLLMMessageToAgent(msg)
	if len(result.Content) != 0 {
		t.Errorf("Content len = %d, want 0 for empty message", len(result.Content))
	}
}
