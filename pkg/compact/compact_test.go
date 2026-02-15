package compact

import (
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestShouldCompact(t *testing.T) {
	config := &Config{
		MaxMessages: 10,
		MaxTokens:   0,
		KeepRecent:  2,
		AutoCompact: true,
	}

	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	// Test with few messages - should not compact
	fewMessages := make([]agent.AgentMessage, 5)
	for i := 0; i < 5; i++ {
		fewMessages[i] = agent.NewUserMessage("test message")
	}

	if compactor.ShouldCompact(fewMessages) {
		t.Error("Should not compact with only 5 messages (threshold is 10)")
	}

	// Test with many messages - should compact
	manyMessages := make([]agent.AgentMessage, 15)
	for i := 0; i < 15; i++ {
		manyMessages[i] = agent.NewUserMessage("test message")
	}

	if !compactor.ShouldCompact(manyMessages) {
		t.Error("Should compact with 15 messages (threshold is 10)")
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
	messages := []agent.AgentMessage{
		agent.NewUserMessage(longText),
		agent.NewAssistantMessage(),
	}

	if !compactor.ShouldCompact(messages) {
		t.Error("Should compact when token limit is exceeded")
	}
}

func TestShouldCompactMessageLimitEvenWithContextWindow(t *testing.T) {
	config := &Config{
		MaxMessages: 3,
		MaxTokens:   8000,
		AutoCompact: true,
	}

	// Large context window means token threshold likely won't be hit for short messages.
	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 200000)
	messages := []agent.AgentMessage{
		agent.NewUserMessage("a"),
		agent.NewAssistantMessage(),
		agent.NewUserMessage("b"),
	}

	if !compactor.ShouldCompact(messages) {
		t.Fatal("expected compaction to trigger on message count even when context window is configured")
	}
}

func TestEstimateTokens(t *testing.T) {
	config := DefaultConfig()
	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	messages := []agent.AgentMessage{
		agent.NewUserMessage("Hello world"),
		agent.NewAssistantMessage(),
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

	messages := make([]agent.AgentMessage, 100)
	for i := 0; i < 100; i++ {
		messages[i] = agent.NewUserMessage("test")
	}

	if compactor.ShouldCompact(messages) {
		t.Error("Should not compact when AutoCompact is disabled")
	}
}

func TestCompactFewMessages(t *testing.T) {
	config := DefaultConfig()
	compactor := NewCompactor(config, llm.Model{}, "test-key", "test", 0)

	// With fewer messages than KeepRecent, should return as-is
	messages := []agent.AgentMessage{
		agent.NewUserMessage("Hello"),
		agent.NewAssistantMessage(),
	}

	result, err := compactor.Compact(messages)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	if len(result) != len(messages) {
		t.Errorf("Expected %d messages, got %d", len(messages), len(result))
	}
}

func TestSplitMessagesByTokenBudget(t *testing.T) {
	messages := []agent.AgentMessage{
		agent.NewUserMessage("aaaa"),
		agent.NewUserMessage("bbbb"),
		agent.NewUserMessage("cccc"),
		agent.NewUserMessage("dddd"),
		agent.NewUserMessage("eeee"),
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
