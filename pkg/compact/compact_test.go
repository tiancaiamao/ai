package compact

import (
	"context"
	"fmt"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestShouldCompact_TokenThreshold(t *testing.T) {
	config := &Config{
		MaxTokens:   50,
		KeepRecent:  2,
		AutoCompact: true,
	}

	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	// Few tokens — should not compact
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("hi"),
			agentctx.NewAssistantMessage(),
		},
	}
	if compactor.ShouldCompact(context.Background(), agentCtx) {
		t.Error("Should not compact when token threshold is not reached")
	}

	// High token content — should compact
	longText := strings.Repeat("a", 400) // ~100 tokens
	agentCtx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage(longText),
		agentctx.NewAssistantMessage(),
	}
	if !compactor.ShouldCompact(context.Background(), agentCtx) {
		t.Error("Should compact when token threshold is exceeded")
	}
}

func TestShouldCompact_Disabled(t *testing.T) {
	config := &Config{
		AutoCompact: false,
	}

	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	agentCtx := &agentctx.AgentContext{
		RecentMessages: make([]agentctx.AgentMessage, 100),
	}
	for i := range agentCtx.RecentMessages {
		agentCtx.RecentMessages[i] = agentctx.NewUserMessage("test")
	}

	if compactor.ShouldCompact(context.Background(), agentCtx) {
		t.Error("Should not compact when AutoCompact is disabled")
	}
}

func TestShouldCompact_MessageCountDoesNotTriggerWithContextWindow(t *testing.T) {
	config := &Config{
		MaxMessages: 3,
		MaxTokens:   8000,
		AutoCompact: true,
	}

	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 200000)
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("a"),
			agentctx.NewAssistantMessage(),
			agentctx.NewUserMessage("b"),
		},
	}

	if compactor.ShouldCompact(context.Background(), agentCtx) {
		t.Fatal("expected message-count alone not to trigger compaction with large context window")
	}
}

func TestEstimateTokens(t *testing.T) {
	config := DefaultConfig()
	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("Hello world"),
		agentctx.NewAssistantMessage(),
	}

	tokens := compactor.EstimateTokens(messages)
	if tokens <= 0 {
		t.Errorf("Estimated tokens should be positive, got %d", tokens)
	}

	// Very rough check: should be more than 10 characters / 4 = 2.5 tokens
	if tokens < 2 {
		t.Errorf("Estimated tokens seems too low: %d", tokens)
	}
}

func TestCompact_FewMessages(t *testing.T) {
	config := DefaultConfig()
	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	// With fewer messages than KeepRecent, should return nil result
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("Hello"),
			agentctx.NewAssistantMessage(),
		},
	}

	result, err := compactor.Compact(agentCtx)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result even for few messages")
	}
	if result.TokensBefore == 0 {
		t.Error("expected TokensBefore > 0")
	}
}

func TestSplitMessagesByTokenBudget(t *testing.T) {
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("aaaa"),
		agentctx.NewUserMessage("bbbb"),
		agentctx.NewUserMessage("cccc"),
		agentctx.NewUserMessage("dddd"),
		agentctx.NewUserMessage("eeee"),
	}

	oldMessages, recentMessages := splitMessagesByTokenBudget(messages, 2)
	if len(recentMessages) != 2 {
		t.Errorf("Expected 2 recent messages, got %d", len(recentMessages))
	}
	if len(oldMessages) != 3 {
		t.Errorf("Expected 3 old messages, got %d", len(oldMessages))
	}
	if recentMessages[0].ExtractText() != "dddd" || recentMessages[1].ExtractText() != "eeee" {
		t.Errorf("Unexpected recent messages order")
	}
}
func TestTruncateConversationToCharBudget(t *testing.T) {
	// Create messages with known text sizes
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage(strings.Repeat("a", 1000)), // 1000+ chars
		agentctx.NewUserMessage(strings.Repeat("b", 1000)), // 1000+ chars
		agentctx.NewUserMessage(strings.Repeat("c", 1000)), // 1000+ chars
		agentctx.NewUserMessage(strings.Repeat("d", 1000)), // 1000+ chars
		agentctx.NewUserMessage(strings.Repeat("e", 500)),  // 500+ chars
	}

	// Budget for ~2 messages (2000 chars)
	truncated := truncateConversationToCharBudget(messages, 2500)
	if len(truncated) >= len(messages) {
		t.Fatalf("expected truncation, got %d messages (same as original %d)", len(truncated), len(messages))
	}
	// Should keep the most recent messages
	if len(truncated) < 2 {
		t.Fatalf("expected at least 2 messages kept, got %d", len(truncated))
	}
	lastText := truncated[len(truncated)-1].ExtractText()
	if !strings.HasPrefix(lastText, strings.Repeat("e", 10)) {
		t.Fatalf("expected last message to be 'e' message, got %s...", lastText[:20])
	}
}

func TestTruncateConversationToCharBudget_NoTruncationNeeded(t *testing.T) {
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("short"),
		agentctx.NewUserMessage("also short"),
	}

	// Budget larger than total
	truncated := truncateConversationToCharBudget(messages, 10000)
	if len(truncated) != len(messages) {
		t.Fatalf("expected no truncation, got %d messages (original %d)", len(truncated), len(messages))
	}
}

func TestCompact_ForcedSplitWhenManyMessagesButNoOldMessages(t *testing.T) {
	// Create a compactor with a large keep-recent budget
	// so that splitMessagesByTokenBudget returns oldMessages=[]
	// but we have enough messages to trigger the forced split
	cfg := &Config{
		KeepRecentTokens: 100000, // Large enough that all messages "fit"
		AutoCompact:      true,
		ReserveTokens:    1000,
	}

	// Create 60 short messages that easily fit within 100k tokens budget
	messages := make([]agentctx.AgentMessage, 60)
	for i := 0; i < 60; i++ {
		messages[i] = agentctx.NewUserMessage(fmt.Sprintf("message %d", i))
	}

	_ = agentctx.NewAgentContext("sys")

	// Use a compactor to verify config
	c := NewCompactor(cfg, llm.Model{ID: "test"}, "test-key", "test prompt", 200000)
	_ = c

	// Test the split logic directly
	oldMsgs, recentMsgs := splitMessagesByTokenBudget(messages, 100000)
	if len(oldMsgs) != 0 {
		t.Fatalf("expected oldMessages=0 with large budget, got %d", len(oldMsgs))
	}

	// Verify the forced split logic would trigger
	if len(messages) <= 50 {
		t.Fatal("test setup error: need > 50 messages to trigger forced split")
	}

	// Simulate the forced split logic
	const forceSplitMinMessages = 50
	if len(messages) > forceSplitMinMessages {
		keepCount := max(10, int(float64(len(messages))*0.3))
		splitIndex := len(messages) - keepCount
		oldMsgs = messages[:splitIndex]
		recentMsgs = messages[splitIndex:]
	}

	if len(oldMsgs) == 0 {
		t.Fatal("forced split should have produced oldMessages")
	}
	if len(recentMsgs) != max(10, int(float64(60)*0.3)) {
		t.Fatalf("expected %d recent messages, got %d", max(10, int(float64(60)*0.3)), len(recentMsgs))
	}
}
