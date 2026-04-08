package agent

import (
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"
)

func TestHasSuccessfulLLMContextWrite(t *testing.T) {
	target := "/tmp/session/llm-context/overview.md"
	msg := agentctx.NewToolResultMessage(
		"call-1",
		"write",
		[]agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Successfully wrote 99 bytes to %s", target),
			},
		},
		false,
	)

	if !hasSuccessfulLLMContextWrite([]agentctx.AgentMessage{msg}, target) {
		t.Fatal("expected successful llm context write to be detected")
	}
	if hasSuccessfulLLMContextWrite([]agentctx.AgentMessage{msg}, "/tmp/another/overview.md") {
		t.Fatal("did not expect detection for a different target path")
	}
}

func TestSelectMessagesForLLM_UsesAllAvailableMessagesWhenLLMContextUnconfirmed(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	longPayload := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for i := 0; i < 700; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage(fmt.Sprintf("message-%03d %s %s %s %s", i, longPayload, longPayload, longPayload, longPayload)))
	}

	selected, mode := selectMessagesForLLM(agentCtx)
	if mode != "all_available_messages_no_runtime_clip" {
		t.Fatalf("expected all_available_messages_no_runtime_clip mode, got %q", mode)
	}
	if len(selected) == 0 {
		t.Fatal("expected selected messages to be non-empty")
	}
	if len(selected) != len(agentCtx.RecentMessages) {
		t.Fatalf("expected full history to be sent: selected=%d total=%d", len(selected), len(agentCtx.RecentMessages))
	}
}

func TestSelectMessagesForLLM_EmptyContext(t *testing.T) {
	selected, mode := selectMessagesForLLM(nil)
	if mode != "empty_context" {
		t.Fatalf("expected empty_context mode, got %q", mode)
	}
	if selected != nil {
		t.Fatalf("expected nil selected messages for empty context")
	}
}

func TestSelectMessagesForLLM_NoMessages(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	selected, mode := selectMessagesForLLM(agentCtx)
	if mode != "no_messages" {
		t.Fatalf("expected no_messages mode, got %q", mode)
	}
	if selected != nil {
		t.Fatalf("expected nil selected messages for no_messages mode")
	}
}

func TestSelectMessagesForLLM_UsesAllAvailableMessages(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	oldAssistant := agentctx.NewAssistantMessage()
	oldAssistant.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "old-assistant"}}
	agentCtx.RecentMessages = append(agentCtx.RecentMessages,
		agentctx.NewUserMessage("old-user"),
		oldAssistant,
		agentctx.NewUserMessage("new-user"),
	)

	selected, mode := selectMessagesForLLM(agentCtx)
	if mode != "all_available_messages_no_runtime_clip" {
		t.Fatalf("expected all_available_messages_no_runtime_clip mode, got %q", mode)
	}
	if len(selected) != len(agentCtx.RecentMessages) {
		t.Fatalf("expected full history in no-inject mode: selected=%d total=%d", len(selected), len(agentCtx.RecentMessages))
	}
	if selected[0].ExtractText() != "old-user" || selected[2].ExtractText() != "new-user" {
		t.Fatalf("expected original ordering to be preserved")
	}
}
