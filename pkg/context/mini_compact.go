package context

import (
	"fmt"
	"log/slog"
)

// MiniCompactor implements lightweight context compression without LLM.
// It truncates stale tool outputs and optionally updates LLM context.
type MiniCompactor struct {
	config         *MiniCompactConfig
	contextWindow  int
}

// MiniCompactConfig contains configuration for mini compaction.
type MiniCompactConfig struct {
	TokenThreshold   float64 // Trigger when token usage exceeds this percentage (e.g., 30.0 for 30%)
	StaleThreshold   int      // Trigger when stale outputs exceed this count
	KeepRecent       int      // Number of recent messages to always keep
	AutoCompact      bool     // Whether to automatically compact
}

// DefaultMiniCompactConfig returns default mini compaction configuration.
func DefaultMiniCompactConfig() *MiniCompactConfig {
	return &MiniCompactConfig{
		TokenThreshold: 30.0, // 30% of context window
		StaleThreshold: 5,     // Trigger when 5+ stale outputs
		KeepRecent:     5,     // Keep last 5 messages
		AutoCompact:    true,
	}
}

// NewMiniCompactor creates a new MiniCompactor.
func NewMiniCompactor(config *MiniCompactConfig, contextWindow int) *MiniCompactor {
	if config == nil {
		config = DefaultMiniCompactConfig()
	}
	if contextWindow <= 0 {
		contextWindow = 128000 // Default context window
	}
	return &MiniCompactor{
		config:        config,
		contextWindow: contextWindow,
	}
}

// ShouldCompact determines if context should be compacted.
func (m *MiniCompactor) ShouldCompact(ctx *AgentContext) bool {
	if !m.config.AutoCompact {
		return false
	}

	tokens := m.EstimateContextTokens(ctx)
	tokensPercent := float64(tokens) / float64(m.contextWindow) * 100
	staleCount := ctx.CountStaleOutputs(m.config.KeepRecent)

	// Trigger if token usage exceeds threshold OR too many stale outputs
	if tokensPercent >= m.config.TokenThreshold {
		slog.Info("[MiniCompact] Token threshold exceeded",
			"tokens", tokens,
			"percent", tokensPercent,
			"threshold", m.config.TokenThreshold)
		return true
	}

	if staleCount > m.config.StaleThreshold {
		slog.Info("[MiniCompact] Stale output threshold exceeded",
			"staleCount", staleCount,
			"threshold", m.config.StaleThreshold)
		return true
	}

	return false
}

// Compact performs lightweight compaction by truncating stale tool outputs.
// It does NOT call LLM - it only truncates messages and updates metadata.
func (m *MiniCompactor) Compact(ctx *AgentContext) (*CompactionResult, error) {
	if len(ctx.RecentMessages) == 0 {
		return &CompactionResult{}, nil
	}

	tokensBefore := m.EstimateContextTokens(ctx)

	// Truncate stale tool outputs
	truncatedCount := 0
	currentTurn := ctx.AgentState.TotalTurns
	protectedStart := len(ctx.RecentMessages) - m.config.KeepRecent
	if protectedStart < 0 {
		protectedStart = 0
	}

	for i, msg := range ctx.RecentMessages {
		// Skip protected recent messages
		if i >= protectedStart {
			continue
		}

		// Only truncate tool results that are stale
		if msg.Role != "toolResult" || !msg.IsAgentVisible() {
			continue
		}

		// Check if message is stale (old turn)
		if currentTurn-msg.TruncatedAt > m.config.KeepRecent {
			originalText := msg.ExtractText()
			if len(originalText) == 0 {
				continue
			}

			// Mark as truncated and preserve head/tail
			msg.Truncated = true
			if msg.OriginalSize == 0 {
				msg.OriginalSize = len(originalText)
			}
			msg.Content = []ContentBlock{
				TextContent{
					Type: "text",
					Text: TruncateWithHeadTail(originalText),
				},
			}
			truncatedCount++
		}
	}

	tokensAfter := m.EstimateContextTokens(ctx)
	summary := fmt.Sprintf("Truncated %d stale tool outputs", truncatedCount)

	slog.Info("[MiniCompact] Compaction complete",
		"truncatedCount", truncatedCount,
		"tokensBefore", tokensBefore,
		"tokensAfter", tokensAfter,
		"saved", tokensBefore-tokensAfter)

	return &CompactionResult{
		Summary:      summary,
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}, nil
}

// CalculateDynamicThreshold returns the token threshold for compaction.
func (m *MiniCompactor) CalculateDynamicThreshold() int {
	return int(float64(m.contextWindow) * m.config.TokenThreshold / 100.0)
}

// EstimateContextTokens estimates the token count of context.
func (m *MiniCompactor) EstimateContextTokens(ctx *AgentContext) int {
	return ctx.EstimateTokens()
}
