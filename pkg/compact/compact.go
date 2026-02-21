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
	AutoCompact         bool   // Whether to automatically compact
}

// DefaultConfig returns default compression configuration.
func DefaultConfig() *Config {
	return &Config{
		MaxMessages:         50,    // Compact after 50 messages
		MaxTokens:           8000,  // Compact after ~8000 tokens (fallback)
		KeepRecent:          5,     // Keep last 5 messages uncompressed
		KeepRecentTokens:    20000, // Keep ~20k tokens from the recent context
		ReserveTokens:       16384, // Reserve tokens for responses when using context window
		ToolCallCutoff:      10,    // Summarize tool outputs after 10 visible tool results
		ToolSummaryStrategy: "llm",
		AutoCompact:         true,
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

	// Message-count and token-limit checks are both valid triggers.
	if c.config.MaxMessages > 0 && len(messages) >= c.config.MaxMessages {
		return true
	}

	// Token limit (context window or explicit max tokens)
	threshold := c.calculateDynamicThreshold()
	if threshold > 0 {
		tokens := c.EstimateContextTokens(messages)
		return tokens >= threshold
	}
	return false
}

// calculateDynamicThreshold calculates the compaction threshold based on context window.
// For models with large context windows (e.g., 128k), this allows much more context
// before triggering compaction, rather than using a fixed 8000 token limit.
func (c *Compactor) calculateDynamicThreshold() int {
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
		if threshold := c.calculateDynamicThreshold(); threshold > 0 {
			maxKeep := int(float64(threshold) * 0.3)
			if budget > maxKeep && maxKeep > 0 {
				budget = maxKeep
			}
		}

		return budget
	}

	// Calculate based on threshold
	threshold := c.calculateDynamicThreshold()
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

// Compact compresses the context by summarizing old messages.
func (c *Compactor) Compact(messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	// Use dynamic keep-recent budget
	keepRecentTokens := c.calculateKeepRecentBudget()
	var oldMessages []agent.AgentMessage
	var recentMessages []agent.AgentMessage
	if keepRecentTokens > 0 {
		oldMessages, recentMessages = splitMessagesByTokenBudget(messages, keepRecentTokens)
		if len(oldMessages) == 0 {
			return messages, nil
		}
		slog.Info("[Compact] Compressing messages",
			"count", len(messages),
			"keepTokens", keepRecentTokens,
			"threshold", c.calculateDynamicThreshold(),
			"contextWindow", c.contextWindow)
	} else {
		keepCount := c.keepRecentMessages()
		if len(messages) <= keepCount {
			return messages, nil
		}
		slog.Info("[Compact] Compressing messages",
			"count", len(messages),
			"keepRecent", keepCount,
			"threshold", c.calculateDynamicThreshold())
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

const summarizationSystemPrompt = `You are a context summarization assistant for a coding agent. Your task is to extract and preserve CRITICAL information from a conversation for continuation.

CRITICAL INFORMATION (preserve at all costs):
- EXACT file paths (e.g., "pkg/agent/loop.go:245")
- EXACT error messages and stack traces
- EXACT function/variable/class names
- EXACT commands executed
- User's explicit requirements and constraints

DISCARDABLE INFORMATION:
- Pleasantries ("sure!", "I'll help you with that")
- Redundant explanations
- Failed approaches that were abandoned
- Intermediate thinking that led to successful outcomes

Output ONLY the structured summary. Do NOT continue the conversation.`

const summarizationPrompt = `Summarize this coding conversation for context preservation.

## Current Task (MOST IMPORTANT)
[What is being actively worked on RIGHT NOW? Be specific about the exact goal.]

## Files Involved
[List EXACT file paths with brief notes:]
- path/to/file.go: [what was done/read/needs to be done]
- path/to/another.py: [status]

## Key Code Elements
[Important names discovered/created:]
- Functions: [names and purposes]
- Variables: [names and types if relevant]
- Classes/Types: [names and purposes]

## Errors Encountered
[If any errors occurred:]
- Error: [EXACT error message]
- Cause: [what caused it]
- Fix: [how it was fixed, if fixed]
- Status: [resolved/unresolved]

## Decisions Made
[Important technical choices:]
- Decision: [what was decided]
- Reason: [why - crucial for continuity]

## Next Steps
[Ordered list of immediate actions:]
1. [specific next action]
2. [following action]

## User Requirements
[Any explicit requirements or constraints from the user]

Rules:
- Preserve EXACT text for paths, errors, names (use quotes if needed)
- Keep under 800 tokens
- Omit pleasantries and redundant explanations`

const updateSummarizationPrompt = `Update the existing summary with NEW conversation messages.

PREVIOUS SUMMARY:
<previous-summary>
%s
</previous-summary>

NEW MESSAGES:
%s

UPDATE RULES:
1. PRESERVE all existing information (especially exact paths, errors, names)
2. ADD new files, errors, decisions discovered
3. UPDATE "Current Task" if focus changed
4. MOVE completed items in "Next Steps" to appropriate sections
5. MARK errors as resolved if fixed in new messages
6. PRESERVE exact text - do not paraphrase paths, errors, or code elements

Output the updated summary using the same format.`

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
	ctx := context.Background()
	summarySpan := traceevent.StartSpan(ctx, "tool_summary_batch", traceevent.CategoryTool,
		traceevent.Field{Key: "mode", Value: "compaction_digest"},
		traceevent.Field{Key: "visible_tool_results", Value: len(visibleToolIndexes)},
		traceevent.Field{Key: "cutoff", Value: cutoff},
		traceevent.Field{Key: "archived_count", Value: excess},
	)

	compacted := append([]agent.AgentMessage{}, messages...)
	lines := make([]string, 0, excess)

	for i := 0; i < excess; i++ {
		idx := visibleToolIndexes[i]
		original := compacted[idx]
		compacted[idx] = original.WithVisibility(false, original.IsUserVisible()).WithKind("tool_result_archived")
		lines = append(lines, compactionToolDigestLine(original))
	}

	digest := "[ARCHIVED_TOOL_CONTEXT: " + strings.Join(lines, " ") + "]"
	compacted = append(compacted, newToolSummaryContextMessage(digest))
	summarySpan.AddField("summary_chars", len([]rune(digest)))
	summarySpan.End()
	return compacted
}

func newToolSummaryContextMessage(text string) agent.AgentMessage {
	msg := agent.NewAssistantMessage()
	msg.Content = []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: text},
	}
	return msg.WithVisibility(true, false).WithKind("tool_summary")
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
