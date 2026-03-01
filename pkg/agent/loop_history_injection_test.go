package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"fmt"
	"os"
	"testing"
)

func TestShouldInjectHistory_ForcedByConfig(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.LLMContext = agentctx.NewLLMContext(t.TempDir())

	inject, reason := shouldInjectHistory(agentCtx, &LoopConfig{InjectHistory: true})
	if !inject {
		t.Fatalf("expected forced history injection, got inject=%v reason=%q", inject, reason)
	}
	if reason != "inject_history_forced" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestShouldInjectHistory_WhenLLMContextNotMaintained(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewLLMContext(sessionDir)

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.LLMContext = wm

	inject, reason := shouldInjectHistory(agentCtx, &LoopConfig{})
	if !inject {
		t.Fatalf("expected history injection before llm context is maintained, reason=%q", reason)
	}
	if reason != "llm_context_not_maintained" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestShouldInjectHistory_StripsAfterLLMContextConfirmed(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewLLMContext(sessionDir)
	if _, err := wm.Load(); err != nil {
		t.Fatalf("failed to initialize llm context: %v", err)
	}

	overviewPath := wm.GetPath()
	content := "# LLM Context\n\n## 当前任务\n- 完成 history gate\n"
	if err := os.WriteFile(overviewPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to update overview: %v", err)
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.LLMContext = wm

	inject, reason := shouldInjectHistory(agentCtx, &LoopConfig{})
	if inject {
		t.Fatalf("expected history stripping after llm context confirmation, reason=%q", reason)
	}
	if reason != "llm_context_content_confirmed" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

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

func TestSelectMessagesForLLM_UsesRecentWindowWhenLLMContextUnconfirmed(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	longPayload := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for i := 0; i < 700; i++ {
		agentCtx.Messages = append(agentCtx.Messages, agentctx.NewUserMessage(fmt.Sprintf("message-%03d %s %s %s %s", i, longPayload, longPayload, longPayload, longPayload)))
	}

	selected, mode := selectMessagesForLLM(agentCtx, true, "llm_context_not_maintained", 128000)
	if mode != "recent_history_window" {
		t.Fatalf("expected recent_history_window mode, got %q", mode)
	}
	if len(selected) == 0 {
		t.Fatal("expected selected messages to be non-empty")
	}
	if len(selected) >= len(agentCtx.Messages) {
		t.Fatalf("expected selected window smaller than full history: selected=%d total=%d", len(selected), len(agentCtx.Messages))
	}
}

func TestSelectMessagesForLLM_ForcedKeepsFullHistory(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	for i := 0; i < 50; i++ {
		agentCtx.Messages = append(agentCtx.Messages, agentctx.NewUserMessage(fmt.Sprintf("forced-%03d", i)))
	}

	selected, mode := selectMessagesForLLM(agentCtx, true, "inject_history_forced", 128000)
	if mode != "full_history_forced" {
		t.Fatalf("expected full_history_forced mode, got %q", mode)
	}
	if len(selected) != len(agentCtx.Messages) {
		t.Fatalf("expected full history in forced mode: selected=%d total=%d", len(selected), len(agentCtx.Messages))
	}
}

func TestSelectMessagesForLLM_NoInjectKeepsRecentHistoryWindow(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	oldAssistant := agentctx.NewAssistantMessage()
	oldAssistant.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "old-assistant"}}
	agentCtx.Messages = append(agentCtx.Messages,
		agentctx.NewUserMessage("old-user"),
		oldAssistant,
		agentctx.NewUserMessage("new-user"),
	)

	selected, mode := selectMessagesForLLM(agentCtx, false, "llm_context_content_confirmed", 128000)
	if mode != "recent_history_window_no_inject" {
		t.Fatalf("expected recent_history_window_no_inject mode, got %q", mode)
	}
	if len(selected) < 2 {
		t.Fatalf("expected recent history window to keep prior context, got %d message(s)", len(selected))
	}
	if selected[0].ExtractText() != "old-user" {
		t.Fatalf("expected oldest selected message to preserve history, got %q", selected[0].ExtractText())
	}
}
