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
	"github.com/tiancaiamao/ai/pkg/prompt"
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

	// Filter agent-visible messages.
	visible := make([]agentctx.AgentMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.IsAgentVisible() {
			visible = append(visible, msg)
		}
	}
	if len(visible) == 0 {
		if strings.TrimSpace(previousSummary) != "" {
			return previousSummary, nil
		}
		return "", fmt.Errorf("no agent-visible messages to summarize")
	}

	// Truncation safeguard: if the visible messages exceed ~50% of the
	// context window, drop the oldest ones to prevent the summary request
	// from overflowing. This leaves room for the system prompt, prefix,
	// instruction, and the summary response.
	if c.contextWindow > 0 {
		maxTokens := int(float64(c.contextWindow) * 0.5)
		originalCount := len(visible)
		for len(visible) > 2 && c.EstimateTokens(visible) > maxTokens {
			visible = visible[1:]
		}
		if dropped := originalCount - len(visible); dropped > 0 {
			slog.Info("[Compact] Truncated messages for summarization",
				"original", originalCount, "remaining", len(visible),
				"context_window", c.contextWindow, "max_tokens", maxTokens)
		}
	}

	// Build cache-friendly LLM messages. Instead of serializing the
	// conversation into text under a dedicated system prompt, we reuse the
	// main agent's system prompt and real message format. This makes the
	// summarization request share a prefix with the main conversation
	// (system prompt + context prefix + real messages), maximizing provider
	// prefix-cache hits — only the trailing compact instruction is new.
	var llmMessages []llm.LLMMessage
	if c.messageConverter != nil {
		llmMessages = c.messageConverter(visible)
	} else {
		llmMessages = convertAgentMessagesToLLM(visible)
	}

	// Prepend the agent context prefix (skills + AGENTS.md) exactly like the
	// main loop, so the message prefix matches the main conversation.
	if c.agentContextPrefix != "" {
		llmMessages = insertBeforeFirstUserMessage(llmMessages, llm.LLMMessage{
			Role:    "user",
			Content: c.agentContextPrefix,
		})
	}

	// Append the summarization instruction as a trailing user message.
	// The format requirements (formerly a dedicated system prompt) are moved
	// here so the system prompt stays identical to the main conversation.
	instruction := summarizationSystemPrompt + "\n\n" + summarizationPrompt
	if previousSummary != "" {
		instruction = summarizationSystemPrompt + "\n\n" +
			"Update the existing summary with the conversation above. Preserve ALL mandatory sections.\n\n" +
			"<previous-summary>\n" + previousSummary + "\n</previous-summary>"
	}
	llmMessages = append(llmMessages, llm.LLMMessage{
		Role:    "user",
		Content: instruction,
	})

	// Use the main agent's system prompt (identical to the main conversation)
	// for cache reuse; fall back to the compact system prompt if not set.
	systemPrompt := c.agentSystemPrompt
	if systemPrompt == "" {
		systemPrompt = summarizationSystemPrompt
	} else if !c.model.Reasoning {
		// Mirror the main agent loop (llm_stream.go): append the thinking
		// instruction so the system prompt matches exactly, preserving the
		// provider prefix cache from the first token.
		if instruction := prompt.ThinkingInstruction(c.thinkingLevel); instruction != "" {
			systemPrompt = systemPrompt + "\n\n" + instruction
		}
	}

	llmCtx := llm.LLMContext{
		SystemPrompt: systemPrompt,
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

// convertAgentMessagesToLLM is a fallback message converter used when no
// converter is injected via SetAgentLLMContext. Production code injects
// agent.ConvertMessagesToLLM to guarantee byte-identical output with the main
// conversation (required for prefix-cache hits). This fallback handles the
// common cases (text, thinking, tool calls, tool results) but omits
// sanitizeToolCallProtocol. This is acceptable because production code always
// injects agent.ConvertMessagesToLLM (which includes sanitize) via
// SetAgentLLMContext. This fallback is only used in tests and during the
// brief window before initialization.
func convertAgentMessagesToLLM(messages []agentctx.AgentMessage) []llm.LLMMessage {
	llmMessages := make([]llm.LLMMessage, 0, len(messages))
	for _, msg := range messages {
		if !msg.IsAgentVisible() {
			continue
		}
		role := msg.Role
		if role == "toolResult" {
			role = "tool"
		}
		llmMsg := llm.LLMMessage{Role: role}
		for _, block := range msg.Content {
			switch b := block.(type) {
			case agentctx.TextContent:
				llmMsg.Content = b.Text
			case agentctx.ThinkingContent:
				llmMsg.Thinking = b.Thinking
			}
		}
		if msg.Role == "assistant" {
			toolCalls := msg.ExtractToolCalls()
			if len(toolCalls) > 0 {
				llmMsg.ToolCalls = make([]llm.ToolCall, len(toolCalls))
				for i, tc := range toolCalls {
					argsJSON, _ := json.Marshal(tc.Arguments)
					llmMsg.ToolCalls[i] = llm.ToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: llm.FunctionCall{
							Name:      tc.Name,
							Arguments: string(argsJSON),
						},
					}
				}
			}
		}
		if msg.Role == "toolResult" {
			llmMsg.ToolCallID = msg.ToolCallID
			llmMsg.Content = msg.ExtractText()
		}
		llmMessages = append(llmMessages, llmMsg)
	}
	return llmMessages
}

// insertBeforeFirstUserMessage inserts msg immediately before the first
// user-role message. Mirrors agent.insertBeforeFirstUserMessage so the
// context prefix sits at the same position as in the main conversation,
// keeping the prefix cache-friendly.
func insertBeforeFirstUserMessage(messages []llm.LLMMessage, msg llm.LLMMessage) []llm.LLMMessage {
	if len(messages) == 0 {
		return []llm.LLMMessage{msg}
	}
	firstUserIdx := -1
	for i, m := range messages {
		if m.Role == "user" {
			firstUserIdx = i
			break
		}
	}
	if firstUserIdx == -1 {
		result := make([]llm.LLMMessage, 0, len(messages)+1)
		result = append(result, msg)
		result = append(result, messages...)
		return result
	}
	result := make([]llm.LLMMessage, 0, len(messages)+1)
	result = append(result, messages[:firstUserIdx]...)
	result = append(result, msg)
	result = append(result, messages[firstUserIdx:]...)
	return result
}
