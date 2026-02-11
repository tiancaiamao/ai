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
	ToolCallCutoff   int  // Summarize oldest tool outputs when visible tool calls exceed this
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
		ToolCallCutoff:   10,    // Summarize tool outputs after 10 visible tool results
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

	recentMessages = compactToolResultsInRecent(recentMessages, c.config.ToolCallCutoff)
	newMessages = append(newMessages, recentMessages...)

	slog.Info("[Compact] Compressed to messages", "count", len(newMessages))

	return newMessages, nil
}

const summarizationSystemPrompt = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI coding assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`

const summarizationPrompt = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

const updateSummarizationPrompt = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// GenerateSummary generates a structured summary of messages using the LLM.
func (c *Compactor) GenerateSummary(messages []agent.AgentMessage) (string, error) {
	return c.GenerateSummaryWithPrevious(messages, "")
}

// GenerateSummaryWithPrevious generates a structured summary, optionally updating a previous summary.
func (c *Compactor) GenerateSummaryWithPrevious(messages []agent.AgentMessage, previousSummary string) (string, error) {
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
func (c *Compactor) EstimateTokens(messages []agent.AgentMessage) int {
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
	if !msg.IsAgentVisible() {
		return 0
	}

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

// EstimateMessageTokens estimates token usage for a single message.
func EstimateMessageTokens(msg agent.AgentMessage) int {
	return estimateMessageTokens(msg)
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

func serializeConversation(messages []agent.AgentMessage) string {
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
				case agent.TextContent:
					if b.Text != "" {
						textParts = append(textParts, b.Text)
					}
				case agent.ThinkingContent:
					if b.Thinking != "" {
						thinkingParts = append(thinkingParts, b.Thinking)
					}
				case agent.ToolCallContent:
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

func projectMessagesForSummary(messages []agent.AgentMessage) []agent.AgentMessage {
	projected := make([]agent.AgentMessage, 0, len(messages))
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
		copyMsg.Content = []agent.ContentBlock{
			agent.TextContent{Type: "text", Text: toolText},
		}
		projected = append(projected, copyMsg)
	}
	return projected
}

func compactToolResultsInRecent(messages []agent.AgentMessage, cutoff int) []agent.AgentMessage {
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

	compacted := append([]agent.AgentMessage{}, messages...)
	lines := make([]string, 0, excess)

	for i := 0; i < excess; i++ {
		idx := visibleToolIndexes[i]
		original := compacted[idx]
		compacted[idx] = original.WithVisibility(false, original.IsUserVisible()).WithKind("tool_result_archived")
		lines = append(lines, compactionToolDigestLine(original))
	}

	digest := "[Compaction tool digest]\n" + strings.Join(lines, "\n")
	compacted = append(compacted, agent.NewUserMessage(digest).WithVisibility(true, false).WithKind("tool_summary"))
	return compacted
}

func compactionToolDigestLine(msg agent.AgentMessage) string {
	status := "ok"
	if msg.IsError {
		status = "error"
	}

	name := strings.TrimSpace(msg.ToolName)
	if name == "" {
		name = "unknown"
	}

	text := strings.TrimSpace(extractText(msg))
	if text == "" {
		text = "(empty output)"
	}
	text = strings.ReplaceAll(text, "\n", " ")
	text = trimRunes(text, 200)

	return fmt.Sprintf("- %s (%s): %s", name, status, text)
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

func extractText(msg agent.AgentMessage) string {
	var b strings.Builder
	for _, block := range msg.Content {
		if tc, ok := block.(agent.TextContent); ok && tc.Text != "" {
			b.WriteString(tc.Text)
		}
	}
	if b.Len() == 0 {
		return msg.ExtractText()
	}
	return b.String()
}
