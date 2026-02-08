package compact

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// Config contains configuration for context compression.
type Config struct {
	MaxMessages int  // Maximum messages before compression
	MaxTokens   int  // Approximate token limit before compression
	KeepRecent  int  // Number of recent messages to keep
	AutoCompact bool // Whether to automatically compact
}

// DefaultConfig returns default compression configuration.
func DefaultConfig() *Config {
	return &Config{
		MaxMessages: 50,   // Compact after 50 messages
		MaxTokens:   8000, // Compact after ~8000 tokens
		KeepRecent:  5,    // Keep last 5 messages uncompressed
		AutoCompact: true,
	}
}

// Compactor handles context compression.
type Compactor struct {
	config       *Config
	model        llm.Model
	apiKey       string
	systemPrompt string
}

// NewCompactor creates a new Compactor.
func NewCompactor(config *Config, model llm.Model, apiKey, systemPrompt string) *Compactor {
	return &Compactor{
		config:       config,
		model:        model,
		apiKey:       apiKey,
		systemPrompt: systemPrompt,
	}
}

// ShouldCompact determines if context should be compressed.
func (c *Compactor) ShouldCompact(messages []agent.AgentMessage) bool {
	if !c.config.AutoCompact {
		return false
	}

	// Check message count
	if len(messages) >= c.config.MaxMessages {
		return true
	}

	// Check token count (rough estimation)
	tokens := c.EstimateTokens(messages)
	if tokens >= c.config.MaxTokens {
		return true
	}

	return false
}

// Compact compresses the context by summarizing old messages.
func (c *Compactor) Compact(messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	if len(messages) <= c.config.KeepRecent {
		return messages, nil
	}

	log.Printf("[Compact] Compressing %d messages (keeping %d recent)", len(messages), c.config.KeepRecent)

	// Split messages into old (to compress) and recent (to keep)
	splitIndex := len(messages) - c.config.KeepRecent
	oldMessages := messages[:splitIndex]
	recentMessages := messages[splitIndex:]

	// Generate summary of old messages
	summary, err := c.GenerateSummary(oldMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	log.Printf("[Compact] Generated summary: %d chars", len(summary))

	// Create new context with summary + recent messages
	newMessages := []agent.AgentMessage{
		agent.NewUserMessage(fmt.Sprintf("[Previous conversation summary]\n\n%s", summary)),
	}

	newMessages = append(newMessages, recentMessages...)

	log.Printf("[Compact] Compressed to %d messages", len(newMessages))

	return newMessages, nil
}

// GenerateSummary generates a summary of messages using the LLM.
func (c *Compactor) GenerateSummary(messages []agent.AgentMessage) (string, error) {
	// Extract conversation text
	var conversation strings.Builder
	conversation.WriteString("Previous conversation:\n")

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			conversation.WriteString(fmt.Sprintf("User: %s\n", msg.ExtractText()))
		case "assistant":
			conversation.WriteString(fmt.Sprintf("Assistant: %s\n", msg.ExtractText()))
		case "toolResult":
			// Skip tool results in summary
		}
	}

	// Build prompt
	prompt := fmt.Sprintf(`Please provide a concise summary of the following conversation. Focus on:
- Key topics discussed
- Important decisions made
- Files or code modified
- Any action items or next steps

%s

Summary:`, conversation.String())

	// Create temporary LLM context for summarization
	llmMessages := []llm.LLMMessage{
		{Role: "system", Content: c.systemPrompt},
		{Role: "user", Content: prompt},
	}

	llmCtx := llm.LLMContext{
		Messages: llmMessages,
	}

	// Stream LLM response
	ctx := context.Background()
	llmStream := llm.StreamLLM(ctx, c.model, llmCtx, c.apiKey)

	var summary strings.Builder
	for event := range llmStream.Iterator(ctx) {
		if event.Done {
			break
		}

		switch e := event.Value.(type) {
		case llm.LLMTextDeltaEvent:
			summary.WriteString(e.Delta)
		case llm.LLMErrorEvent:
			return "", e.Error
		}
	}

	result := summary.String()
	if result == "" {
		return "", fmt.Errorf("empty summary generated")
	}

	return result, nil
}

// EstimateTokens provides a rough estimation of token count.
func (c *Compactor) EstimateTokens(messages []agent.AgentMessage) int {
	// Rough estimation: ~4 characters per token
	totalChars := 0
	for _, msg := range messages {
		totalChars += len(msg.ExtractText())
		totalChars += 50 // Overhead per message
	}

	return totalChars / 4
}

// CompactIfNeeded compacts the context if it exceeds limits.
func (c *Compactor) CompactIfNeeded(messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	if c.ShouldCompact(messages) {
		return c.Compact(messages)
	}
	return messages, nil
}
