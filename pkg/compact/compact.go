package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
)

// Config contains configuration for context compression.
type Config struct {
	MaxMessages         int    // Maximum messages before compression
	MaxTokens           int    // Approximate token limit before compression
	KeepRecent          int    // Number of recent messages to keep
	KeepRecentTokens    int    // Token budget to keep from the recent messages
	ReserveTokens       int    // Tokens to reserve when using context window
	ToolCallCutoff      int    // Summarize oldest tool outputs when visible tool calls exceed this
	ToolSummaryStrategy string // llm, heuristic, off
	// ToolSummaryAutomation controls when background tool-output summary runs:
	// - off: disable automatic tool-output summary
	// - fallback: only run when compactor pressure fallback is triggered
	// - always: run whenever ToolCallCutoff is exceeded
	ToolSummaryAutomation string
	// GracePeriod protects the N most recent tool results from being archived during
	// tool call pairing check. This allows tool calls that span compaction boundaries
	// to complete without their results being hidden. Default is 1 (the most recent).
	GracePeriod int
	AutoCompact bool // Whether to automatically compact
}

// DefaultConfig returns default compression configuration.
func DefaultConfig() *Config {
	return &Config{
		MaxMessages:           50,    // Compact after 50 messages
		MaxTokens:             8000,  // Compact after ~8000 tokens (fallback)
		KeepRecent:            5,     // Keep last 5 messages uncompressed
		KeepRecentTokens:      20000, // Keep ~20k tokens from the recent context
		ReserveTokens:         16384, // Reserve tokens for responses when using context window
		ToolCallCutoff:        10,    // Summarize tool outputs after 10 visible tool results
		ToolSummaryStrategy:   "off", // Tool summary strategy (llm, heuristic, off)
		ToolSummaryAutomation: "off", // Automatic tool-output summary (off, fallback, always)
		GracePeriod:           1,     // Protect 1 most recent tool result by default
		AutoCompact:           true,  // Automatic context compression at 75% threshold
	}
}

// Compactor handles context compression.
type Compactor struct {
	config        *Config
	model         llm.Model
	apiKey        string
	systemPrompt  string
	contextWindow int

	// Cache-friendly summarization fields. When set via SetAgentLLMContext,
	// summary requests reuse the main agent's system prompt and real message
	// format so they share a prefix with the main conversation, maximizing
	// provider prefix-cache hits. See GenerateSummaryWithPrevious.
	agentSystemPrompt  string
	agentContextPrefix string
	thinkingLevel      string
	messageConverter   func([]agentctx.AgentMessage) []llm.LLMMessage
}

// SetAgentLLMContext configures the compactor to reuse the main agent's system
// prompt, context prefix, and message converter when generating summaries.
// This makes summary requests share the conversation prefix for cache reuse
// instead of using a dedicated system prompt with serialized text.
// thinkingLevel is used to reconstruct the thinking instruction appended to
// the system prompt (matching the main agent loop) so the cache prefix matches.
// Call this after the agent context is built (system prompt + prefix are known).
func (c *Compactor) SetAgentLLMContext(systemPrompt, contextPrefix, thinkingLevel string, converter func([]agentctx.AgentMessage) []llm.LLMMessage) {
	c.agentSystemPrompt = systemPrompt
	c.agentContextPrefix = contextPrefix
	c.thinkingLevel = thinkingLevel
	c.messageConverter = converter
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

// GetConfig returns the compactor configuration.
func (c *Compactor) GetConfig() *Config {
	return c.config
}

// CalculateDynamicThreshold calculates the compaction threshold based on context window.
// For models with large context windows (e.g., 128k), this allows much more context
// before triggering compaction, rather than using a fixed 8000 token limit.
// CalculateDynamicThreshold returns the dynamic compaction threshold based on context window.
// Exported for use by context_management tool to provide feedback when compact is rejected.
func (c *Compactor) CalculateDynamicThreshold() int {
	// If context window is known, calculate dynamic threshold
	if c.contextWindow > 0 {
		// Reserve tokens for:
		// - System prompt (~5k estimated)
		// - Tool definitions (~3k estimated)
		// - Output generation (16k reserve)
		// - Safety margin (20% of available)

		systemTokens := estimateStringTokens(c.systemPrompt)
		toolTokens := 3000 // Average tool definitions
		reserveTokens := c.ReserveTokens()

		overhead := systemTokens + toolTokens + reserveTokens
		available := c.contextWindow - overhead

		if available <= 0 {
			// Fallback to configured max tokens if window is too small
			return c.config.MaxTokens
		}

		// Use 75% of available as compaction threshold
		// This leaves 25% buffer before hitting context limit
		threshold := int(float64(available) * 0.75)

		// Ensure minimum threshold
		minThreshold := 4000
		if threshold < minThreshold {
			threshold = minThreshold
		}

		return threshold
	}

	// Fallback to configured max tokens
	return c.config.MaxTokens
}

// calculateKeepRecentBudget calculates the token budget for keeping recent messages.
// This scales with the context window rather than using a fixed value.
func (c *Compactor) calculateKeepRecentBudget() int {
	// If a fixed budget is configured, respect it (but cap it)
	if c.config.KeepRecentTokens > 0 {
		budget := c.config.KeepRecentTokens

		// Don't let keep-recent exceed 30% of available context
		if threshold := c.CalculateDynamicThreshold(); threshold > 0 {
			maxKeep := int(float64(threshold) * 0.3)
			if budget > maxKeep && maxKeep > 0 {
				budget = maxKeep
			}
		}

		return budget
	}

	// Calculate based on threshold
	threshold := c.CalculateDynamicThreshold()
	if threshold > 0 {
		// Keep 25% of threshold as recent context
		return int(float64(threshold) * 0.25)
	}

	// Fallback to default
	return 20000
}

// estimateStringTokens provides a rough token estimation for a string.
func estimateStringTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	// Rough approximation: 1 token per 4 characters
	return int(float64(len(s)) / 4.0)
}

