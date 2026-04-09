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
	// This test verifies basic conversion of tool results
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{ID: "call-1", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "a.go"}},
	}

	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("do work"),
		assistant,
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "output"},
		}, false),
	}

	llmMessages := ConvertMessagesToLLM(context.Background(), msgs)
	// Without deduplication, all 3 messages should pass through
	if len(llmMessages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(llmMessages))
	}
	// Verify tool result message
	if llmMessages[2].Role != "tool" || llmMessages[2].ToolCallID != "call-1" {
		t.Fatalf("expected third message to be tool result for call-1")
	}
}

func TestConvertMessagesToLLMDedupesToolSummaryByContent(t *testing.T) {
	// This test verifies that duplicate tool summaries are removed
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
	// Without deduplication, all 3 messages pass through
	if len(llmMessages) != 3 {
		t.Fatalf("expected 3 messages (no longer deduplicated), got %d", len(llmMessages))
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
		agentctx.ToolCallContent{ID: "call-4", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "a.go"}},
		agentctx.ToolCallContent{ID: "call-5", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "echo two"}},
	}

	llmMessages := ConvertMessagesToLLM(context.Background(), []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		a1,
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "read output 1"},
		}, false),
		agentctx.NewToolResultMessage("call-2", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "bash output 1"},
		}, false),
		a2,
		agentctx.NewToolResultMessage("call-4", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "read output 2"},
		}, false),
		agentctx.NewToolResultMessage("call-5", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "bash output 2"},
		}, false),
	})

	assistantWithTools := 0
	for _, msg := range llmMessages {
		if msg.Role == "assistant" && len(msg.ToolCalls) == 2 {
			assistantWithTools++
		}
	}
	if assistantWithTools != 2 {
		t.Fatalf("expected both distinct assistant tool-call sets to be kept, got %d", assistantWithTools)
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

func TestConvertMessagesToLLMStripsDanglingAssistantToolCalls(t *testing.T) {
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "planning"},
		agentctx.ToolCallContent{
			ID:        "call-hidden",
			Type:      "toolCall",
			Name:      "bash",
			Arguments: map[string]any{"command": "echo hidden"},
		},
	}

	hiddenTool := agentctx.NewToolResultMessage(
		"call-hidden",
		"bash",
		[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hidden output"}},
		false,
	).WithVisibility(false, true)

	llmMessages := ConvertMessagesToLLM(context.Background(), []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		assistant,
		hiddenTool,
		agentctx.NewUserMessage("next"),
	})

	assertNoOrphanedToolProtocol(t, llmMessages)
	for _, msg := range llmMessages {
		if msg.Role != "assistant" {
			continue
		}
		if strings.TrimSpace(msg.Content) == "planning" && len(msg.ToolCalls) != 0 {
			t.Fatalf("expected dangling tool calls to be removed, got %d", len(msg.ToolCalls))
		}
	}
}

func TestConvertMessagesToLLMDropsOrphanedToolResult(t *testing.T) {
	llmMessages := ConvertMessagesToLLM(context.Background(), []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		agentctx.NewToolResultMessage(
			"call-orphan",
			"read",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "orphan"}},
			false,
		),
		agentctx.NewUserMessage("next"),
	})

	assertNoOrphanedToolProtocol(t, llmMessages)
	for _, msg := range llmMessages {
		if msg.Role == "tool" {
			t.Fatalf("expected orphan tool messages to be dropped, got %+v", msg)
		}
	}
}

func TestConvertMessagesToLLMRetainsResolvedToolCallsWhenPartiallyMatched(t *testing.T) {
	assistant := agentctx.NewAssistantMessage()
	assistant.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:        "call-keep",
			Type:      "toolCall",
			Name:      "read",
			Arguments: map[string]any{"path": "a.txt"},
		},
		agentctx.ToolCallContent{
			ID:        "call-drop",
			Type:      "toolCall",
			Name:      "bash",
			Arguments: map[string]any{"command": "echo drop"},
		},
	}

	visibleTool := agentctx.NewToolResultMessage(
		"call-keep",
		"read",
		[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "kept"}},
		false,
	)
	hiddenTool := agentctx.NewToolResultMessage(
		"call-drop",
		"bash",
		[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "dropped"}},
		false,
	).WithVisibility(false, true)

	llmMessages := ConvertMessagesToLLM(context.Background(), []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		assistant,
		visibleTool,
		hiddenTool,
		agentctx.NewUserMessage("next"),
	})

	assertNoOrphanedToolProtocol(t, llmMessages)

	var keptAssistant *llm.LLMMessage
	toolCount := 0
	for i := range llmMessages {
		if llmMessages[i].Role == "assistant" && len(llmMessages[i].ToolCalls) > 0 {
			keptAssistant = &llmMessages[i]
		}
		if llmMessages[i].Role == "tool" {
			toolCount++
		}
	}
	if keptAssistant == nil {
		t.Fatal("expected assistant with resolved tool call to be kept")
	}
	if len(keptAssistant.ToolCalls) != 1 || keptAssistant.ToolCalls[0].ID != "call-keep" {
		t.Fatalf("expected only resolved tool call to remain, got %+v", keptAssistant.ToolCalls)
	}
	if toolCount != 1 {
		t.Fatalf("expected exactly one tool result to remain, got %d", toolCount)
	}
}

func assertNoOrphanedToolProtocol(t *testing.T, messages []llm.LLMMessage) {
	t.Helper()
	pending := map[string]struct{}{}
	for i, msg := range messages {
		switch msg.Role {
		case "assistant":
			if len(pending) > 0 {
				t.Fatalf("assistant at %d appeared before pending tools resolved: %+v", i, pending)
			}
			for _, tc := range msg.ToolCalls {
				pending[tc.ID] = struct{}{}
			}
		case "tool":
			if len(pending) == 0 {
				t.Fatalf("orphaned tool message at %d: %+v", i, msg)
			}
			if _, ok := pending[msg.ToolCallID]; !ok {
				t.Fatalf("tool message at %d has unknown toolCallID=%q pending=%+v", i, msg.ToolCallID, pending)
			}
			delete(pending, msg.ToolCallID)
		default:
			if len(pending) > 0 {
				t.Fatalf("non-tool role=%q at %d before pending tools resolved: %+v", msg.Role, i, pending)
			}
		}
	}
	if len(pending) > 0 {
		t.Fatalf("unresolved pending tool calls at end: %+v", pending)
	}
}
