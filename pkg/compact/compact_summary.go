package compact

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

var (
	summarizationPrompt = prompt.CompactSummarizePrompt()
)

// GenerateSummary generates a structured summary of messages.
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
//
// The previous compaction summary (if any) is part of oldMessages, so the LLM
// can see it without a separate prompt.
func (c *Compactor) GenerateSummary(goCtx context.Context, messages []agentctx.AgentMessage, systemPrompt string, contextPrefix string, tools []agentctx.Tool) (string, error) {
	span := traceevent.StartSpan(goCtx, "GenerateSummary", traceevent.CategoryEvent)
	defer span.End()

	if len(messages) == 0 {
		return "", fmt.Errorf("no messages to summarize")
	}

	// Convert old messages to the same structured LLMMessage format used by
	// normal agent turns. oldMessages is a strict prefix of the conversation
	// (split off by splitMessagesByTokenBudget in Compact), so it already fits
	// within the context window — no further truncation needed or wanted:
	// truncating here would break prefix-cache alignment.
	if len(agentctx.ConvertMessagesToLLM(messages)) == 0 {
		return "", fmt.Errorf("no agent-visible messages to summarize")
	}

	llmCtx := buildCacheFriendlyLLMContext(messages, systemPrompt, contextPrefix, tools, summarizationPrompt)

	const maxRetries = 3
	const totalTimeout = 5 * time.Minute
	const chunkTimeout = 2 * time.Minute
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(goCtx, totalTimeout)

		llmStream := llm.StreamLLM(ctx, c.model, llmCtx, c.apiKey, chunkTimeout)

		var summary strings.Builder
		var thinking strings.Builder
		var streamErr error
		var doneEvent llm.LLMDoneEvent
		for event := range llmStream.Iterator(ctx) {
			if event.Done {
				break
			}

			switch e := event.Value.(type) {
			case llm.LLMTextDeltaEvent:
				summary.WriteString(e.Delta)
			case llm.LLMThinkingDeltaEvent:
				thinking.WriteString(e.Delta)
			case llm.LLMErrorEvent:
				streamErr = e.Error
			case llm.LLMDoneEvent:
				doneEvent = e
			}
		}
		cancel()

		// Record token usage regardless of success/failure — the LLM call
		// already happened, and usage data is critical for debugging cache
		// behavior even when the summary is empty or the stream errors.
		span.AddField("input_tokens", doneEvent.Usage.InputTokens)
		span.AddField("output_tokens", doneEvent.Usage.OutputTokens)
		span.AddField("total_tokens", doneEvent.Usage.TotalTokens)
		cachedTokens := 0
		if doneEvent.Usage.PromptTokensDetails != nil {
			cachedTokens = doneEvent.Usage.PromptTokensDetails.CachedTokens
		}
		span.AddField("cache_read", cachedTokens)

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
			// Some models (e.g. GLM-5.1) may place the entire response in
			// reasoning_content instead of text content, especially when
			// the agent's system prompt contains conflicting instructions.
			// Fall back to thinking output to avoid discarding a valid summary.
			thinkingStr := thinking.String()
			if strings.TrimSpace(thinkingStr) != "" {
				slog.Warn("[Compact] Summary text was empty, falling back to reasoning_content",
					"thinking_chars", len(thinkingStr), "output_tokens", doneEvent.Usage.OutputTokens)
				result = thinkingStr
			} else {
				return "", fmt.Errorf("empty summary generated")
			}
		}

		span.AddField("summary_chars", len(result))
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
