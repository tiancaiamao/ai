package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// Config contains configuration for context compression.
type Config struct {
	MaxMessages      int  // Maximum messages before compression
	MaxTokens        int  // Approximate token limit before compression
	KeepRecent       int  // Number of recent messages to keep
	KeepRecentTokens int  // Token budget to keep from the most recent messages
	ReserveTokens    int  // Tokens to reserve when using context window
	AutoCompact      bool // Whether to automatically compact
}

// DefaultConfig returns default compression configuration.
func DefaultConfig() *Config {
	return &Config{
		MaxMessages:      50,    // Compact after 50 messages
		MaxTokens:        8000,  // Compact after ~8000 tokens (fallback)
		KeepRecent:       5,     // Keep last 5 messages uncompressed
		KeepRecentTokens: 20000, // Keep ~20k tokens from the recent context
		ReserveTokens:    16384, // Reserve tokens for responses when using context window
		AutoCompact:      true,
	}
}

// Compactor handles context compression.
type Compactor struct {
	config        *Config
	model         llm.Model
	apiKey        string
	systemPrompt  string
	contextWindow int
}

// NewCompactor creates a new Compactor.
func NewCompactor(config *Config, model llm.Model, apiKey, systemPrompt string, contextWindow int) *Compactor {
	if config == nil {
		config = DefaultConfig()
	}
	return &Compactor{
		config:        config,
		model:         model,
		apiKey:        apiKey,
		systemPrompt:  systemPrompt,
		contextWindow: contextWindow,
	}
}

// ShouldCompact determines if context should be compressed.
func (c *Compactor) ShouldCompact(messages []agent.AgentMessage) bool {
	if !c.config.AutoCompact {
		return false
	}

	// Prefer token limit when available (context window or max tokens)
	if tokenLimit, _ := c.EffectiveTokenLimit(); tokenLimit > 0 {
		tokens := c.EstimateContextTokens(messages)
		return tokens >= tokenLimit
	}

	// Check message count (fallback)
	if c.config.MaxMessages > 0 && len(messages) >= c.config.MaxMessages {
		return true
	}
	return false
}

