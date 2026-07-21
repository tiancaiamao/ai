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

// TestAppendCanary verifies AppendCanary appends a canary message and
// removes any existing ones.
func TestAppendCanary(t *testing.T) {
	ctx := agentctx.NewAgentContext("test")
	for i := 0; i < 3; i++ {
		ctx.AddRecentMessage(agentctx.NewUserMessage("msg"))
	}

	// First append.
	v1 := AppendCanary(ctx)
	if v1 == "" {
		t.Fatal("expected non-empty canary value")
	}

	// Should have 4 messages (3 original + 1 canary).
	if len(ctx.RecentMessages) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(ctx.RecentMessages))
	}

	// Verify kind on the last message.
	last := ctx.RecentMessages[len(ctx.RecentMessages)-1]
	if last.Metadata == nil || last.Metadata.Kind != CanaryKind {
		t.Errorf("last message should have canary kind")
	}

	// Second append — should remove old canary and add new one.
	v2 := AppendCanary(ctx)
	if v2 == v1 {
		t.Error("expected different canary values")
	}

	// Should still have 4 messages (3 original + 1 new canary).
	if len(ctx.RecentMessages) != 4 {
		t.Fatalf("expected 4 messages after second append, got %d", len(ctx.RecentMessages))
	}

	// Verify only one canary remains.
	canaryCount := 0
	for _, msg := range ctx.RecentMessages {
		if msg.Metadata != nil && msg.Metadata.Kind == CanaryKind {
			canaryCount++
		}
	}
	if canaryCount != 1 {
		t.Errorf("expected exactly 1 canary, got %d", canaryCount)
	}
}

// TestFindCanaryValue verifies FindCanaryValue finds the most recent canary.
func TestFindCanaryValue(t *testing.T) {
	ctx := agentctx.NewAgentContext("test")

	// No canary yet.
	if v := FindCanaryValue(ctx.RecentMessages); v != "" {
		t.Errorf("expected empty, got %q", v)
	}

	// After AppendCanary.
	v1 := AppendCanary(ctx)
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

// TestCompactorCanaryLifecycle verifies the full canary lifecycle:
// first askLLM plants one, second checks it.
func TestCompactorCanaryLifecycle(t *testing.T) {
	c := &Compactor{canaryValue: ""}
	ctx := agentctx.NewAgentContext("test")
	for i := 0; i < 5; i++ {
		ctx.AddRecentMessage(agentctx.NewUserMessage("msg"))
	}

	// Initially no canary in messages.
	if v := FindCanaryValue(ctx.RecentMessages); v != "" {
		t.Error("expected no canary initially")
	}

	// Simulate askLLM post-call replant (canary is checked, then replanted).
	oldCanary := c.canaryValue
	if oldCanary != "" {
		t.Error("expected no old canary initially")
	}

	newVal := AppendCanary(ctx)
	c.canaryValue = newVal

	// Now canary should be in messages and tracked.
	if v := FindCanaryValue(ctx.RecentMessages); v != newVal {
		t.Errorf("expected %q in messages, got %q", newVal, v)
	}
	if c.canaryValue != newVal {
		t.Errorf("expected %q tracked, got %q", newVal, c.canaryValue)
	}

	// Simulate compaction.
	ctx.RecentMessages = RemoveAllCanaries(ctx.RecentMessages)
	c.canaryValue = ""

	if v := FindCanaryValue(ctx.RecentMessages); v != "" {
		t.Error("canary should be removed after compaction")
	}
	if c.canaryValue != "" {
		t.Error("tracked canary should be reset after compaction")
	}
}
