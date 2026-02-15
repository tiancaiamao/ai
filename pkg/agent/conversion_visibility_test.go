package agent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestConvertMessagesToLLMFiltersAgentInvisible(t *testing.T) {
	visible := NewUserMessage("visible")
	hidden := NewUserMessage("hidden").WithVisibility(false, true)
	assistant := NewAssistantMessage()
	assistant.Content = []ContentBlock{
		TextContent{Type: "text", Text: "ok"},
	}

	llmMessages := ConvertMessagesToLLM(context.Background(), []AgentMessage{visible, hidden, assistant})
	if len(llmMessages) != 2 {
		t.Fatalf("expected 2 LLM messages, got %d", len(llmMessages))
	}
	if llmMessages[0].Content != "visible" {
		t.Fatalf("unexpected first content: %q", llmMessages[0].Content)
	}
	if llmMessages[1].Content != "ok" {
		t.Fatalf("unexpected second content: %q", llmMessages[1].Content)
	}
}

func TestConvertToolsToLLMDeduplicatesByName(t *testing.T) {
	t1 := &mockTool{name: "read"}
	t2 := &mockTool{name: "bash"}
	t3 := &mockTool{name: "read"} // duplicate name

	tools := ConvertToolsToLLM(context.Background(), []Tool{t1, t2, t3})
	if len(tools) != 2 {
		t.Fatalf("expected 2 unique tools, got %d", len(tools))
	}
	if tools[0].Function.Name != "read" {
		t.Fatalf("expected first tool read, got %q", tools[0].Function.Name)
	}
	if tools[1].Function.Name != "bash" {
		t.Fatalf("expected second tool bash, got %q", tools[1].Function.Name)
	}
}

func TestAgentMessageMetadataRoundTrip(t *testing.T) {
	msg := NewUserMessage("hello").WithVisibility(false, true).WithKind("tool_summary")

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded AgentMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.IsAgentVisible() {
		t.Fatal("expected agentVisible=false after round-trip")
	}
	if !decoded.IsUserVisible() {
		t.Fatal("expected userVisible=true after round-trip")
	}
	if decoded.Metadata == nil || decoded.Metadata.Kind != "tool_summary" {
		t.Fatalf("expected kind to round-trip, got %+v", decoded.Metadata)
	}
}

func TestConvertMessagesToLLMDedupesToolResultsByCallID(t *testing.T) {
	msgs := []AgentMessage{
		NewUserMessage("do work"),
		NewToolResultMessage("call-1", "read", []ContentBlock{
			TextContent{Type: "text", Text: "old output"},
		}, false),
		NewToolResultMessage("call-1", "read", []ContentBlock{
			TextContent{Type: "text", Text: "new output"},
		}, false),
	}

	llmMessages := ConvertMessagesToLLM(context.Background(), msgs)
	if len(llmMessages) != 2 {
		t.Fatalf("expected 2 messages after dedupe, got %d", len(llmMessages))
	}
	if llmMessages[1].Role != "tool" {
		t.Fatalf("expected second message role=tool, got %q", llmMessages[1].Role)
	}
	if llmMessages[1].ToolCallID != "call-1" {
		t.Fatalf("expected toolCallID call-1, got %q", llmMessages[1].ToolCallID)
	}
	if llmMessages[1].Content != "new output" {
		t.Fatalf("expected newest tool output to be kept, got %q", llmMessages[1].Content)
	}
}

func TestConvertMessagesToLLMDedupesToolSummaryByContent(t *testing.T) {
	summaryA := NewAssistantMessage()
	summaryA.Content = []ContentBlock{TextContent{Type: "text", Text: "summary text"}}
	summaryA = summaryA.WithVisibility(true, false).WithKind("tool_summary")

	summaryB := NewAssistantMessage()
	summaryB.Content = []ContentBlock{TextContent{Type: "text", Text: "summary text"}}
	summaryB = summaryB.WithVisibility(true, false).WithKind("tool_summary")

	llmMessages := ConvertMessagesToLLM(context.Background(), []AgentMessage{
		NewUserMessage("start"),
		summaryA,
		summaryB,
	})

	if len(llmMessages) != 2 {
		t.Fatalf("expected deduped summary messages, got %d entries", len(llmMessages))
	}
	if llmMessages[1].Role != "assistant" {
		t.Fatalf("expected deduped summary as assistant role, got %q", llmMessages[1].Role)
	}
	if llmMessages[1].Content != "summary text" {
		t.Fatalf("unexpected summary content: %q", llmMessages[1].Content)
	}
}
