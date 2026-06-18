package compact

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// GenerateSummary generates a structured summary of messages using the LLM.
func (c *Compactor) GenerateSummary(messages []agentctx.AgentMessage) (string, error) {
	return c.GenerateSummaryWithPrevious(messages, c.systemPrompt, "", nil, "")
}

// GenerateSummaryWithPrevious generates a structured summary, optionally
// updating a previous summary.
//
// To maximise prompt-cache reuse, the request mirrors a normal agent turn:
//
//	SystemPrompt: systemPrompt              (cached)
//	Tools:        agent tools               (cached)
//	Messages:     [contextPrefix] + old messages + summarisation instruction
//
// The contextPrefix (skills + AGENTS.md) is injected as a user message before
// the first old message, exactly like the agent loop does. The old messages
// are a prefix of the full conversation, so the entire prefix
// [system_prompt + tools + contextPrefix + old_messages] is served from cache.
// Only the trailing summarisation instruction is new.
func (c *Compactor) GenerateSummaryWithPrevious(messages []agentctx.AgentMessage, systemPrompt string, contextPrefix string, tools []agentctx.Tool, previousSummary string) (string, error) {
	if len(messages) == 0 {
		return "", fmt.Errorf("no messages to summarize")
	}

	// Guard: cap old messages to fit within ~40% of context window, leaving
	// room for system prompt, tools, instruction, and the summary response.
	// For large sessions oldMessages can be very large; without this guard
	// the request exceeds the model's context window and compaction fails.
	if c.contextWindow > 0 {
		messages = c.truncateForSummary(messages)
	}

	// Convert old messages to the same structured LLMMessage format used by
	// normal agent turns — this is what makes the prefix cache hit work.
	llmMessages := agentctx.ConvertMessagesToLLM(messages)
	if len(llmMessages) == 0 {
		if strings.TrimSpace(previousSummary) != "" {
			return previousSummary, nil
		}
		return "", fmt.Errorf("no agent-visible messages to summarize")
	}

	// Inject contextPrefix (skills + AGENTS.md) before the first user message,
	// mirroring insertBeforeFirstUserMessage in the agent loop.
	// This must match the agent loop's message structure exactly for prefix
	// cache to hit on the [contextPrefix + old messages] segment.
	if strings.TrimSpace(contextPrefix) != "" {
		llmMessages = agentctx.InsertBeforeFirstUserMessage(llmMessages, llm.LLMMessage{
			Role:    "user",
			Content: contextPrefix,
		})
	}

	// Build the summarisation instruction as a trailing user message.
	// Everything before this point is identical to a normal agent request,
	// so it is served from the provider's prompt cache.
	var instruction string
	if previousSummary != "" {
		instruction = summarizationSystemPrompt + "\n\n" +
			fmt.Sprintf(updateSummarizationPrompt, previousSummary, "(see conversation messages above)")
	} else {
		instruction = summarizationSystemPrompt + "\n\n" + summarizationPrompt
	}
	llmMessages = append(llmMessages, llm.LLMMessage{Role: "user", Content: instruction})

	llmCtx := llm.LLMContext{
		SystemPrompt: systemPrompt,
		Messages:     llmMessages,
		Tools:        agentctx.ConvertToolsToLLM(tools),
	}

	const maxRetries = 3
	const totalTimeout = 5 * time.Minute
	const chunkTimeout = 2 * time.Minute
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
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

// truncateForSummary drops oldest messages until the remaining fit within
// ~40% of the model's context window. This prevents oversized requests
// that would exceed the context window and fail compaction.
func (c *Compactor) truncateForSummary(messages []agentctx.AgentMessage) []agentctx.AgentMessage {
	const maxContextFraction = 0.4
	maxTokens := int(float64(c.contextWindow) * maxContextFraction)
	if maxTokens <= 0 {
		return messages
	}

	// Quick check: sum token estimates across all messages.
	totalTokens := 0
	for _, msg := range messages {
		totalTokens += estimateMessageTokens(msg)
	}
	if totalTokens <= maxTokens {
		return messages
	}

	// Drop oldest messages to fit within budget. splitMessagesByTokenBudget
	// walks from the newest backwards, so recent = the suffix that fits.
	dropped, kept := splitMessagesByTokenBudget(messages, maxTokens)
	if len(dropped) > 0 {
		slog.Info("[Compact] Truncated old messages for summary",
			"dropped", len(dropped), "kept", len(kept),
			"total_tokens", totalTokens, "max_tokens", maxTokens)
	}

	// Fix tool_call/tool_result pairing that the split may have broken.
	if c.config.GracePeriod > 0 {
		kept = c.ensureToolCallPairingWithGrace(dropped, kept)
	} else {
		kept = ensureToolCallPairing(dropped, kept)
	}
	return kept
}