// Compact compresses the context by summarizing old messages.
func (c *Compactor) Compact(messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	keepRecentTokens := c.effectiveKeepRecentTokens()
	var oldMessages []agent.AgentMessage
	var recentMessages []agent.AgentMessage
	if keepRecentTokens > 0 {
		oldMessages, recentMessages = splitMessagesByTokenBudget(messages, keepRecentTokens)
		if len(oldMessages) == 0 {
			return messages, nil
		}
		slog.Info("[Compact] Compressing messages", "count", len(messages), "keepTokens", keepRecentTokens)
	} else {
		keepCount := c.keepRecentMessages()
		if len(messages) <= keepCount {
			return messages, nil
		}
		slog.Info("[Compact] Compressing messages", "count", len(messages), "keepRecent", keepCount)
		splitIndex := len(messages) - keepCount
		oldMessages = messages[:splitIndex]
		recentMessages = messages[splitIndex:]
	}

	// Generate summary of old messages
	summary, err := c.GenerateSummary(oldMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	slog.Debug("[Compact] Generated summary", "chars", len(summary))

	// Create new context with summary + recent messages
	newMessages := []agent.AgentMessage{
		agent.NewUserMessage(fmt.Sprintf("[Previous conversation summary]\n\n%s", summary)),
	}

	newMessages = append(newMessages, recentMessages...)

	slog.Info("[Compact] Compressed to messages", "count", len(newMessages))

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

// ContextWindow returns the configured model context window.
func (c *Compactor) ContextWindow() int {
	return c.contextWindow
}

// SetContextWindow updates the model context window used for compaction.
func (c *Compactor) SetContextWindow(window int) {
	c.contextWindow = window
}

// ReserveTokens returns the effective reserve tokens setting.
func (c *Compactor) ReserveTokens() int {
	if c.config == nil || c.config.ReserveTokens <= 0 {
		return DefaultConfig().ReserveTokens
	}
	return c.config.ReserveTokens
}

func (c *Compactor) keepRecentMessages() int {
	if c.config == nil || c.config.KeepRecent <= 0 {
		return DefaultConfig().KeepRecent
	}
	return c.config.KeepRecent
}

func (c *Compactor) effectiveKeepRecentTokens() int {
	if c == nil || c.config == nil || c.config.KeepRecentTokens <= 0 {
		return 0
	}

	keep := c.config.KeepRecentTokens
	if limit, _ := c.EffectiveTokenLimit(); limit > 0 {
		maxKeep := limit / 2
		if maxKeep > 0 && keep > maxKeep {
			keep = maxKeep
		}
	}

	return keep
}

// EffectiveTokenLimit returns the token limit for compaction and its source.
func (c *Compactor) EffectiveTokenLimit() (int, string) {
	if c == nil {
		return 0, "none"
	}
	if c.contextWindow > 0 {
		reserve := c.ReserveTokens()
		limit := c.contextWindow - reserve
		if limit > 0 {
			return limit, "context_window"
		}
	}
	if c.config != nil && c.config.MaxTokens > 0 {
		return c.config.MaxTokens, "max_tokens"
	}
	return 0, "none"
}

// EstimateTokens provides a rough estimation of token count.
func (c *Compactor) EstimateTokens(messages []agent.AgentMessage) int {
	totalTokens := 0
	for _, msg := range messages {
		totalTokens += estimateMessageTokens(msg)
	}
	return totalTokens
}

// EstimateContextTokens estimates context tokens using usage when available.
func (c *Compactor) EstimateContextTokens(messages []agent.AgentMessage) int {
	usageTokens, lastIndex := lastAssistantUsageTokens(messages)
	if lastIndex >= 0 {
		trailingTokens := 0
		for i := lastIndex + 1; i < len(messages); i++ {
			trailingTokens += estimateMessageTokens(messages[i])
		}
		return usageTokens + trailingTokens
	}
	return c.EstimateTokens(messages)
}

// CompactIfNeeded compacts the context if it exceeds limits.
func (c *Compactor) CompactIfNeeded(messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	if c.ShouldCompact(messages) {
		return c.Compact(messages)
	}
	return messages, nil
}

func lastAssistantUsageTokens(messages []agent.AgentMessage) (int, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "assistant" || msg.Usage == nil {
			continue
		}
		stopReason := strings.ToLower(strings.TrimSpace(msg.StopReason))
		if stopReason == "aborted" || stopReason == "error" {
			continue
		}
		tokens := usageTotalTokens(msg.Usage)
		if tokens > 0 {
			return tokens, i
		}
	}
	return 0, -1
}

func usageTotalTokens(usage *agent.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.InputTokens + usage.OutputTokens + usage.CacheRead + usage.CacheWrite
}

func estimateMessageTokens(msg agent.AgentMessage) int {
	charCount := 0
	for _, block := range msg.Content {
		switch b := block.(type) {
		case agent.TextContent:
			charCount += len(b.Text)
		case agent.ThinkingContent:
			charCount += len(b.Thinking)
		case agent.ToolCallContent:
			charCount += len(b.Name)
			if b.Arguments != nil {
				if argBytes, err := json.Marshal(b.Arguments); err == nil {
					charCount += len(argBytes)
				}
			}
		case agent.ImageContent:
			// Roughly estimate images as 1200 tokens (4800 chars).
			charCount += 4800
		}
	}
	if charCount == 0 {
		charCount = len(msg.ExtractText())
	}
	if charCount == 0 {
		return 0
	}
	return int(math.Ceil(float64(charCount) / 4.0))
}

func splitMessagesByTokenBudget(
	messages []agent.AgentMessage,
	tokenBudget int,
) ([]agent.AgentMessage, []agent.AgentMessage) {
	if len(messages) == 0 {
		return messages, nil
	}
	if tokenBudget <= 0 {
		return messages[:len(messages)-1], messages[len(messages)-1:]
	}

	used := 0
	start := len(messages)

	for i := len(messages) - 1; i >= 0; i-- {
		msgTokens := estimateMessageTokens(messages[i])
		if used+msgTokens > tokenBudget && start != len(messages) {
			break
		}
		used += msgTokens
		start = i
	}

	if start <= 0 {
		return nil, messages
	}
	if start >= len(messages) {
		return messages[:len(messages)-1], messages[len(messages)-1:]
	}
	return messages[:start], messages[start:]
}
