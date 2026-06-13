package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// GenerateSummary generates a structured summary of messages using the LLM.
func (c *Compactor) GenerateSummary(messages []agentctx.AgentMessage) (string, error) {
	return c.GenerateSummaryWithPrevious(messages, "")
}

// GenerateSummaryWithPrevious generates a structured summary, optionally updating a previous summary.
// It includes retry logic for transient LLM errors and a total timeout to prevent
// the compaction path from hanging indefinitely on network failures.

// GenerateSummaryWithPrevious generates a structured summary, optionally updating a previous summary.
// It includes retry logic for transient LLM errors and a total timeout to prevent
// the compaction path from hanging indefinitely on network failures.
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

	// Guard against sending a conversation that exceeds the model's context
	// window. For large sessions the serialized text can be many megabytes,
	// which the summarization model cannot process. Truncate oldest messages
	// until the conversation fits within ~40% of the model's context window
	// (leaving room for the prompt, system prompt, and response).
	const maxContextFraction = 0.4
	if c.contextWindow > 0 {
		maxTokens := int(float64(c.contextWindow) * maxContextFraction)
		// Each char is roughly 0.25 tokens, so maxBytes ≈ maxTokens * 4
		maxChars := maxTokens * 4
		if len(conversationText) > maxChars {
			truncated := truncateConversationToCharBudget(projected, maxChars)
			conversationText = serializeConversation(truncated)
			slog.Info("[Compact] Truncated conversation for summarization",
				"original_messages", len(projected),
				"truncated_messages", len(truncated),
				"original_chars", len(serializeConversation(projected)),
				"truncated_chars", len(conversationText),
				"context_window", c.contextWindow,
				"max_chars", maxChars)
		}
	}

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

	const maxRetries = 3
	const totalTimeout = 5 * time.Minute
	const chunkTimeout = 2 * time.Minute
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Use a bounded context with timeout instead of context.Background().
		ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)

		llmStream := llm.StreamLLM(ctx, c.model, llmCtx, c.apiKey, chunkTimeout)

		var summary strings.Builder
		var streamErr error
		for event := range llmStream.Iterator(ctx) {
			if event.Done {
				break
			}

			switch e := event.Value.(type) {
			case llm.LLMTextDeltaEvent:
				summary.WriteString(e.Delta)
			case llm.LLMErrorEvent:
				streamErr = e.Error
			}
		}
		cancel()

		if streamErr != nil {
			lastErr = streamErr
			if !llm.IsRetryableError(streamErr) {
				slog.Error("[Compact] Summary generation failed (non-retryable)",
					"attempt", attempt, "error", streamErr)
				return "", fmt.Errorf("failed to generate summary: %w", streamErr)
			}
			if attempt < maxRetries {
				backoff := time.Duration(attempt) * 2 * time.Second
				slog.Warn("[Compact] Summary generation failed (retryable), retrying",
					"attempt", attempt,
					"max_retries", maxRetries,
					"backoff", backoff,
					"error", streamErr)
				time.Sleep(backoff)
			}
			continue
		}

		result := summary.String()
		if strings.TrimSpace(result) == "" {
			return "", fmt.Errorf("empty summary generated")
		}

		return result, nil
	}

	return "", fmt.Errorf("failed to generate summary after %d retries: %w", maxRetries, lastErr)
}

// ContextWindow returns the configured model context window.

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

	// Compaction summary messages should always be in recent messages.
	// We'll find them first and ensure they're included.
	compactionSummaryIndices := make(map[int]struct{})
	for i, msg := range messages {
		if msg.Metadata != nil && msg.Metadata.Kind == "compactionSummary" {
			compactionSummaryIndices[i] = struct{}{}
		}
	}

	used := 0
	start := len(messages)

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		// Skip compaction summaries - they'll be handled specially
		if _, isSummary := compactionSummaryIndices[i]; isSummary {
			continue
		}

		msgTokens := estimateMessageTokens(msg)
		if used+msgTokens > tokenBudget && start != len(messages) {
			break
		}
		used += msgTokens
		start = i
	}

	// Now ensure all compaction summaries are included in recent
	// by moving start to the earliest summary index
	minSummaryIndex := len(messages)
	for i := range compactionSummaryIndices {
		if i < minSummaryIndex {
			minSummaryIndex = i
		}
	}
	if minSummaryIndex < len(messages) && minSummaryIndex < start {
		start = minSummaryIndex
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
			if text := msg.ExtractText(); text != "" {
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
			if text := msg.ExtractText(); text != "" {
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

// truncateConversationToCharBudget keeps the most recent messages whose
// serialized text fits within charBudget. It drops oldest messages first.

// truncateConversationToCharBudget keeps the most recent messages whose
// serialized text fits within charBudget. It drops oldest messages first.
func truncateConversationToCharBudget(messages []agentctx.AgentMessage, charBudget int) []agentctx.AgentMessage {
	if len(messages) == 0 || charBudget <= 0 {
		return messages
	}

	// Walk from the end backwards, accumulating size until we exceed the budget.
	totalChars := 0
	cutoff := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		msgText := serializeSingleMessage(messages[i])
		totalChars += len(msgText)
		if totalChars > charBudget {
			cutoff = i + 1
			break
		}
	}

	if cutoff >= len(messages) {
		return messages
	}
	return messages[cutoff:]
}

// serializeSingleMessage returns the serialized text of a single message.

// serializeSingleMessage returns the serialized text of a single message.
func serializeSingleMessage(msg agentctx.AgentMessage) string {
	switch msg.Role {
	case "user":
		if text := msg.ExtractText(); text != "" {
			return "[User]: " + text
		}
	case "assistant":
		parts := make([]string, 0)
		for _, block := range msg.Content {
			switch b := block.(type) {
			case agentctx.TextContent:
				if b.Text != "" {
					parts = append(parts, b.Text)
				}
			case agentctx.ToolCallContent:
				args := ""
				if b.Arguments != nil {
					if raw, err := json.Marshal(b.Arguments); err == nil {
						args = string(raw)
					}
				}
				if args != "" {
					parts = append(parts, fmt.Sprintf("%s(%s)", b.Name, args))
				} else {
					parts = append(parts, fmt.Sprintf("%s()", b.Name))
				}
			}
		}
		if len(parts) > 0 {
			return "[Assistant]: " + strings.Join(parts, "\n")
		}
	case "toolResult":
		if text := msg.ExtractText(); text != "" {
			toolName := strings.TrimSpace(msg.ToolName)
			if toolName == "" {
				return "[Tool result]: " + text
			}
			return "[Tool result " + toolName + "]: " + text
		}
	}
	return ""
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
		toolText := strings.TrimSpace(msg.ExtractText())
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
