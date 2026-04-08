package context

import (
	"fmt"
	"log/slog"
)

// MiniCompactConfig contains configuration for mini compact (lightweight truncation only).
type MiniCompactConfig struct {
	// TokenThreshold is token percentage threshold (0-100).
	// Mini compact is triggered when tokens_percent >= TokenThreshold.
	TokenThreshold float64

	// StaleOutputThreshold is number of stale outputs before triggering.
	StaleOutputThreshold int

	// MaxTruncated is maximum number of outputs to truncate in one operation.
	MaxTruncated int

	// HeadLen is number of characters to keep from beginning of truncated content.
	HeadLen int

	// TailLen is number of characters to keep from end of truncated content.
	TailLen int
}

// DefaultMiniCompactConfig returns default mini compact configuration.
func DefaultMiniCompactConfig() *MiniCompactConfig {
	return &MiniCompactConfig{
		TokenThreshold:        30.0, // Trigger at 30% token usage
		StaleOutputThreshold: 5,    // Trigger when 5+ stale outputs
		MaxTruncated:           10,   // Max 10 outputs per operation
		HeadLen:                100,  // Keep first 100 chars
		TailLen:                100,  // Keep last 100 chars
	}
}

// MiniCompact implements Compactor interface for lightweight context maintenance.
// Unlike FullCompact, MiniCompact does NOT use LLM summarization.
// It only truncates stale tool outputs to reduce context size.
type MiniCompact struct {
	config *MiniCompactConfig
}

var _ Compactor = (*MiniCompact)(nil)

// NewMiniCompact creates a new MiniCompact compactor.
func NewMiniCompact(config *MiniCompactConfig) *MiniCompact {
	if config == nil {
		config = DefaultMiniCompactConfig()
	}
	return &MiniCompact{
		config: config,
	}
}

// ShouldCompact implements Compactor.
// Returns true if mini compact should trigger based on token threshold or stale outputs.
func (m *MiniCompact) ShouldCompact(messages []AgentMessage) bool {
	tokensPercent := m.EstimateContextTokensPercent()

	// Check token threshold
	if tokensPercent >= m.config.TokenThreshold {
		slog.Info("[MiniCompact] Triggered by token threshold",
			"tokens_percent", tokensPercent,
			"threshold", m.config.TokenThreshold)
		return true
	}

	// Check for stale outputs
	staleCount := m.countStaleOutputs(messages)
	if staleCount >= m.config.StaleOutputThreshold {
		slog.Info("[MiniCompact] Triggered by stale outputs",
			"stale_count", staleCount,
			"threshold", m.config.StaleOutputThreshold)
		return true
	}

	return false
}

// Compact implements Compactor.
// Performs lightweight truncation of stale tool outputs without LLM summarization.
// The previousSummary parameter is ignored (mini compact doesn't support incremental updates).
func (m *MiniCompact) Compact(messages []AgentMessage, _ string) (*CompactionResult, error) {
	tokensBefore := m.EstimateContextTokens(messages)

	// Identify and truncate stale outputs
	truncated := make([]AgentMessage, 0, len(messages))
	truncatedCount := 0

	for _, msg := range messages {
		if m.shouldTruncate(msg, messages) {
			truncatedMsg := m.truncateMessage(msg)
			truncated = append(truncated, truncatedMsg)
			truncatedCount++
		} else {
			truncated = append(truncated, msg)
		}
	}

	tokensAfter := m.EstimateContextTokens(truncated)
	summary := fmt.Sprintf("Truncated %d stale tool outputs (no summarization)", truncatedCount)

	slog.Info("[MiniCompact] Compaction complete",
		"truncated_count", truncatedCount,
		"tokens_before", tokensBefore,
		"tokens_after", tokensAfter,
		"tokens_saved", tokensBefore-tokensAfter)

	return &CompactionResult{
		Summary:      summary,
		Messages:     truncated,
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}, nil
}

// CalculateDynamicThreshold implements Compactor.
// Returns token threshold as an absolute value.
func (m *MiniCompact) CalculateDynamicThreshold() int {
	// Convert percentage to absolute token count
	// Default context window is 128k tokens
	const defaultContextWindow = 128000
	return int(float64(defaultContextWindow) * m.config.TokenThreshold / 100.0)
}

// EstimateContextTokens implements Compactor.
// Estimates total token count for all messages.
func (m *MiniCompact) EstimateContextTokens(messages []AgentMessage) int {
	tokens := 0
	for _, msg := range messages {
		if msg.IsAgentVisible() {
			tokens += m.estimateMessageTokens(msg)
		}
	}
	return tokens
}

// EstimateContextTokensPercent returns token usage as a percentage.
func (m *MiniCompact) EstimateContextTokensPercent() float64 {
	// This method is called from ShouldCompact with current messages
	// For simplicity, we'll return the threshold itself
	// In practice, you'd pass messages to CalculateDynamicThreshold
	return m.config.TokenThreshold
}

// shouldTruncate returns true if a message should be truncated.
// Simple heuristic: tool results that are more than 5 messages after are stale.
func (m *MiniCompact) shouldTruncate(msg AgentMessage, messages []AgentMessage) bool {
	// Only truncate tool results
	if msg.Role != "toolResult" || !msg.IsAgentVisible() {
		return false
	}

	// Count messages after this one
	// Find this message's position
	msgPosition := -1
	for i := range messages {
		if len(messages[i].Content) == len(msg.Content) &&
			messages[i].Role == msg.Role &&
			messages[i].ToolCallID == msg.ToolCallID {
			msgPosition = i
			break
		}
	}

	if msgPosition < 0 {
		return false
	}

	messagesAfter := len(messages) - msgPosition - 1

	// Heuristic: truncate if more than 5 messages after this tool result
	return messagesAfter > 5
}

// truncateMessage truncates a tool result message by keeping head and tail.
func (m *MiniCompact) truncateMessage(msg AgentMessage) AgentMessage {
	content := msg.ExtractText()

	// Don't truncate if too small
	if len(content) <= (m.config.HeadLen + m.config.TailLen) {
		return msg
	}

	head := content[:m.config.HeadLen]
	tail := content[len(content)-m.config.TailLen:]
	truncated := fmt.Sprintf("[truncated %d → %d chars]\n%s\n...[skipped %d chars]...\n%s",
		len(content),
		m.config.HeadLen+m.config.TailLen,
		head,
		len(content)-(m.config.HeadLen+m.config.TailLen),
		tail,
	)

	// Create truncated message
	truncatedMsg := msg
	truncatedMsg.Content = []ContentBlock{TextContent{Text: truncated}}

	return truncatedMsg
}

// countStaleOutputs counts number of tool results that should be truncated.
func (m *MiniCompact) countStaleOutputs(messages []AgentMessage) int {
	count := 0
	for _, msg := range messages {
		if m.shouldTruncate(msg, messages) {
			count++
		}
	}
	return count
}

// estimateMessageTokens estimates token count for a single message.
// Rough approximation: 1 token per 4 characters.
func (m *MiniCompact) estimateMessageTokens(msg AgentMessage) int {
	if !msg.IsAgentVisible() {
		return 0
	}

	charCount := len(msg.ExtractText())
	if charCount == 0 {
		return 0
	}

	return (charCount + 3) / 4
}