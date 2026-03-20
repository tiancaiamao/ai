package compact

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestEstimateTokensSkipsAgentInvisibleMessages(t *testing.T) {
	compactor := NewCompactor(DefaultConfig(), llm.Model{}, "key", "sys", 0)

	visible := agentctx.NewUserMessage("short visible text")
	invisible := agentctx.NewUserMessage(strings.Repeat("X", 8000)).WithVisibility(false, true)

	withInvisible := []agentctx.AgentMessage{visible, invisible}
	withoutInvisible := []agentctx.AgentMessage{visible}

	tokensWithInvisible := compactor.EstimateTokens(withInvisible)
	tokensWithoutInvisible := compactor.EstimateTokens(withoutInvisible)
	if tokensWithInvisible != tokensWithoutInvisible {
		t.Fatalf("expected invisible messages to be ignored, got with=%d without=%d", tokensWithInvisible, tokensWithoutInvisible)
	}
}

func TestCompactToolResultsInRecent(t *testing.T) {
	// Create messages with tool_calls (assistant) and tool_results
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		func() agentctx.AgentMessage {
			m := agentctx.NewAssistantMessage()
			m.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{ID: "call-1", Name: "read", Arguments: map[string]any{"path": "file1.txt"}},
			}
			return m
		}(),
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "first output"},
		}, false),
		func() agentctx.AgentMessage {
			m := agentctx.NewAssistantMessage()
			m.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{ID: "call-2", Name: "grep", Arguments: map[string]any{"pattern": "foo"}},
			}
			return m
		}(),
		agentctx.NewToolResultMessage("call-2", "grep", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "second output"},
		}, false),
	}

	compacted := compactToolResultsInRecent(messages, 1)

	visibleToolResults := 0
	for _, msg := range compacted {
		if msg.Role == "toolResult" && msg.IsAgentVisible() {
			visibleToolResults++
		}
	}
	if visibleToolResults != 1 {
		t.Fatalf("expected 1 visible tool result after compaction, got %d", visibleToolResults)
	}

	// The oldest tool_result (first one) should be archived
	oldestResultIndex := -1
	for i, msg := range compacted {
		if msg.Role == "toolResult" {
			oldestResultIndex = i
			break
		}
	}
	if oldestResultIndex < 0 {
		t.Fatal("expected to find tool_result messages")
	}
	oldest := compacted[oldestResultIndex]
	if oldest.IsAgentVisible() {
		t.Fatal("expected oldest tool result to be archived")
	}
	if oldest.Metadata == nil || oldest.Metadata.Kind != "tool_result_archived" {
		t.Fatalf("expected archived kind, got %+v", oldest.Metadata)
	}

	// Verify no tool_summary message was added
	for _, msg := range compacted {
		if msg.Metadata != nil && msg.Metadata.Kind == "tool_summary" {
			t.Fatal("expected no tool_summary message to be added")
		}
	}

	// Verify tool_call messages are filtered for archived tool_results
	// so assistant/tool protocol stays valid.
	toolCallCount := 0
	for _, msg := range compacted {
		if msg.Role == "assistant" {
			for _, block := range msg.Content {
				if _, ok := block.(agentctx.ToolCallContent); ok {
					toolCallCount++
				}
			}
		}
	}
	if toolCallCount != 1 {
		t.Fatalf("expected 1 remaining tool_call after filtering archived pair, got %d", toolCallCount)
	}
}

func TestProjectMessagesForSummaryTrimsToolOutputs(t *testing.T) {
	longText := strings.Repeat("a", 5000)
	messages := []agentctx.AgentMessage{
		agentctx.NewToolResultMessage("call-1", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: longText},
		}, false),
		agentctx.NewToolResultMessage("call-2", "grep", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "hidden"},
		}, false).WithVisibility(false, true),
	}

	projected := projectMessagesForSummary(messages)
	if len(projected) != 1 {
		t.Fatalf("expected only visible messages in projection, got %d", len(projected))
	}

	text := projected[0].ExtractText()
	if len([]rune(text)) > 1850 {
		t.Fatalf("expected trimmed tool output, got rune length %d", len([]rune(text)))
	}
	if !strings.Contains(text, "... (truncated) ...") {
		t.Fatalf("expected truncation marker, got %q", text)
	}
}