var (
	summarizationSystemPrompt = prompt.CompactSystemPrompt()
	summarizationPrompt       = prompt.CompactSummarizePrompt()
	updateSummarizationPrompt = prompt.CompactUpdatePrompt()
)

// GenerateSummary generates a structured summary of messages using the LLM.

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

// KeepRecentMessages returns the effective keep-recent message count.
func (c *Compactor) KeepRecentMessages() int {
	return c.keepRecentMessages()
}

// KeepRecentTokens returns the effective keep-recent token budget.
func (c *Compactor) KeepRecentTokens() int {
	return c.effectiveKeepRecentTokens()
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
func (c *Compactor) EstimateTokens(messages []agentctx.AgentMessage) int {
	totalTokens := 0
	for _, msg := range messages {
		if !msg.IsAgentVisible() {
			continue
		}
		totalTokens += estimateMessageTokens(msg)
	}
	return totalTokens
}

func lastAssistantUsageTokens(messages []agentctx.AgentMessage) (int, int) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !msg.IsAgentVisible() {
			continue
		}
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

func usageTotalTokens(usage *agentctx.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	return usage.InputTokens + usage.OutputTokens + usage.CacheRead + usage.CacheWrite
}

func estimateMessageTokens(msg agentctx.AgentMessage) int {
	if !msg.IsAgentVisible() {
		return 0
	}

	charCount := 0
	for _, block := range msg.Content {
		switch b := block.(type) {
		case agentctx.TextContent:
			charCount += len(b.Text)
		case agentctx.ThinkingContent:
			charCount += len(b.Thinking)
		case agentctx.ToolCallContent:
			charCount += len(b.Name)
			if b.Arguments != nil {
				if argBytes, err := json.Marshal(b.Arguments); err == nil {
					charCount += len(argBytes)
				}
			}
		case agentctx.ImageContent:
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

// EstimateMessageTokens estimates token usage for a single message.
func EstimateMessageTokens(msg agentctx.AgentMessage) int {
	return estimateMessageTokens(msg)
}

// Compact compacts context by summarizing old messages using AgentContext.
// This method implements the context.Compactor interface.
func (c *Compactor) Compact(ctx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	if len(ctx.RecentMessages) == 0 {
		return &agentctx.CompactionResult{
			TokensBefore: 0,
			TokensAfter:  0,
		}, nil
	}

	tokensBefore := ctx.EstimateTokens()

	keepRecentTokens := c.calculateKeepRecentBudget()
	var oldMessages []agentctx.AgentMessage
	var recentMessages []agentctx.AgentMessage

	if keepRecentTokens > 0 {
		oldMessages, recentMessages = splitMessagesByTokenBudget(ctx.RecentMessages, keepRecentTokens)
		if len(oldMessages) == 0 {
			// Token estimation says all messages fit within budget, but if we have
			// many messages the estimation is likely inaccurate (rough char/4
			// heuristic). Force a split when message count is high.
			const forceSplitMinMessages = 50
			if len(ctx.RecentMessages) > forceSplitMinMessages {
				// Keep the last 30% of messages (minimum 10)
				keepCount := max(10, int(float64(len(ctx.RecentMessages))*0.3))
				splitIndex := len(ctx.RecentMessages) - keepCount
				oldMessages = ctx.RecentMessages[:splitIndex]
				recentMessages = ctx.RecentMessages[splitIndex:]
				slog.Info("[Compact] Forced split: token budget covered all messages but count exceeds threshold",
					"count", len(ctx.RecentMessages),
					"keepCount", keepCount,
					"keepTokens", keepRecentTokens,
					"forceSplitMin", forceSplitMinMessages)
			} else {
				return &agentctx.CompactionResult{
					TokensBefore: tokensBefore,
					TokensAfter:  tokensBefore,
				}, nil
			}
		}
		slog.Info("[Compact] Compressing messages",
			"count", len(ctx.RecentMessages),
			"keepTokens", keepRecentTokens,
			"threshold", c.CalculateDynamicThreshold(),
			"contextWindow", c.contextWindow,
			"hasPreviousSummary", ctx.LastCompactionSummary != "")
	} else {
		keepCount := c.keepRecentMessages()
		if len(ctx.RecentMessages) <= keepCount {
			return &agentctx.CompactionResult{
				TokensBefore: tokensBefore,
				TokensAfter:  tokensBefore,
			}, nil
		}
		slog.Info("[Compact] Compressing messages",
			"count", len(ctx.RecentMessages),
			"keepRecent", keepCount,
			"threshold", c.CalculateDynamicThreshold(),
			"hasPreviousSummary", ctx.LastCompactionSummary != "")
		splitIndex := len(ctx.RecentMessages) - keepCount
		oldMessages = ctx.RecentMessages[:splitIndex]
		recentMessages = ctx.RecentMessages[splitIndex:]
	}

	// Generate summary of old messages (with previous summary for incremental update)
	summary, err := c.GenerateSummaryWithPrevious(oldMessages, ctx.LastCompactionSummary)
	if err != nil {
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	slog.Info("[Compact] Generated summary", "chars", len(summary), "hasPrevious", ctx.LastCompactionSummary != "")

	// Ensure tool_call and tool_result pairing is preserved
	if c.config.GracePeriod > 0 {
		recentMessages = c.ensureToolCallPairingWithGrace(oldMessages, recentMessages)
	} else {
		recentMessages = ensureToolCallPairing(oldMessages, recentMessages)
	}

	// Create new recent messages with summary
	newRecentMessages := []agentctx.AgentMessage{
		agentctx.NewCompactionSummaryMessage(summary),
	}

	recentMessages = compactToolResultsInRecent(recentMessages, c.config.ToolCallCutoff)
	recentMessages = cleanOldRuntimeState(recentMessages)
	newRecentMessages = append(newRecentMessages, recentMessages...)
	messagesBefore := len(ctx.RecentMessages)

	// Update AgentContext directly
	ctx.RecentMessages = newRecentMessages
	ctx.LastCompactionSummary = summary
	// Preserve LLMContext maintained by ContextManager; do not overwrite.
	// The summary is already stored in ctx.LastCompactionSummary and injected
	// as [Previous conversation summary] message in newRecentMessages above.
	// ctx.LLMContext = summary

	tokensAfter := ctx.EstimateTokens()
	messagesAfter := len(newRecentMessages)
	slog.Info("[Compact] Compressed context", "messages", messagesAfter)

	return &agentctx.CompactionResult{
		Summary:        summary,
		TokensBefore:   tokensBefore,
		TokensAfter:    tokensAfter,
		MessagesBefore: messagesBefore,
		MessagesAfter:  messagesAfter,
		Type:           "major",
	}, nil
}

// cleanOldRuntimeState removes all but the last runtime_state message from the
// given slice. During compaction, older runtime_state snapshots are stale — only
// the most recent one carries useful telemetry. Cleaning them unconditionally
// keeps pkg/compact independent of cache mode logic.
func cleanOldRuntimeState(messages []agentctx.AgentMessage) []agentctx.AgentMessage {
	lastIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Metadata != nil && messages[i].Metadata.Kind == "runtime_state" {
			lastIdx = i
			break
		}
	}

	if lastIdx == -1 {
		return messages
	}

	var result []agentctx.AgentMessage
	for i, msg := range messages {
		if msg.Metadata != nil && msg.Metadata.Kind == "runtime_state" && i != lastIdx {
			continue
		}
		result = append(result, msg)
	}
	return result
}

// ShouldCompact determines if context should be compressed using AgentContext.
func (c *Compactor) ShouldCompact(_ context.Context, agentCtx *agentctx.AgentContext) bool {
	if !c.config.AutoCompact {
		return false
	}

	threshold := c.CalculateDynamicThreshold()
	if threshold > 0 {
		tokens := agentCtx.EstimateTokens()
		return tokens >= threshold
	}
	return false
}
