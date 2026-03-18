package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"log/slog"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// Config contains configuration for context compression.
type Config struct {
	MaxMessages         int    // Maximum messages before compression
	MaxTokens           int    // Approximate token limit before compression
	KeepRecent          int    // Number of recent messages to keep
	KeepRecentTokens    int    // Token budget to keep from the most recent messages
	ReserveTokens       int    // Tokens to reserve when using context window
	ToolCallCutoff      int    // Summarize oldest tool outputs when visible tool calls exceed this
	ToolSummaryStrategy string // llm, heuristic, off
	// ToolSummaryAutomation controls when background tool-output summary runs:
	// - off: disable automatic tool-output summary
	// - fallback: only run when compactor pressure fallback is triggered
	// - always: run whenever ToolCallCutoff is exceeded
	ToolSummaryAutomation string
	AutoCompact           bool // Whether to automatically compact
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
func (c *Compactor) ShouldCompact(messages []agentctx.AgentMessage) bool {
	if !c.config.AutoCompact {
		return false
	}

	// Phase 2 manual mode: only token-pressure fallback triggers auto compaction.
	// Message-count based compaction is intentionally disabled.
	threshold := c.CalculateDynamicThreshold()
	if threshold > 0 {
		tokens := c.EstimateContextTokens(messages)
		return tokens >= threshold
	}
	return false
}

// calculateDynamicThreshold calculates the compaction threshold based on context window.
// For models with large context windows (e.g., 128k), this allows much more context
// before triggering compaction, rather than using a fixed 8000 token limit.
// CalculateDynamicThreshold returns the dynamic compaction threshold based on context window.
// Exported for use by llm_context_decision tool to provide feedback when compact is rejected.
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

// CompactionResult contains the result of a compaction operation.
type CompactionResult struct {
	Summary      string               // The generated summary
	Messages     []agentctx.AgentMessage // The compressed message list
	TokensBefore int                  // Token count before compaction
	TokensAfter  int                  // Token count after compaction
}

// Compact compresses the context by summarizing old messages.
// If previousSummary is non-empty, it will be used to incrementally update the summary.
func (c *Compactor) Compact(messages []agentctx.AgentMessage, previousSummary string) (*CompactionResult, error) {
	if len(messages) == 0 {
		return &CompactionResult{
			Messages:     messages,
			TokensBefore: 0,
			TokensAfter:  0,
		}, nil
	}

	// Use dynamic keep-recent budget
	keepRecentTokens := c.calculateKeepRecentBudget()
	var oldMessages []agentctx.AgentMessage
	var recentMessages []agentctx.AgentMessage
	if keepRecentTokens > 0 {
		oldMessages, recentMessages = splitMessagesByTokenBudget(messages, keepRecentTokens)
		if len(oldMessages) == 0 {
			return &CompactionResult{
				Messages:     messages,
				TokensBefore: c.EstimateContextTokens(messages),
				TokensAfter:  c.EstimateContextTokens(messages),
			}, nil
		}
		slog.Info("[Compact] Compressing messages",
			"count", len(messages),
			"keepTokens", keepRecentTokens,
			"threshold", c.CalculateDynamicThreshold(),
			"contextWindow", c.contextWindow,
			"hasPreviousSummary", previousSummary != "")
	} else {
		keepCount := c.keepRecentMessages()
		if len(messages) <= keepCount {
			return &CompactionResult{
				Messages:     messages,
				TokensBefore: c.EstimateContextTokens(messages),
				TokensAfter:  c.EstimateContextTokens(messages),
			}, nil
		}
		slog.Info("[Compact] Compressing messages",
			"count", len(messages),
			"keepRecent", keepCount,
			"threshold", c.CalculateDynamicThreshold(),
			"hasPreviousSummary", previousSummary != "")
		splitIndex := len(messages) - keepCount
		oldMessages = messages[:splitIndex]
		recentMessages = messages[splitIndex:]
	}

	// Generate summary of old messages (with previous summary for incremental update)
	tokensBefore := c.EstimateContextTokens(messages)
	summary, err := c.GenerateSummaryWithPrevious(oldMessages, previousSummary)
	if err != nil {
		return nil, fmt.Errorf("failed to generate summary: %w", err)
	}

	slog.Info("[Compact] Generated summary", "chars", len(summary), "hasPrevious", previousSummary != "")

	// Ensure tool_call and tool_result pairing is preserved
	recentMessages = ensureToolCallPairing(oldMessages, recentMessages)

	// Create new context with summary + recent messages
	newMessages := []agentctx.AgentMessage{
		agentctx.NewUserMessage(fmt.Sprintf("[Previous conversation summary]\n\n%s", summary)),
	}

	recentMessages = compactToolResultsInRecent(recentMessages, c.config.ToolCallCutoff)
	newMessages = append(newMessages, recentMessages...)

	tokensAfter := c.EstimateContextTokens(newMessages)
	slog.Info("[Compact] Compressed to messages", "count", len(newMessages))

	return &CompactionResult{
		Summary:      summary,
		Messages:     newMessages,
		TokensBefore: tokensBefore,
		TokensAfter:  tokensAfter,
	}, nil
}

var (
	summarizationSystemPrompt = prompt.CompactSystemPrompt()
	summarizationPrompt       = prompt.CompactSummarizePrompt()
	updateSummarizationPrompt = prompt.CompactUpdatePrompt()
)

// GenerateSummary generates a structured summary of messages using the LLM.
func (c *Compactor) GenerateSummary(messages []agentctx.AgentMessage) (string, error) {
	return c.GenerateSummaryWithPrevious(messages, "")
}

// GenerateSummaryWithPrevious generates a structured summary, optionally updating a previous summary.
func (c *Compactor) GenerateSummaryWithPrevious(messages []agentctx.AgentMessage, previousSummary string) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("no messages to summarize")
	}

	projected := projectMessagesForSummary(messages)
	if len(projected) == 0 {
		if strings.TrimSpace(previousSummary) != "" {
			return previousSummary, nil
		}
		return "", fmt.Errorf("no agent-visible messages to summarize")
	}

	conversationText := serializeConversation(projected)
	promptText := fmt.Sprintf("<conversation>\\n%s\\n</conversation>\\n\\n", conversationText)
	basePrompt := summarizationPrompt
	if previousSummary != "" {
		promptText += fmt.Sprintf("<previous-summary>\\n%s\\n</previous-summary>\\n\\n", previousSummary)
		basePrompt = updateSummarizationPrompt
	}
	promptText += basePrompt

	llmMessages := []llm.LLMMessage{
		{Role: "user", Content: promptText},
	}

	llmCtx := llm.LLMContext{
		SystemPrompt: summarizationSystemPrompt,
		Messages:     llmMessages,
	}

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
	if strings.TrimSpace(result) == "" {
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

// EstimateContextTokens estimates context tokens using usage when available.
func (c *Compactor) EstimateContextTokens(messages []agentctx.AgentMessage) int {
	systemPromptTokens := 0
	if c != nil && strings.TrimSpace(c.systemPrompt) != "" {
		systemPromptTokens = int(math.Ceil(float64(len(c.systemPrompt)) / 4.0))
	}

	usageTokens, lastIndex := lastAssistantUsageTokens(messages)
	if lastIndex >= 0 {
		trailingTokens := 0
		for i := lastIndex + 1; i < len(messages); i++ {
			trailingTokens += estimateMessageTokens(messages[i])
		}
		return usageTokens + trailingTokens + systemPromptTokens
	}
	return c.EstimateTokens(messages) + systemPromptTokens
}

// CompactIfNeeded compacts the context if it exceeds limits.
// Returns the compacted messages and the summary (if compaction occurred).
func (c *Compactor) CompactIfNeeded(messages []agentctx.AgentMessage, previousSummary string) ([]agentctx.AgentMessage, *CompactionResult, error) {
	if c.ShouldCompact(messages) {
		result, err := c.Compact(messages, previousSummary)
		if err != nil {
			return nil, nil, err
		}
		return result.Messages, result, nil
	}
	return messages, nil, nil
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

func splitMessagesByTokenBudget(
	messages []agentctx.AgentMessage,
	tokenBudget int,
) ([]agentctx.AgentMessage, []agentctx.AgentMessage) {
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

func serializeConversation(messages []agentctx.AgentMessage) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if !msg.IsAgentVisible() {
			continue
		}

		switch msg.Role {
		case "user":
			if text := extractText(msg); text != "" {
				parts = append(parts, "[User]: "+text)
			}
		case "assistant":
			textParts := make([]string, 0)
			thinkingParts := make([]string, 0)
			toolCalls := make([]string, 0)
			for _, block := range msg.Content {
				switch b := block.(type) {
				case agentctx.TextContent:
					if b.Text != "" {
						textParts = append(textParts, b.Text)
					}
				case agentctx.ThinkingContent:
					if b.Thinking != "" {
						thinkingParts = append(thinkingParts, b.Thinking)
					}
				case agentctx.ToolCallContent:
					args := ""
					if b.Arguments != nil {
						if raw, err := json.Marshal(b.Arguments); err == nil {
							args = string(raw)
						}
					}
					if args != "" {
						toolCalls = append(toolCalls, fmt.Sprintf("%s(%s)", b.Name, args))
					} else {
						toolCalls = append(toolCalls, fmt.Sprintf("%s()", b.Name))
					}
				}
			}
			if len(thinkingParts) > 0 {
				parts = append(parts, "[Assistant thinking]: "+strings.Join(thinkingParts, "\n"))
			}
			if len(textParts) > 0 {
				parts = append(parts, "[Assistant]: "+strings.Join(textParts, "\n"))
			}
			if len(toolCalls) > 0 {
				parts = append(parts, "[Assistant tool calls]: "+strings.Join(toolCalls, "; "))
			}
		case "toolResult":
			if text := extractText(msg); text != "" {
				toolName := strings.TrimSpace(msg.ToolName)
				if toolName == "" {
					parts = append(parts, "[Tool result]: "+text)
				} else {
					parts = append(parts, "[Tool result "+toolName+"]: "+text)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func projectMessagesForSummary(messages []agentctx.AgentMessage) []agentctx.AgentMessage {
	projected := make([]agentctx.AgentMessage, 0, len(messages))
	for _, msg := range messages {
		if !msg.IsAgentVisible() {
			continue
		}

		if msg.Role != "toolResult" {
			projected = append(projected, msg)
			continue
		}

		copyMsg := msg
		toolText := strings.TrimSpace(extractText(msg))
		if toolText == "" {
			toolText = "(empty output)"
		}
		toolText = trimTextWithTail(toolText, 1800)
		copyMsg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: toolText},
		}
		projected = append(projected, copyMsg)
	}
	return projected
}

func compactToolResultsInRecent(messages []agentctx.AgentMessage, cutoff int) []agentctx.AgentMessage {
	if cutoff <= 0 || len(messages) == 0 {
		return messages
	}

	visibleToolIndexes := make([]int, 0)
	for i, msg := range messages {
		if msg.Role == "toolResult" && msg.IsAgentVisible() {
			visibleToolIndexes = append(visibleToolIndexes, i)
		}
	}

	excess := len(visibleToolIndexes) - cutoff
	if excess <= 0 {
		return messages
	}
	ctx := context.Background()
	summarySpan := traceevent.StartSpan(ctx, "tool_summary_batch", traceevent.CategoryTool,
		traceevent.Field{Key: "mode", Value: "compaction_digest"},
		traceevent.Field{Key: "visible_tool_results", Value: len(visibleToolIndexes)},
		traceevent.Field{Key: "cutoff", Value: cutoff},
		traceevent.Field{Key: "archived_count", Value: excess},
	)

	compacted := append([]agentctx.AgentMessage{}, messages...)

	// Hide excess tool_results from agent (but keep visible to user)
	// Unlike before, we don't hide tool_calls or add a summary message,
	// which avoids protocol violations.
	for i := 0; i < excess; i++ {
		idx := visibleToolIndexes[i]
		original := compacted[idx]
		compacted[idx] = original.WithVisibility(false, original.IsUserVisible()).WithKind("tool_result_archived")
	}

	summarySpan.End()
	return compacted
}

func boolPtr(v bool) *bool {
	b := v
	return &b
}

func trimRunes(input string, limit int) string {
	if limit <= 0 {
		return input
	}
	runes := []rune(input)
	if len(runes) <= limit {
		return input
	}
	return string(runes[:limit])
}

func trimTextWithTail(input string, maxRunes int) string {
	if maxRunes <= 0 {
		return input
	}
	runes := []rune(input)
	if len(runes) <= maxRunes {
		return input
	}

	head := maxRunes * 2 / 3
	tail := maxRunes - head
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}

	return string(runes[:head]) + "\n... (truncated) ...\n" + string(runes[len(runes)-tail:])
}

func extractText(msg agentctx.AgentMessage) string {
	var b strings.Builder
	for _, block := range msg.Content {
		if tc, ok := block.(agentctx.TextContent); ok && tc.Text != "" {
			b.WriteString(tc.Text)
		}
	}
	if b.Len() == 0 {
		return msg.ExtractText()
	}
	return b.String()
}

// ensureToolCallPairing ensures that tool_call and tool_result messages remain paired.
// Two cases must be handled:
// 1. tool_result in recentMessages but tool_call in oldMessages -> hide tool_result
// 2. tool_call in recentMessages but tool_result in oldMessages -> hide tool_call
//
// This prevents "tool call and result not match" errors after compaction.
func ensureToolCallPairing(oldMessages, recentMessages []agentctx.AgentMessage) []agentctx.AgentMessage {
	if len(recentMessages) == 0 {
		return recentMessages
	}

	// Collect all tool_call IDs from oldMessages (for case 1)
	oldToolCallIDs := make(map[string]bool)
	// Collect all tool_result IDs from oldMessages (for case 2)
	oldToolResultIDs := make(map[string]bool)

	for _, msg := range oldMessages {
		if msg.Role == "assistant" {
			for _, tc := range msg.ExtractToolCalls() {
				oldToolCallIDs[tc.ID] = true
			}
		}
		if msg.Role == "toolResult" && msg.ToolCallID != "" {
			oldToolResultIDs[msg.ToolCallID] = true
		}
	}

	// If no tool-related messages in oldMessages, nothing to fix
	if len(oldToolCallIDs) == 0 && len(oldToolResultIDs) == 0 {
		return recentMessages
	}

	keptMessages := make([]agentctx.AgentMessage, 0, len(recentMessages))
	archivedToolResultCount := 0
	archivedToolCallCount := 0

	for _, msg := range recentMessages {
		// Case 1: tool_result in recentMessages but its tool_call is in oldMessages
		if msg.Role == "toolResult" && msg.ToolCallID != "" {
			if oldToolCallIDs[msg.ToolCallID] {
				// This tool_result's call is in oldMessages - hide it to prevent mismatch
				archivedMsg := msg.WithVisibility(false, msg.IsUserVisible()).WithKind("tool_result_archived")
				keptMessages = append(keptMessages, archivedMsg)
				archivedToolResultCount++
				continue
			}
		}

		// Case 2: tool_call in recentMessages but its tool_result is in oldMessages
		if msg.Role == "assistant" && len(msg.ExtractToolCalls()) > 0 {
			// Check if any tool_call has its result in oldMessages
			hasOrphanToolCall := false
			for _, tc := range msg.ExtractToolCalls() {
				if oldToolResultIDs[tc.ID] {
					hasOrphanToolCall = true
					break
				}
			}

			if hasOrphanToolCall {
				// Hide the entire assistant message if it contains orphan tool_calls
				// This is safer than selectively hiding tool_calls within a message
				archivedMsg := msg.WithVisibility(false, msg.IsUserVisible()).WithKind("tool_call_archived")
				keptMessages = append(keptMessages, archivedMsg)
				archivedToolCallCount++
				continue
			}
		}

		keptMessages = append(keptMessages, msg)
	}

	if archivedToolResultCount > 0 || archivedToolCallCount > 0 {
		slog.Info("[Compact] Fixed tool_call/tool_result pairing",
			"archived_tool_results", archivedToolResultCount,
			"archived_tool_calls", archivedToolCallCount,
			"kept", len(keptMessages))
	}

	return keptMessages
}
// ToContextCompactor adapts this Compactor to implement context.Compactor interface.
// This allows the compact.Compactor to be used where context.Compactor is expected.
func (c *Compactor) ToContextCompactor() agentctx.Compactor {
	return &contextCompactorAdapter{c: c}
}

// contextCompactorAdapter adapts compact.Compactor to context.Compactor interface.
type contextCompactorAdapter struct {
	c *Compactor
}

// ShouldCompact checks if context compression is needed.
func (a *contextCompactorAdapter) ShouldCompact(messages []agentctx.AgentMessage) bool {
	return a.c.ShouldCompact(messages)
}

// Compact performs context compression and returns a context.CompactionResult.
func (a *contextCompactorAdapter) Compact(messages []agentctx.AgentMessage, previousSummary string) (*agentctx.CompactionResult, error) {
	result, err := a.c.Compact(messages, previousSummary)
	if err != nil {
		return nil, err
	}
	return &agentctx.CompactionResult{
		Summary:      result.Summary,
		Messages:     result.Messages,
		TokensBefore: result.TokensBefore,
		TokensAfter:  result.TokensAfter,
	}, nil
}

// CalculateDynamicThreshold returns the token threshold for compaction.
func (a *contextCompactorAdapter) CalculateDynamicThreshold() int {
	return a.c.CalculateDynamicThreshold()
}

// EstimateContextTokens estimates the token count of messages.
func (a *contextCompactorAdapter) EstimateContextTokens(messages []agentctx.AgentMessage) int {
	return a.c.EstimateContextTokens(messages)
}
