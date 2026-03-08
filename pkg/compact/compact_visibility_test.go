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
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "first output"},
		}, false),
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

// TestCompactToolSummaryMessageRole verifies that the tool_summary message
// added by compactToolResultsInRecent has role "user", not "assistant".
// This is critical for API compatibility: when compact is called during
// tool execution (e.g., llm_context_decision), adding an assistant message
// would create consecutive assistant messages (tool_use followed by this message),
// which violates OpenAI API requirements.
func TestCompactToolSummaryMessageRole(t *testing.T) {
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "first output"},
		}, false),
		agentctx.NewToolResultMessage("call-2", "grep", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "second output"},
		}, false),
	}

	compacted := compactToolResultsInRecent(messages, 1)

	// Find the tool_summary message
	var summaryMsg *agentctx.AgentMessage
	for i := range compacted {
		if compacted[i].Metadata != nil && compacted[i].Metadata.Kind == "tool_summary" {
			summaryMsg = &compacted[i]
			break
		}
	}

	if summaryMsg == nil {
		t.Fatal("expected tool_summary message to be present")
	}

	// CRITICAL: The summary message must be "user" role, not "assistant".
	// This prevents consecutive assistant messages when compact is called
	// during tool execution (e.g., llm_context_decision tool).
	if summaryMsg.Role != "user" {
		t.Fatalf("expected tool_summary message to have role 'user' to avoid consecutive assistant messages, got role '%s'", summaryMsg.Role)
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
