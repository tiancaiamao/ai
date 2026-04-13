package compact

import (
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func makeToolResult(toolCallID string, size int) agentctx.AgentMessage {
	return agentctx.NewToolResultMessage(
		toolCallID,
		"bash",
		[]agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: strings.Repeat("x", size),
			},
		},
		false,
	)
}

func TestCollectTruncationCandidatesFiltersBySelectability(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.RecentMessages = []agentctx.AgentMessage{
		makeToolResult("call-selectable", 5000),
		makeToolResult("", 5000), // non-selectable (missing tool_call_id)
		func() agentctx.AgentMessage {
			msg := makeToolResult("call-truncated", 5000)
			msg.Truncated = true
			return msg
		}(),
		agentctx.NewUserMessage("recent-1"),
		agentctx.NewUserMessage("recent-2"),
		agentctx.NewUserMessage("recent-3"),
		agentctx.NewUserMessage("recent-4"),
		agentctx.NewUserMessage("recent-5"),
	}

	protectedStart := len(agentCtx.RecentMessages) - agentctx.RecentMessagesKeep
	candidates, truncatedCount, nonSelectableCount := collectTruncationCandidates(agentCtx, protectedStart)

	if len(candidates) != 1 {
		t.Fatalf("expected 1 truncation candidate, got %d", len(candidates))
	}
	if candidates[0].ID != "call-selectable" {
		t.Fatalf("unexpected candidate id: %s", candidates[0].ID)
	}
	if truncatedCount != 1 {
		t.Fatalf("expected truncated count 1, got %d", truncatedCount)
	}
	if nonSelectableCount != 1 {
		t.Fatalf("expected non-selectable count 1, got %d", nonSelectableCount)
	}
}

func TestBuildContextMgmtMessagesExposesSavingsAndGuidance(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.LLMContext = "existing context"
	agentCtx.RecentMessages = []agentctx.AgentMessage{
		makeToolResult("call-a", 12000),
		makeToolResult("call-b", 12000),
		makeToolResult("call-c", 12000),
		makeToolResult("", 3000), // shown as NON_TRUNCATABLE:NO_ID
		agentctx.NewUserMessage("recent-1"),
		agentctx.NewUserMessage("recent-2"),
		agentctx.NewUserMessage("recent-3"),
		agentctx.NewUserMessage("recent-4"),
		agentctx.NewUserMessage("recent-5"),
	}

	compactor := NewLLMMiniCompactor(DefaultLLMMiniCompactorConfig(), llmModelStub(), "", 200000, "system", nil)
	msgs := compactor.buildContextMgmtMessages(agentCtx)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 mini-compact messages, got %d", len(msgs))
	}

	history := msgs[0].Content
	state := msgs[1].Content

	if !strings.Contains(history, "NON_TRUNCATABLE:NO_ID") {
		t.Fatalf("expected NON_TRUNCATABLE marker in history message, got: %s", history)
	}
	if !strings.Contains(state, "Estimated savings if truncating selectable outputs:") {
		t.Fatalf("expected estimated savings in state message, got: %s", state)
	}
	if !strings.Contains(state, "force_truncate_recommended=true") {
		t.Fatalf("expected force_truncate_recommended=true, got: %s", state)
	}
	if !strings.Contains(state, "Truncatable tool outputs (selectable): 3") {
		t.Fatalf("expected selectable truncatable count in state message, got: %s", state)
	}
}

func llmModelStub() llm.Model {
	return llm.Model{
		ID:            "stub-model",
		ContextWindow: 200000,
	}
}
