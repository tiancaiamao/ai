package compactcase

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestInputCommitHashLooksLikeMainCommit(t *testing.T) {
	data, err := os.ReadFile("main_commit.txt")
	if err != nil {
		t.Fatalf("read main_commit.txt: %v", err)
	}
	hash := strings.TrimSpace(string(data))
	if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(hash) {
		t.Fatalf("invalid commit hash format: %q", hash)
	}
}

func TestTraceShowsCompactThenToolCallMismatch(t *testing.T) {
	ok, err := traceShowsCompactThenMismatch("trace/main.perfetto.json")
	if err != nil {
		t.Fatalf("parse trace: %v", err)
	}
	if !ok {
		t.Fatal("expected trace to contain compact decision followed by tool call mismatch error")
	}
}

func TestEnsureToolCallPairingFiltersAssistantToolCalls(t *testing.T) {
	oldMessages := []Message{
		NewAssistantMessage(ToolCall("call-old-1", "read", map[string]any{"path": "/tmp/old.txt"})),
	}
	recentMessages := []Message{
		NewAssistantMessage(
			Text("I found this in logs:"),
			ToolCall("call-old-1", "read", map[string]any{"path": "/tmp/old.txt"}),
		),
		NewToolResultMessage("call-old-1", "read", "old content"),
	}

	got := ensureToolCallPairing(oldMessages, recentMessages)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}

	assistant := got[0]
	if assistant.Role != "assistant" {
		t.Fatalf("expected first message assistant, got %s", assistant.Role)
	}
	if len(assistant.ExtractToolCalls()) != 0 {
		t.Fatal("assistant message should not keep old tool calls after pairing")
	}
	hasText := false
	for _, block := range assistant.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			hasText = true
		}
	}
	if !hasText {
		t.Fatal("assistant text content should be preserved after filtering tool calls")
	}

	toolResult := got[1]
	if toolResult.Role != "toolResult" {
		t.Fatalf("expected second message toolResult, got %s", toolResult.Role)
	}
	if toolResult.AgentVisible {
		t.Fatal("tool result should be hidden when its tool call was compacted")
	}
}
