package compact

import (
	"context"
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