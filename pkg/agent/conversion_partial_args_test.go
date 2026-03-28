package agent

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestConvertLLMMessageToAgent_ParsesTruncatedToolArgs(t *testing.T) {
	msg := llm.LLMMessage{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: llm.FunctionCall{
					Name:      "write",
					Arguments: `{"path":"index.html","content":"<!DOCTYPE html>`,
				},
			},
		},
	}

	agentMsg := ConvertLLMMessageToAgent(msg)
	calls := agentMsg.ExtractToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if got := calls[0].Arguments["path"]; got != "index.html" {
		t.Fatalf("path parse failed: %#v", calls[0].Arguments)
	}
	if got := calls[0].Arguments["content"]; got != "<!DOCTYPE html>" {
		t.Fatalf("content parse failed: %#v", calls[0].Arguments)
	}
}

func TestConvertLLMMessageToAgent_ParsesNestedProperties(t *testing.T) {
	msg := llm.LLMMessage{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{
			{
				ID:   "call_2",
				Type: "function",
				Function: llm.FunctionCall{
					Name:      "bash",
					Arguments: `{"properties":"{\"command\":\"echo hi\"}"}`,
				},
			},
		},
	}

	agentMsg := ConvertLLMMessageToAgent(msg)
	calls := agentMsg.ExtractToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if _, has := calls[0].Arguments["properties"]; has {
		t.Fatalf("unexpected properties wrapper remained: %#v", calls[0].Arguments)
	}
	if got := calls[0].Arguments["command"]; got != "echo hi" {
		t.Fatalf("command parse failed: %#v", calls[0].Arguments)
	}
}
