package compact

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestGenerateCanaryValue verifies generated values are non-empty and unique.
func TestGenerateCanaryValue(t *testing.T) {
	v1 := generateCanaryValue()
	v2 := generateCanaryValue()

	if v1 == "" {
		t.Error("generated value should not be empty")
	}
	if v1 == v2 {
		t.Error("generated values should be unique")
	}
	if len(v1) != 12 {
		t.Errorf("expected 12 hex chars, got %d: %s", len(v1), v1)
	}
}

// TestInsertCanary verifies InsertCanary appends a canary message.
func TestInsertCanary(t *testing.T) {
	ctx := agentctx.NewAgentContext("test")
	for i := 0; i < 3; i++ {
		ctx.AddRecentMessage(agentctx.NewUserMessage("msg"))
	}

	v1 := InsertCanary(ctx)
	if v1 == "" {
		t.Fatal("expected non-empty canary value")
	}
	if len(ctx.RecentMessages) != 4 {
		t.Fatalf("expected 4 messages after insert, got %d", len(ctx.RecentMessages))
	}

	last := ctx.RecentMessages[len(ctx.RecentMessages)-1]
	if last.Metadata == nil || last.Metadata.Kind != CanaryKind {
		t.Errorf("last message should have canary kind")
	}
}

// TestInsertCanary_NoClean verifies InsertCanary does not remove previous
// canary messages (they are cleaned by Compact instead).
func TestInsertCanary_NoClean(t *testing.T) {
	ctx := agentctx.NewAgentContext("test")
	v1 := InsertCanary(ctx)

	// Insert again — old canary should remain.
	v2 := InsertCanary(ctx)

	if v1 == "" || v2 == "" || v1 == v2 {
		t.Errorf("expected two different canaries, got v1=%q v2=%q", v1, v2)
	}

	// Verify both canary messages exist in the list.
	count := 0
	for _, msg := range ctx.RecentMessages {
		if msg.Metadata != nil && msg.Metadata.Kind == CanaryKind {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 canary messages, got %d", count)
	}
}

// TestRemoveAllCanaries verifies all canary messages are removed.
func TestRemoveAllCanaries(t *testing.T) {
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("a"),
		agentctx.NewUserMessage("b").WithKind(CanaryKind),
		agentctx.NewUserMessage("c"),
		agentctx.NewUserMessage("d").WithKind(CanaryKind),
		agentctx.NewUserMessage("e"),
	}

	result := RemoveAllCanaries(messages)
	if len(result) != 3 {
		t.Errorf("expected 3 remaining, got %d", len(result))
	}
	for _, msg := range result {
		if msg.Metadata != nil && msg.Metadata.Kind == CanaryKind {
			t.Error("canary message was not removed")
		}
	}
}

// TestRemoveAllCanaries_Nil handles nil/empty input.
func TestRemoveAllCanaries_Nil(t *testing.T) {
	if result := RemoveAllCanaries(nil); result != nil {
		t.Errorf("expected nil, got %v", result)
	}
	if result := RemoveAllCanaries([]agentctx.AgentMessage{}); len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

// TestCanaryLifecycle verifies the full lifecycle:
// no canary → planted after compact → found in messages → removed on next
// compact. The planted message is the only record of the value, so removal and
// lookup cannot drift apart.
func TestCanaryLifecycle(t *testing.T) {
	ctx := agentctx.NewAgentContext("test")
	for i := 0; i < 5; i++ {
		ctx.AddRecentMessage(agentctx.NewUserMessage("msg"))
	}

	if got := FindCanaryValue(ctx.RecentMessages); got != "" {
		t.Fatalf("FindCanaryValue before planting = %q, want empty", got)
	}

	// Simulate planting after compaction.
	val := InsertCanary(ctx)
	if val == "" {
		t.Fatal("expected non-empty canary value")
	}
	if got := FindCanaryValue(ctx.RecentMessages); got != val {
		t.Errorf("FindCanaryValue after planting = %q, want %q", got, val)
	}

	// Simulate compaction.
	ctx.RecentMessages = RemoveAllCanaries(ctx.RecentMessages)

	for _, msg := range ctx.RecentMessages {
		if msg.Metadata != nil && msg.Metadata.Kind == CanaryKind {
			t.Error("canary should be removed after compaction")
		}
	}
	if got := FindCanaryValue(ctx.RecentMessages); got != "" {
		t.Errorf("FindCanaryValue after removal = %q, want empty", got)
	}
}

// TestFindCanaryValue_LastWins verifies that the most recent canary is used
// when several are present (e.g. two compactions without a cleanup in between).
func TestFindCanaryValue_LastWins(t *testing.T) {
	ctx := agentctx.NewAgentContext("test")
	first := InsertCanary(ctx)
	second := InsertCanary(ctx)
	if got := FindCanaryValue(ctx.RecentMessages); got != second {
		t.Errorf("FindCanaryValue = %q, want newest %q (first was %q)", got, second, first)
	}
}

// TestParseCanaryValue covers the message body format written by InsertCanary.
func TestParseCanaryValue(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{`<agent:canary value="abc123"/>`, "abc123"},
		{`wrapped: <agent:canary value="abc123"/> trailing`, "abc123"},
		{`<agent:canary value=""/>`, ""},
		{`<agent:canary/>`, ""},
		{`no canary here`, ""},
	}
	for _, tt := range tests {
		if got := parseCanaryValue(tt.content); got != tt.want {
			t.Errorf("parseCanaryValue(%q) = %q, want %q", tt.content, got, tt.want)
		}
	}
}
