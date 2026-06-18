package compact

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// GenerateSummary generates a structured summary of messages using the LLM.
func (c *Compactor) GenerateSummary(ctx context.Context, messages []agentctx.AgentMessage) (string, error) {
	return c.GenerateSummaryWithPrevious(ctx, messages, c.systemPrompt, "", nil, "")
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
func (c *Compactor) GenerateSummaryWithPrevious(goCtx context.Context, messages []agentctx.AgentMessage, systemPrompt string, contextPrefix string, tools []agentctx.Tool, previousSummary string) (string, error) {
	span := traceevent.StartSpan(goCtx, "GenerateSummaryWithPrevious", traceevent.CategoryEvent)
	defer span.End()

	if len(messages) == 0 {
		return "", fmt.Errorf("no messages to summarize")
	}

	// Convert old messages to the same structured LLMMessage format used by
	// normal agent turns. oldMessages is a strict prefix of the conversation
	// (split off by splitMessagesByTokenBudget in Compact), so it already fits
	// within the context window — no further truncation needed or wanted:
	// truncating here would break prefix-cache alignment.
	llmMessages := agentctx.ConvertMessagesToLLM(messages)
	if len(llmMessages) == 0 {
		if strings.TrimSpace(previousSummary) != "" {
			return previousSummary, nil
		}
		return "", fmt.Errorf("no agent-visible messages to summarize")
	}

	// Prepend contextPrefix (skills + AGENTS.md) as the first message (after
	// system). oldMessages starts with the conversation's first user message
	// (it's a strict prefix), so placing the prefix at [0] mirrors the agent
	// loop's structure exactly: [system, prefix, real_first_user, ...].
	if strings.TrimSpace(contextPrefix) != "" {
		llmMessages = append([]llm.LLMMessage{{
			Role:    "user",
			Content: contextPrefix,
		}}, llmMessages...)
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
		ctx, cancel := context.WithTimeout(goCtx, totalTimeout)

		llmStream := llm.StreamLLM(ctx, c.model, llmCtx, c.apiKey, chunkTimeout)

		var summary strings.Builder
		var streamErr error
		var doneEvent llm.LLMDoneEvent
		for event := range llmStream.Iterator(ctx) {
			if event.Done {
				break
			}

			switch e := event.Value.(type) {
			case llm.LLMTextDeltaEvent:
				summary.WriteString(e.Delta)
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
