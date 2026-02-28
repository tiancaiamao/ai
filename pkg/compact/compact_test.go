package compact

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestShouldCompact(t *testing.T) {
	config := &Config{
		MaxMessages: 10, // ignored in manual mode fallback
		MaxTokens:   50,
		KeepRecent:  2,
		AutoCompact: true,
	}

	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	// Test with few messages - should not compact (token threshold not reached)
	fewMessages := make([]agentctx.AgentMessage, 5)
	for i := 0; i < 5; i++ {
		fewMessages[i] = agentctx.NewUserMessage("test message")
	}

	if compactor.ShouldCompact(fewMessages) {
		t.Error("Should not compact when token threshold is not reached")
	}

	// Test with high token content - should compact
	longText := strings.Repeat("a", 400) // ~100 tokens
	manyMessages := []agentctx.AgentMessage{
		agentctx.NewUserMessage(longText),
		agentctx.NewAssistantMessage(),
	}

	if !compactor.ShouldCompact(manyMessages) {
		t.Error("Should compact when token threshold is exceeded")
	}
}

func TestShouldCompactTokenLimit(t *testing.T) {
	config := &Config{
		MaxMessages: 0,
		MaxTokens:   50,
		KeepRecent:  2,
		AutoCompact: true,
	}

	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	longText := strings.Repeat("a", 400) // ~100 tokens
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage(longText),
		agentctx.NewAssistantMessage(),
	}

	if !compactor.ShouldCompact(messages) {
		t.Error("Should compact when token limit is exceeded")
	}
}

func TestShouldCompactMessageLimitDoesNotTriggerWithContextWindow(t *testing.T) {
	config := &Config{
		MaxMessages: 3,
		MaxTokens:   8000,
		AutoCompact: true,
	}

	// Large context window means token threshold likely won't be hit for short messages.
	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 200000)
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("a"),
		agentctx.NewAssistantMessage(),
		agentctx.NewUserMessage("b"),
	}

	if compactor.ShouldCompact(messages) {
		t.Fatal("expected message-count alone not to trigger compaction in manual mode fallback")
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

func TestCompactDisabled(t *testing.T) {
	config := &Config{
		AutoCompact: false,
	}

	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	messages := make([]agentctx.AgentMessage, 100)
	for i := 0; i < 100; i++ {
		messages[i] = agentctx.NewUserMessage("test")
	}

	if compactor.ShouldCompact(messages) {
		t.Error("Should not compact when AutoCompact is disabled")
	}
}

func TestCompactFewMessages(t *testing.T) {
	config := DefaultConfig()
	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	// With fewer messages than KeepRecent, should return as-is
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("Hello"),
		agentctx.NewAssistantMessage(),
	}

	result, err := compactor.Compact(messages, "")
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	if len(result.Messages) != len(messages) {
		t.Errorf("Expected %d messages, got %d", len(messages), len(result.Messages))
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
