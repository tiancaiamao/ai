package agent

import (
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

	llmMessages := ConvertMessagesToLLM([]AgentMessage{visible, hidden, assistant})
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
