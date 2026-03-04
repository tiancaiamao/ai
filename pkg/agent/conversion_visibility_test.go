package agent

import (
	"context"
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestConvertMessagesToLLMFiltersAgentInvisible(t *testing.T) {
	visible := agentctx.NewUserMessage("visible")
	hidden := agentctx.NewUserMessage("hidden").WithVisibility(false, true)
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "ok"},
	}

	llmMessages := ConvertMessagesToLLM(context.Background(), []agentctx.AgentMessage{visible, hidden, assistant})
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

	tools := ConvertToolsToLLM(context.Background(), []agentctx.Tool{t1, t2, t3})
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
	msg := agentctx.NewUserMessage("hello").WithVisibility(false, true).WithKind("tool_summary")

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded agentctx.AgentMessage
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
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("do work"),
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "old output"},
		}, false),
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "new output"},
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
	summaryA := agentctx.NewAssistantMessage()
	summaryA.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "summary text"}}
	summaryA = summaryA.WithVisibility(true, false).WithKind("tool_summary")

	summaryB := agentctx.NewAssistantMessage()
	summaryB.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "summary text"}}
	summaryB = summaryB.WithVisibility(true, false).WithKind("tool_summary")

	llmMessages := ConvertMessagesToLLM(context.Background(), []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
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

func TestConvertMessagesToLLMDedupesAssistantToolCallsByFullSet(t *testing.T) {
	a1 := agentctx.NewAssistantMessage()
	a1.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{ID: "call-1", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "a.go"}},
		agentctx.ToolCallContent{ID: "call-2", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "echo hi"}},
	}
	a2 := agentctx.NewAssistantMessage()
	a2.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{ID: "call-1", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "a.go"}},
		agentctx.ToolCallContent{ID: "call-2", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "echo hi"}},
	}

	llmMessages := ConvertMessagesToLLM(context.Background(), []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		a1,
		a2,
	})
	if len(llmMessages) != 2 {
		t.Fatalf("expected duplicate assistant tool-call set to dedupe, got %d", len(llmMessages))
	}
	if len(llmMessages[1].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls after dedupe, got %d", len(llmMessages[1].ToolCalls))
	}
}

func TestConvertMessagesToLLMKeepsAssistantToolCallsWhenSetDiffers(t *testing.T) {
	a1 := agentctx.NewAssistantMessage()
	a1.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{ID: "call-1", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "a.go"}},
		agentctx.ToolCallContent{ID: "call-2", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "echo one"}},
	}
	a2 := agentctx.NewAssistantMessage()
	a2.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{ID: "call-1", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "a.go"}},
		agentctx.ToolCallContent{ID: "call-3", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "echo two"}},
	}

	llmMessages := ConvertMessagesToLLM(context.Background(), []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		a1,
		a2,
	})
	if len(llmMessages) != 3 {
		t.Fatalf("expected both assistant tool-call sets to be kept, got %d", len(llmMessages))
	}
}

func TestConvertMessagesToLLMInjectsStaleToolMetadataBeyondRecent10(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("old turn"),
	}
	for i := 1; i <= 11; i++ {
		msgs = append(msgs, agentctx.NewToolResultMessage(
			fmt.Sprintf("call-%d", i),
			"read",
			[]agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("payload-%d", i)},
			},
			false,
		))
	}
	msgs = append(msgs, agentctx.NewUserMessage("latest turn"))

	llmMessages := ConvertMessagesToLLM(context.Background(), msgs)

	var firstTool, latestTool llm.LLMMessage
	for _, m := range llmMessages {
		if m.Role != "tool" {
			continue
		}
		if m.ToolCallID == "call-1" {
			firstTool = m
		}
		if m.ToolCallID == "call-11" {
			latestTool = m
		}
	}

	if !strings.Contains(firstTool.Content, `stale="`) {
		t.Fatalf("expected call-1 to include stale metadata tag, got %q", firstTool.Content)
	}
	if strings.Contains(latestTool.Content, `stale="`) {
		t.Fatalf("expected recent tool output to remain untagged, got %q", latestTool.Content)
	}
}

func TestConvertMessagesToLLMDoesNotInjectMetadataForRecent10ToolOutputs(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("old turn"),
	}
	for i := 1; i <= 10; i++ {
		msgs = append(msgs, agentctx.NewToolResultMessage(
			fmt.Sprintf("call-%d", i),
			"bash",
			[]agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("out-%d", i)},
			},
			false,
		))
	}
	msgs = append(msgs, agentctx.NewUserMessage("latest turn"))

	llmMessages := ConvertMessagesToLLM(context.Background(), msgs)
	for _, m := range llmMessages {
		if m.Role != "tool" {
			continue
		}
		if strings.Contains(m.Content, `stale="true"`) {
			t.Fatalf("expected no stale metadata within recent 10 tool outputs, got %q", m.Content)
		}
	}
}
