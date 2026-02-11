package compact

import (
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestEstimateTokensSkipsAgentInvisibleMessages(t *testing.T) {
	compactor := NewCompactor(DefaultConfig(), llm.Model{}, "key", "sys", 0)

	visible := agent.NewUserMessage("short visible text")
	invisible := agent.NewUserMessage(strings.Repeat("X", 8000)).WithVisibility(false, true)

	withInvisible := []agent.AgentMessage{visible, invisible}
	withoutInvisible := []agent.AgentMessage{visible}

	tokensWithInvisible := compactor.EstimateTokens(withInvisible)
	tokensWithoutInvisible := compactor.EstimateTokens(withoutInvisible)
	if tokensWithInvisible != tokensWithoutInvisible {
		t.Fatalf("expected invisible messages to be ignored, got with=%d without=%d", tokensWithInvisible, tokensWithoutInvisible)
	}
}

func TestCompactToolResultsInRecent(t *testing.T) {
	messages := []agent.AgentMessage{
		agent.NewUserMessage("start"),
		agent.NewToolResultMessage("call-1", "read", []agent.ContentBlock{
			agent.TextContent{Type: "text", Text: "first output"},
		}, false),
		agent.NewToolResultMessage("call-2", "grep", []agent.ContentBlock{
			agent.TextContent{Type: "text", Text: "second output"},
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

	oldest := compacted[1]
	if oldest.IsAgentVisible() {
		t.Fatal("expected oldest tool result to be archived")
	}
	if oldest.Metadata == nil || oldest.Metadata.Kind != "tool_result_archived" {
		t.Fatalf("expected archived kind, got %+v", oldest.Metadata)
	}

	last := compacted[len(compacted)-1]
	if last.Metadata == nil || last.Metadata.Kind != "tool_summary" {
		t.Fatalf("expected tool summary message, got %+v", last.Metadata)
	}
	if !last.IsAgentVisible() || last.IsUserVisible() {
		t.Fatal("expected summary to be agent-visible and user-hidden")
	}
}

func TestProjectMessagesForSummaryTrimsToolOutputs(t *testing.T) {
	longText := strings.Repeat("a", 5000)
	messages := []agent.AgentMessage{
		agent.NewToolResultMessage("call-1", "bash", []agent.ContentBlock{
			agent.TextContent{Type: "text", Text: longText},
		}, false),
		agent.NewToolResultMessage("call-2", "grep", []agent.ContentBlock{
			agent.TextContent{Type: "text", Text: "hidden"},
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
