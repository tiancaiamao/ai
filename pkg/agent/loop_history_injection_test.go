package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"fmt"
	"os"
	"testing"
)

func TestShouldInjectHistory_ForcedByConfig(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.WorkingMemory = agentctx.NewWorkingMemory(t.TempDir())

	inject, reason := shouldInjectHistory(agentCtx, &LoopConfig{InjectHistory: true})
	if !inject {
		t.Fatalf("expected forced history injection, got inject=%v reason=%q", inject, reason)
	}
	if reason != "inject_history_forced" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestShouldInjectHistory_WhenWorkingMemoryNotMaintained(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewWorkingMemory(sessionDir)

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.WorkingMemory = wm

	inject, reason := shouldInjectHistory(agentCtx, &LoopConfig{})
	if !inject {
		t.Fatalf("expected history injection before working memory is maintained, reason=%q", reason)
	}
	if reason != "working_memory_not_maintained" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestShouldInjectHistory_StripsAfterWorkingMemoryConfirmed(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewWorkingMemory(sessionDir)
	if _, err := wm.Load(); err != nil {
		t.Fatalf("failed to initialize working memory: %v", err)
	}

	overviewPath := wm.GetPath()
	content := "# Working Memory\n\n## 当前任务\n- 完成 history gate\n"
	if err := os.WriteFile(overviewPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to update overview: %v", err)
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.WorkingMemory = wm

	inject, reason := shouldInjectHistory(agentCtx, &LoopConfig{})
	if inject {
		t.Fatalf("expected history stripping after working memory confirmation, reason=%q", reason)
	}
	if reason != "working_memory_content_confirmed" {
		t.Fatalf("unexpected reason: %q", reason)
	}
}

func TestHasSuccessfulWorkingMemoryWrite(t *testing.T) {
	target := "/tmp/session/working-memory/overview.md"
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

	if !hasSuccessfulWorkingMemoryWrite([]agentctx.AgentMessage{msg}, target) {
		t.Fatal("expected successful working memory write to be detected")
	}
	if hasSuccessfulWorkingMemoryWrite([]agentctx.AgentMessage{msg}, "/tmp/another/overview.md") {
		t.Fatal("did not expect detection for a different target path")
	}
}

func TestSelectMessagesForLLM_UsesRecentWindowWhenWorkingMemoryUnconfirmed(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	longPayload := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for i := 0; i < 700; i++ {
		agentCtx.Messages = append(agentCtx.Messages, agentctx.NewUserMessage(fmt.Sprintf("message-%03d %s %s %s %s", i, longPayload, longPayload, longPayload, longPayload)))
	}

	selected, mode := selectMessagesForLLM(agentCtx, true, "working_memory_not_maintained", 128000)
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

	selected, mode := selectMessagesForLLM(agentCtx, false, "working_memory_content_confirmed", 128000)
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
