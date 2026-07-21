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
	InsertCanary(ctx)
	v1 := FindCanaryValue(ctx.RecentMessages)

	// Insert again — old canary should remain.
	InsertCanary(ctx)
	v2 := FindCanaryValue(ctx.RecentMessages)

	// Both canary values should be findable (FindCanaryValue returns newest).
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

// TestFindCanaryValue verifies FindCanaryValue finds the most recent canary.
func TestFindCanaryValue(t *testing.T) {
	ctx := agentctx.NewAgentContext("test")

	if v := FindCanaryValue(ctx.RecentMessages); v != "" {
		t.Errorf("expected empty, got %q", v)
	}

	v1 := InsertCanary(ctx)
	found := FindCanaryValue(ctx.RecentMessages)
	if found != v1 {
		t.Errorf("expected %q, got %q", v1, found)
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

// TestCompactorCanaryLifecycle verifies the full lifecycle:
// no canary → planted after compact → removed on next compact.
func TestCompactorCanaryLifecycle(t *testing.T) {
	c := &Compactor{canaryValue: ""}
	ctx := agentctx.NewAgentContext("test")
	for i := 0; i < 5; i++ {
		ctx.AddRecentMessage(agentctx.NewUserMessage("msg"))
	}

	// Simulate planting after compaction.
	val := InsertCanary(ctx)
	c.canaryValue = val

	if c.canaryValue == "" {
		t.Fatal("expected non-empty canary value")
	}
	if FindCanaryValue(ctx.RecentMessages) != val {
		t.Error("canary should be in RecentMessages")
	}

	// Simulate compaction.
	ctx.RecentMessages = RemoveAllCanaries(ctx.RecentMessages)
	c.canaryValue = ""

	if FindCanaryValue(ctx.RecentMessages) != "" {
		t.Error("canary should be removed after compaction")
	}
	if c.canaryValue != "" {
		t.Error("tracked canary should be reset after compaction")
	}
}
