package compact

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestShouldCompact(t *testing.T) {
	config := &Config{
		MaxMessages: 10,
		MaxTokens:   1000,
		KeepRecent:  2,
		AutoCompact: true,
	}

	compactor := NewCompactor(config, llm.Model{}, "test-key", "test")

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

func TestEstimateTokens(t *testing.T) {
	config := DefaultConfig()
	compactor := NewCompactor(config, llm.Model{}, "test-key", "test")

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

	compactor := NewCompactor(config, llm.Model{}, "test-key", "test")

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
	compactor := NewCompactor(config, llm.Model{}, "test-key", "test")

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
