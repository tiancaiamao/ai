package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/traceevent"
	"log/slog"
)

// executeConversationLoop executes the conversation loop in normal mode.
// It implements: LLM → (tools) → LLM → ... → final response
//
// This is called by executeNormalStep (user message is already appended).
//
// Returns:
// - ExecutionMode: ModeDone (complete), ModeContextMgmt (need context management), ModeError
// - error: any error that occurred
func (a *AgentNew) executeConversationLoop(ctx context.Context) (ExecutionMode, error) {
	// Track duplicate tool call signatures to detect infinite loops
	const maxDuplicateCalls = 7
	duplicateSignatureCount := make(map[string]int) // signature -> consecutive count

	for cycle := 0; ; cycle++ {
		// Check for context cancellation at the start of each cycle
		// This allows steer/follow-up to interrupt before LLM call
		select {
		case <-ctx.Done():
			slog.Info("[AgentNew] Context canceled at start of conversation loop cycle",
				"cycle", cycle+1,
			)
			return ModeDone, ctx.Err()
		default:
			// Context is still valid, continue
		}

		// Check max turns limit (if set, typically only in headless mode)
		if a.maxTurns > 0 && cycle >= a.maxTurns {
			return ModeError, fmt.Errorf("reached maximum turns (%d) with pending tool calls", a.maxTurns)
		}

		// Build LLM request
		llmMessages, systemPrompt := a.buildNormalModeRequest(ctx)

		llmCtx := llm.LLMContext{
			SystemPrompt:  systemPrompt,
			Messages:      llmMessages,
			Tools:         ConvertToolsToLLM(ctx, a.allTools),
			ThinkingLevel: a.thinkingLevel,
		}

		// Call LLM with retry logic
		assistantMsg, toolResults, err := a.callLLMWithRetry(ctx, llmCtx, cycle)
		if err != nil {
			return ModeError, err
		}

		// Append assistant message
		a.snapshot.RecentMessages = append(a.snapshot.RecentMessages, *assistantMsg)
		traceevent.Log(ctx, traceevent.CategoryEvent, "message_end",
			traceevent.Field{Key: "role", Value: "assistant"},
			traceevent.Field{Key: "stop_reason", Value: assistantMsg.StopReason},
			traceevent.Field{Key: "chars", Value: len(assistantMsg.ExtractText())},
		)
		if err := a.journal.AppendMessage(*assistantMsg); err != nil {
			return ModeError, fmt.Errorf("failed to append assistant message: %w", err)
		}

		// Append tool results
		for _, result := range toolResults {
			a.snapshot.RecentMessages = append(a.snapshot.RecentMessages, result)
			if err := a.journal.AppendMessage(result); err != nil {
				slog.Warn("[AgentNew] Failed to append tool result", "error", err)
			}
		}

		// Track tool calls since last trigger
		a.snapshot.AgentState.ToolCallsSinceLastTrigger += len(toolResults)
		a.snapshot.AgentState.UpdatedAt = time.Now()

		// Check for context cancellation (e.g., from /abort or /steer)
		select {
		case <-ctx.Done():
			slog.Info("[AgentNew] Context canceled during conversation loop",
				"cycle", cycle+1,
				"tool_count", len(toolResults),
			)
			return ModeDone, ctx.Err()
		default:
			// Context is still valid, continue
		}

		// Check for pending input (from /steer or /follow-up)
		if pendingMessage, hasPending := a.GetAndClearPendingInput(); hasPending {
			slog.Info("[AgentNew] Pending input detected, exiting conversation loop",
				"pending_message", pendingMessage,
				"cycle", cycle+1,
			)
			// Store the pending message for the next turn
			// We return ModeDone so the trampoline will exit and the RPC handler can process the pending input
			return ModeDone, nil
		}

		// If no tool calls were made, we're done
		if len(toolResults) == 0 {
			return ModeDone, nil
		}

		// Tool calls were made, check if we need context management before next LLM call
		shouldTrigger, urgency, _ := a.triggerChecker.ShouldTrigger(a.snapshot)
		if shouldTrigger && urgency != agentctx.UrgencySkip {
			slog.Info("[AgentNew] Context management needed during conversation loop",
				"urgency", urgency,
				"cycle", cycle+1,
			)
			return ModeContextMgmt, nil
		}

		// Continue the loop to get the final response
		slog.Info("[AgentNew] Tool calls completed, continuing conversation",
			"tool_count", len(toolResults),
			"cycle", cycle+1,
		)

		// Check for duplicate tool call signatures (same tool + parameters repeated)
		// This detects infinite loops where the agent keeps calling the same tool with same args
		if err := a.checkDuplicateToolSignatures(assistantMsg, duplicateSignatureCount, maxDuplicateCalls); err != nil {
			return ModeError, err
		}
	}
}

// buildNormalModeRequest builds the LLM request for normal mode.
func (a *AgentNew) buildNormalModeRequest(ctx context.Context) ([]llm.LLMMessage, string) {
	// Build system prompt with thinking level instruction, skills, and project context
	slog.Info("[AgentNew] buildNormalModeRequest", "skillsExtraLen", len(a.skillsExtra), "projectContextExtraLen", len(a.projectContextExtra), "thinkingLevel", a.thinkingLevel, "customSystemPromptLen", len(a.customSystemPrompt))
	systemPrompt := prompt.BuildSystemPromptWithExtras(agentctx.ModeNormal, a.thinkingLevel, a.skillsExtra, a.projectContextExtra, a.customSystemPrompt)

	// Convert recent messages to LLM format with validation
	var llmMessages []llm.LLMMessage

	for _, msg := range a.snapshot.RecentMessages {
		// Filter out messages that are not visible to agent
		// Compaction sets agent_visible=false to hide old messages from LLM (saves tokens)
		// Users can still see them (user_visible=true)
		if !msg.IsAgentVisible() {
			continue
		}
		// Filter out truncated messages - they have been replaced with summaries
		if msg.IsTruncated() {
			continue
		}

		// Extract text content and tool calls
		content := msg.ExtractText()
		toolCalls := msg.ExtractToolCalls()

		// Skip empty messages
		// Assistant messages with only tool_calls should be preserved
		// Tool messages should be preserved even with empty content
		isEmpty := content == "" && len(toolCalls) == 0
		isTool := msg.Role == "tool" || msg.Role == "toolResult"
		if isEmpty && !isTool {
			continue
		}

		// Skip empty assistant messages (content == "" && no tool_calls)
		// These are typically failed or incomplete responses
		if msg.Role == "assistant" && isEmpty {
			continue
		}

		// Convert internal role to LLM API role
		// - toolResult → tool (OpenAI API format)
		// - other roles → keep as-is
		role := msg.Role
		if msg.Role == "toolResult" {
			role = "tool"  // Convert to OpenAI API format
		}

		// Convert message to LLM format
		llmMsg := llm.LLMMessage{
			Role:    role,
			Content: content,
		}

		// Extract and convert tool calls for assistant messages
		if len(toolCalls) > 0 {
			llmMsg.ToolCalls = make([]llm.ToolCall, 0, len(toolCalls))
			for _, tc := range toolCalls {
				// Convert arguments map back to JSON string
				argsJSON := "{}"
				if tc.Arguments != nil {
					if bytes, err := json.Marshal(tc.Arguments); err == nil {
						argsJSON = string(bytes)
					}
				}
				llmMsg.ToolCalls = append(llmMsg.ToolCalls, llm.ToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: llm.FunctionCall{
						Name:      tc.Name,
						Arguments: argsJSON,
					},
				})
			}
		}

		// For tool messages, we need the tool_call_id
		if msg.Role == "toolResult" {
			llmMsg.ToolCallID = msg.ToolCallID
		}

		llmMessages = append(llmMessages, llmMsg)
	}

	// Normalize message sequence to ensure valid role alternation
	llmMessages = normalizeMessageSequence(ctx, llmMessages)

	// Inject runtime state and LLM context
	// These should be part of the last user message, not separate messages
	runtimeState := a.buildRuntimeState()
	llmContext := ""
	if a.snapshot.LLMContext != "" {
		llmContext = fmt.Sprintf("<agent:llm_context>\n%s\n</agent:llm_context>", a.snapshot.LLMContext)
	}

	// Append runtime state and LLM context to the last user message
	if runtimeState != "" || llmContext != "" {
		llmMessages = appendToLastUserMessage(llmMessages, runtimeState, llmContext, ctx)
	}

	return llmMessages, systemPrompt
}

// buildRuntimeState builds the runtime state XML for the agent.
func (a *AgentNew) buildRuntimeState() string {
	tokenPercent := a.snapshot.EstimateTokenPercent()
	staleCount := a.snapshot.CountStaleOutputs(10)

	content := fmt.Sprintf(`<agent:runtime_state>
tokens_used: %d
tokens_limit: %d
tokens_percent: %.1f
recent_messages: %d
stale_outputs: %d
turn: %d
</agent:runtime_state>`,
		a.snapshot.EstimateTokens(),
		a.snapshot.AgentState.TokensLimit,
		tokenPercent*100,
		len(a.snapshot.RecentMessages),
		staleCount,
		a.snapshot.AgentState.TotalTurns,
	)

	return content
}

// processLLMResponse processes the LLM streaming response and executes tools.
// callLLMWithRetry wraps the LLM call + response processing with retry logic.
// It mirrors the retry strategy from main branch's streamAssistantResponseWithRetry:
//   - Rate limit errors: up to 8 retries with exponential backoff (base 3s, cap 30s)
//   - Other retryable errors (5xx, network): up to 1 retry with exponential backoff (base 1s)
//   - Non-retryable errors (4xx, context length exceeded): no retry
//   - Context cancellation: immediate abort
func (a *AgentNew) callLLMWithRetry(
	ctx context.Context,
	llmCtx llm.LLMContext,
	cycle int,
) (*agentctx.AgentMessage, []agentctx.AgentMessage, error) {
	// Resolve retry configuration
	maxRetries := a.maxLLMRetries
	if maxRetries < 0 {
		// Retry explicitly disabled
		maxRetries = 0
	} else if maxRetries == 0 {
		maxRetries = defaultLLMMaxRetries
	}
	baseDelay := a.retryBaseDelay
	if baseDelay <= 0 {
		baseDelay = defaultRetryBaseDelay
	}

	var lastErr error
	var isRateLimitError bool

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Compute backoff delay
			if isRateLimitError {
				// Extend retry budget for rate limit errors
				rlMaxRetries := defaultRateLimitMaxRetries
				if maxRetries > defaultRateLimitMaxRetries {
					rlMaxRetries = maxRetries
				}
				if attempt > maxRetries && attempt <= rlMaxRetries {
					maxRetries = rlMaxRetries
				}

				baseDelay = defaultRateLimitBaseDelay
				delay := baseDelay * time.Duration(1<<(attempt-1))
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}

				// Respect provider backoff hint
				retryAfter := llm.RetryAfter(lastErr)
				if retryAfter > delay {
					delay = retryAfter
				}
				if delay < 2*time.Second {
					delay = 2 * time.Second
				}
				delay = jitterDelay(delay)

				// Emit retry event
				a.emitRetryEvent(ctx, LLMRetryInfo{
					Attempt:    attempt,
					MaxRetries: maxRetries,
					Delay:      delay,
					ErrorType:  "rate_limit",
					Error:      lastErr.Error(),
				})

				slog.Info("[AgentNew] Retrying LLM call (rate limit)",
					"attempt", attempt,
					"maxRetries", maxRetries,
					"delay", delay)

				select {
				case <-time.After(delay):
					// Continue with retry
				case <-ctx.Done():
					traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
						traceevent.Field{Key: "attempt", Value: attempt},
						traceevent.Field{Key: "max_retries", Value: maxRetries},
						traceevent.Field{Key: "reason", Value: "context_done"},
					)
					if lastErr != nil {
						return nil, nil, lastErr
					}
					return nil, nil, ctx.Err()
				}
			} else {
				// Standard retry for non-rate-limit errors
				delay := baseDelay * time.Duration(1<<(attempt-1))
				delay = jitterDelay(delay)

				slog.Info("[AgentNew] Retrying LLM call",
					"attempt", attempt,
					"maxRetries", maxRetries,
					"delay", delay,
					"errorType", classifyLLMError(lastErr).ErrorType)

				select {
				case <-time.After(delay):
					// Continue with retry
				case <-ctx.Done():
					traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
						traceevent.Field{Key: "attempt", Value: attempt},
						traceevent.Field{Key: "max_retries", Value: maxRetries},
						traceevent.Field{Key: "reason", Value: "context_done"},
					)
					if lastErr != nil {
						return nil, nil, lastErr
					}
					return nil, nil, ctx.Err()
				}
			}
		}

		// Start trace span for this attempt
		llmStart := time.Now()
		llmSpan := traceevent.StartSpan(ctx, "llm_call", traceevent.CategoryLLM,
			traceevent.Field{Key: "model", Value: a.model.ID},
			traceevent.Field{Key: "provider", Value: a.model.Provider},
			traceevent.Field{Key: "api", Value: a.model.API},
			traceevent.Field{Key: "attempt", Value: attempt},
			traceevent.Field{Key: "timeout_ms", Value: int64((2 * time.Minute) / time.Millisecond)},
		)

		stream := llm.StreamLLM(
			ctx,
			*a.model,
			llmCtx,
			a.apiKey,
			2*time.Minute, // Chunk interval timeout
		)

		// Process response
		assistantMsg, toolResults, err := a.processLLMResponse(ctx, stream, llmStart)
		if err == nil {
			// Success — record metadata and return
			if assistantMsg != nil {
				llmSpan.AddField("stop_reason", assistantMsg.StopReason)
				if assistantMsg.Usage != nil {
					llmSpan.AddField("input_tokens", assistantMsg.Usage.InputTokens)
					llmSpan.AddField("output_tokens", assistantMsg.Usage.OutputTokens)
					llmSpan.AddField("total_tokens", assistantMsg.Usage.TotalTokens)
				}
			}
			llmSpan.End()
			return assistantMsg, toolResults, nil
		}

		// Error path
		llmSpan.AddField("error", true)
		llmSpan.AddField("error_message", err.Error())
		meta := classifyLLMError(err)
		llmSpan.AddField("error_type", meta.ErrorType)
		if meta.StatusCode > 0 {
			llmSpan.AddField("error_status_code", meta.StatusCode)
		}
		if meta.RetryAfter > 0 {
			llmSpan.AddField("retry_after_ms", meta.RetryAfter.Milliseconds())
		}
		llmSpan.AddField("retryable", shouldRetryLLMError(err))
		llmSpan.End()

		isRateLimitError = llm.IsRateLimit(err)

		// Context length exceeded — never retry, escalate to context management
		if llm.IsContextLengthExceeded(err) {
			slog.Error("[AgentNew] LLM call failed (context length exceeded)",
				"attempt", attempt,
				"error", err)
			return nil, nil, err
		}

		lastErr = err
		slog.Error("[AgentNew] LLM call failed",
			"attempt", attempt,
			"maxRetries", maxRetries,
			"isRateLimit", isRateLimitError,
			"error", err)

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
				traceevent.Field{Key: "attempt", Value: attempt},
				traceevent.Field{Key: "max_retries", Value: maxRetries},
				traceevent.Field{Key: "reason", Value: "context_done_after_error"},
			)
			return nil, nil, lastErr
		}

		// Check if error is retryable
		if !shouldRetryLLMError(lastErr) {
			traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_aborted",
				traceevent.Field{Key: "attempt", Value: attempt},
				traceevent.Field{Key: "max_retries", Value: maxRetries},
				traceevent.Field{Key: "reason", Value: "non_retryable"},
				traceevent.Field{Key: "error_type", Value: meta.ErrorType},
				traceevent.Field{Key: "error_message", Value: lastErr.Error()},
			)
			return nil, nil, lastErr
		}

		// For rate limit errors, allow more retries beyond initial maxRetries
		if isRateLimitError && attempt >= maxRetries {
			rlMaxRetries := defaultRateLimitMaxRetries
			if a.maxLLMRetries > defaultRateLimitMaxRetries {
				rlMaxRetries = a.maxLLMRetries
			}
			if attempt < rlMaxRetries {
				maxRetries = rlMaxRetries
				continue
			}
		}
	}

	// All retries exhausted
	if lastErr != nil {
		meta := classifyLLMError(lastErr)
		exhaustedFields := []traceevent.Field{
			{Key: "max_retries", Value: maxRetries},
			{Key: "error_type", Value: meta.ErrorType},
			{Key: "error_message", Value: lastErr.Error()},
		}
		if meta.StatusCode > 0 {
			exhaustedFields = append(exhaustedFields, traceevent.Field{Key: "error_status_code", Value: meta.StatusCode})
		}
		traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry_exhausted", exhaustedFields...)
	}
	return nil, nil, lastErr
}

// emitRetryEvent emits an LLM retry event through the event emitter (if available).
func (a *AgentNew) emitRetryEvent(ctx context.Context, info LLMRetryInfo) {
	if a.eventEmitter != nil {
		a.eventEmitter.Emit(NewLLMRetryEvent(info))
	}
	traceevent.Log(ctx, traceevent.CategoryLLM, "llm_retry",
		traceevent.Field{Key: "attempt", Value: info.Attempt},
		traceevent.Field{Key: "max_retries", Value: info.MaxRetries},
		traceevent.Field{Key: "delay", Value: info.Delay.String()},
		traceevent.Field{Key: "error_type", Value: info.ErrorType},
	)
}

func (a *AgentNew) processLLMResponse(
	ctx context.Context,
	stream *llm.EventStream[llm.LLMEvent, llm.LLMMessage],
	startTime time.Time,
) (*agentctx.AgentMessage, []agentctx.AgentMessage, error) {
	var partialMessage *agentctx.AgentMessage
	var textBuilder strings.Builder
	var thinkingBuilder strings.Builder
	toolCalls := map[int]*llm.ToolCall{}

	// Wait for the stream to complete
	for event := range stream.Iterator(ctx) {
		if event.Done {
			break
		}

		switch e := event.Value.(type) {
		case llm.LLMStartEvent:
			traceevent.Log(ctx, traceevent.CategoryEvent, "message_start",
				traceevent.Field{Key: "role", Value: "assistant"},
			)
			traceevent.Log(ctx, traceevent.CategoryLLM, "assistant_text",
				traceevent.Field{Key: "state", Value: "start"},
			)
			// Emit message_start event for win UI
			if a.eventEmitter != nil {
				partialMessage = &agentctx.AgentMessage{}
				*partialMessage = agentctx.NewAssistantMessage()
				a.eventEmitter.Emit(NewMessageStartEvent(*partialMessage))
			} else {
				partialMessage = &agentctx.AgentMessage{}
				*partialMessage = agentctx.NewAssistantMessage()
			}
			textBuilder.Reset()
			thinkingBuilder.Reset()
			toolCalls = map[int]*llm.ToolCall{}

		case llm.LLMTextDeltaEvent:
			traceevent.Log(ctx, traceevent.CategoryLLM, "text_delta",
				traceevent.Field{Key: "content_index", Value: e.Index},
				traceevent.Field{Key: "delta", Value: e.Delta},
			)
			if partialMessage != nil {
				textBuilder.WriteString(e.Delta)
				// Update content to include all existing blocks
				var contentBlocks []agentctx.ContentBlock
				if thinkingBuilder.Len() > 0 {
					contentBlocks = append(contentBlocks, agentctx.ThinkingContent{
						Type:     "thinking",
						Thinking: thinkingBuilder.String(),
					})
				}
				if textBuilder.Len() > 0 {
					contentBlocks = append(contentBlocks, agentctx.TextContent{
						Type: "text",
						Text: textBuilder.String(),
					})
				}
				// Preserve tool calls if any
				if len(toolCalls) > 0 {
					for _, tc := range toolCalls {
						if tc.ID != "" {
							argsMap := make(map[string]any)
							if tc.Function.Arguments != "" {
								if err := json.Unmarshal([]byte(tc.Function.Arguments), &argsMap); err != nil {
									argsMap = make(map[string]any)
								}
							}
							contentBlocks = append(contentBlocks, agentctx.ToolCallContent{
								ID:        tc.ID,
								Type:      "toolCall",
								Name:      tc.Function.Name,
								Arguments: argsMap,
							})
						}
					}
				}
				partialMessage.Content = contentBlocks
				// Emit text_delta event for win UI
				if a.eventEmitter != nil {
					a.eventEmitter.Emit(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
						Type:         "text_delta",
						ContentIndex: e.Index,
						Delta:        e.Delta,
					}))
				}
			}

		case llm.LLMThinkingDeltaEvent:
			traceevent.Log(ctx, traceevent.CategoryLLM, "thinking_delta",
				traceevent.Field{Key: "content_index", Value: e.Index},
				traceevent.Field{Key: "delta", Value: e.Delta},
			)
			if partialMessage != nil {
				thinkingBuilder.WriteString(e.Delta)
				// Update content to include thinking
				var contentBlocks []agentctx.ContentBlock
				if thinkingBuilder.Len() > 0 {
					contentBlocks = append(contentBlocks, agentctx.ThinkingContent{
						Type:     "thinking",
						Thinking: thinkingBuilder.String(),
					})
				}
				if textBuilder.Len() > 0 {
					contentBlocks = append(contentBlocks, agentctx.TextContent{
						Type: "text",
						Text: textBuilder.String(),
					})
				}
				// Preserve tool calls if any
				if len(toolCalls) > 0 {
					for _, tc := range toolCalls {
						if tc.ID != "" {
							argsMap := make(map[string]any)
							if tc.Function.Arguments != "" {
								if err := json.Unmarshal([]byte(tc.Function.Arguments), &argsMap); err != nil {
									argsMap = make(map[string]any)
								}
							}
							contentBlocks = append(contentBlocks, agentctx.ToolCallContent{
								ID:        tc.ID,
								Type:      "toolCall",
								Name:      tc.Function.Name,
								Arguments: argsMap,
							})
						}
					}
				}
				partialMessage.Content = contentBlocks
				// Emit thinking_delta event for win UI
				if a.eventEmitter != nil {
					a.eventEmitter.Emit(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
						Type:         "thinking_delta",
						ContentIndex: e.Index,
						Delta:        e.Delta,
					}))
				}
			}

		case llm.LLMToolCallDeltaEvent:
			toolCallID := ""
			toolName := ""
			if e.ToolCall != nil {
				toolCallID = e.ToolCall.ID
				toolName = e.ToolCall.Function.Name
			}
			traceevent.Log(ctx, traceevent.CategoryLLM, "tool_call_delta",
				traceevent.Field{Key: "content_index", Value: e.Index},
				traceevent.Field{Key: "tool_call_id", Value: toolCallID},
				traceevent.Field{Key: "tool_name", Value: toolName},
			)
			if partialMessage != nil {
				call, ok := toolCalls[e.Index]
				if !ok {
					call = &llm.ToolCall{}
					toolCalls[e.Index] = call
				}

				if e.ToolCall != nil && e.ToolCall.ID != "" {
					call.ID = e.ToolCall.ID
				}
				if e.ToolCall != nil && e.ToolCall.Function.Name != "" {
					call.Function.Name = e.ToolCall.Function.Name
				}
				if e.ToolCall != nil && e.ToolCall.Function.Arguments != "" {
					call.Function.Arguments += e.ToolCall.Function.Arguments
				}

				// Update message content with all blocks
				var contentBlocks []agentctx.ContentBlock
				if thinkingBuilder.Len() > 0 {
					contentBlocks = append(contentBlocks, agentctx.ThinkingContent{
						Type:     "thinking",
						Thinking: thinkingBuilder.String(),
					})
				}
				if textBuilder.Len() > 0 {
					contentBlocks = append(contentBlocks, agentctx.TextContent{
						Type: "text",
						Text: textBuilder.String(),
					})
				}

				for _, tc := range toolCalls {
					if tc.ID != "" {
						argsMap := make(map[string]any)
						if tc.Function.Arguments != "" {
							if err := json.Unmarshal([]byte(tc.Function.Arguments), &argsMap); err != nil {
								slog.Warn("[AgentNew] Failed to parse tool call arguments",
									"tool", tc.Function.Name,
									"toolCallID", tc.ID,
									"error", err,
								)
								argsMap = make(map[string]any)
							}
						}

						contentBlocks = append(contentBlocks, agentctx.ToolCallContent{
							ID:        tc.ID,
							Type:      "toolCall",
							Name:      tc.Function.Name,
							Arguments: argsMap,
						})
					}
				}

				partialMessage.Content = contentBlocks
			}

		case llm.LLMDoneEvent:
			elapsed := time.Since(startTime)
			traceevent.Log(ctx, traceevent.CategoryLLM, "assistant_text",
				traceevent.Field{Key: "state", Value: "end"},
			)
			slog.Info("[AgentNew] LLM call completed",
				"duration_ms", elapsed.Milliseconds(),
				"input_tokens", e.Usage.InputTokens,
				"output_tokens", e.Usage.OutputTokens,
			)

			if partialMessage != nil {
				partialMessage.API = a.model.API
				partialMessage.Provider = a.model.Provider
				partialMessage.Model = a.model.ID
				partialMessage.Timestamp = time.Now().UnixMilli()
				partialMessage.StopReason = e.StopReason
				partialMessage.Usage = &agentctx.Usage{
					InputTokens:  e.Usage.InputTokens,
					OutputTokens: e.Usage.OutputTokens,
					TotalTokens:  e.Usage.TotalTokens,
				}

				// Update tokens used in snapshot
				a.snapshot.AgentState.TokensUsed = e.Usage.TotalTokens
				a.snapshot.AgentState.LastLLMContextUpdate = a.snapshot.AgentState.TotalTurns

				// Emit message_end event for win UI
				if a.eventEmitter != nil {
					a.eventEmitter.Emit(NewMessageEndEvent(*partialMessage))
				}

				// Execute tool calls if present
				toolResults := a.executeToolsFromMessage(ctx, partialMessage)
				return partialMessage, toolResults, nil
			}

			if e.Message != nil {
				msg := convertLLMMessageToAgent(*e.Message)

				// Emit message_end event for win UI
				if a.eventEmitter != nil {
					a.eventEmitter.Emit(NewMessageEndEvent(msg))
				}

				// Execute tool calls if present
				toolResults := a.executeToolsFromMessage(ctx, &msg)
				return &msg, toolResults, nil
			}

			return nil, nil, fmt.Errorf("no message in LLM response")

		case llm.LLMErrorEvent:
			return nil, nil, e.Error
		}
	}

	return nil, nil, fmt.Errorf("LLM stream ended without completion")
}

// executeToolsFromMessage executes tool calls from an assistant message.
// When multiple tool calls are present, they are executed concurrently.
func (a *AgentNew) executeToolsFromMessage(
	ctx context.Context,
	assistantMsg *agentctx.AgentMessage,
) []agentctx.AgentMessage {
	toolCalls := assistantMsg.ExtractToolCalls()
	if len(toolCalls) == 0 {
		return nil
	}

	// Build tool lookup map
	toolsByName := make(map[string]agentctx.Tool, len(a.allTools))
	for _, tool := range a.allTools {
		toolsByName[tool.Name()] = tool
	}

	// If only one tool call, execute sequentially (avoids goroutine overhead)
	if len(toolCalls) == 1 {
		return a.executeToolSingle(ctx, toolsByName, toolCalls[0])
	}

	// Execute multiple tool calls concurrently
	return a.executeToolsConcurrent(ctx, toolsByName, toolCalls)
}

// toolExecutionOutcome holds the result of a single tool execution.
type toolExecutionOutcome struct {
	index   int
	toolMsg agentctx.AgentMessage
}

// executeToolSingle executes a single tool call sequentially.
func (a *AgentNew) executeToolSingle(
	ctx context.Context,
	toolsByName map[string]agentctx.Tool,
	tc agentctx.ToolCallContent,
) (msgResult []agentctx.AgentMessage) {
	// Recover from panics in tool execution to prevent crashing the agent.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("[AgentNew] Tool execution panicked (single)",
				"tool", tc.Name,
				"tool_call_id", tc.ID,
				"panic", r,
			)
			errorContent := []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("Tool '%s' panicked: %v", tc.Name, r)},
			}
			msgResult = []agentctx.AgentMessage{
				{
					Role:       "tool",
					Content:    errorContent,
					ToolCallID: tc.ID,
					Timestamp:  time.Now().Unix(),
				},
			}
		}
	}()

	// Check for context cancellation
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	toolName := tc.Name
	toolCallID := tc.ID
	arguments := tc.Arguments
	toolStart := time.Now()
	toolSpan := traceevent.StartSpan(ctx, "tool_execution", traceevent.CategoryTool,
		traceevent.Field{Key: "tool", Value: toolName},
		traceevent.Field{Key: "tool_call_id", Value: toolCallID},
	)

	slog.Info("[AgentNew] Executing tool",
		"tool", toolName,
		"toolCallID", toolCallID,
		"args", arguments,
	)
	traceevent.Log(ctx, traceevent.CategoryTool, "tool_start",
		traceevent.Field{Key: "tool", Value: toolName},
		traceevent.Field{Key: "tool_call_id", Value: toolCallID},
		traceevent.Field{Key: "args", Value: arguments},
	)

	// Emit tool_execution_start event for win mode
	if a.eventEmitter != nil {
		a.eventEmitter.Emit(NewToolExecutionStartEvent(toolCallID, toolName, arguments))
	}

	// Find the tool
	tool := toolsByName[toolName]
	if tool == nil {
		toolSpan.AddField("error", true)
		toolSpan.AddField("error_message", "tool not found")
		toolSpan.End()
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_unresolved",
			traceevent.Field{Key: "normalized_name", Value: toolName},
			traceevent.Field{Key: "tool_call_id", Value: toolCallID},
			traceevent.Field{Key: "args", Value: arguments},
		)
		slog.Warn("[AgentNew] Tool not found",
			"tool", toolName,
			"availableTools", len(toolsByName),
		)
		errorContent := []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Tool '%s' not found", toolName),
			},
		}
		truncatedContent := truncateToolContent(ctx, errorContent, DefaultToolOutputLimits(), toolName)
		result := agentctx.NewToolResultMessage(toolCallID, toolName, truncatedContent, true)
		if a.eventEmitter != nil {
			a.eventEmitter.Emit(NewToolExecutionEndEvent(toolCallID, toolName, &result, true))
		}
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
			traceevent.Field{Key: "tool", Value: toolName},
			traceevent.Field{Key: "tool_call_id", Value: toolCallID},
			traceevent.Field{Key: "duration_ms", Value: time.Since(toolStart).Milliseconds()},
			traceevent.Field{Key: "error", Value: true},
			traceevent.Field{Key: "error_message", Value: "tool not found"},
		)
		a.snapshot.AgentState.ToolCallsSinceLastTrigger++
		a.snapshot.AgentState.UpdatedAt = time.Now()
		return []agentctx.AgentMessage{result}
	}

	// Execute the tool
	content, err := tool.Execute(ctx, arguments)
	var result agentctx.AgentMessage
	if err != nil {
		toolSpan.AddField("error", true)
		toolSpan.AddField("error_message", err.Error())
		toolSpan.End()
		slog.Error("[AgentNew] Tool execution failed",
			"tool", toolName,
			"error", err,
		)
		errorContent := []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Tool execution failed: %v", err),
			},
		}
		truncatedContent := truncateToolContent(ctx, errorContent, DefaultToolOutputLimits(), toolName)
		result = agentctx.NewToolResultMessage(toolCallID, toolName, truncatedContent, true)
		if a.eventEmitter != nil {
			a.eventEmitter.Emit(NewToolExecutionEndEvent(toolCallID, toolName, &result, true))
		}
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
			traceevent.Field{Key: "tool", Value: toolName},
			traceevent.Field{Key: "tool_call_id", Value: toolCallID},
			traceevent.Field{Key: "duration_ms", Value: time.Since(toolStart).Milliseconds()},
			traceevent.Field{Key: "error", Value: true},
			traceevent.Field{Key: "error_message", Value: err.Error()},
		)
	} else {
		truncatedContent := truncateToolContent(ctx, content, DefaultToolOutputLimits(), toolName)
		result = agentctx.NewToolResultMessage(toolCallID, toolName, truncatedContent, false)
		toolSpan.AddField("duration_ms", time.Since(toolStart).Milliseconds())
		toolSpan.End()
		if a.eventEmitter != nil {
			a.eventEmitter.Emit(NewToolExecutionEndEvent(toolCallID, toolName, &result, false))
		}
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
			traceevent.Field{Key: "tool", Value: toolName},
			traceevent.Field{Key: "tool_call_id", Value: toolCallID},
			traceevent.Field{Key: "duration_ms", Value: time.Since(toolStart).Milliseconds()},
			traceevent.Field{Key: "error", Value: false},
		)
		slog.Info("[AgentNew] Tool execution completed",
			"tool", toolName,
			"toolCallID", toolCallID,
		)
	}

	a.snapshot.AgentState.ToolCallsSinceLastTrigger++
	a.snapshot.AgentState.UpdatedAt = time.Now()
	return []agentctx.AgentMessage{result}
}

// executeToolsConcurrent executes multiple tool calls concurrently using fan-out/fan-in pattern.
func (a *AgentNew) executeToolsConcurrent(
	ctx context.Context,
	toolsByName map[string]agentctx.Tool,
	toolCalls []agentctx.ToolCallContent,
) []agentctx.AgentMessage {
	type indexedResult struct {
		index    int
		toolMsg  agentctx.AgentMessage
	}

	outcomes := make(chan indexedResult, len(toolCalls))
	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		// Check for context cancellation before launching each goroutine
		select {
		case <-ctx.Done():
			slog.Info("[AgentNew] Context canceled, skipping remaining tool executions",
				"tool", tc.Name,
				"index", i,
				"remaining", len(toolCalls)-i,
			)
			// Drain: send empty results for remaining tools
			go func(idx int) {
				outcomes <- indexedResult{index: idx}
			}(i)
			continue
		default:
		}

		wg.Add(1)
		go func(idx int, tc agentctx.ToolCallContent) {
			defer wg.Done()
			// Recover from panics in tool execution to prevent crashing the agent.
			// If a tool panics, we return an error result for that tool call,
			// preserving the correct index so results are ordered correctly.
			defer func() {
				if r := recover(); r != nil {
					slog.Error("[AgentNew] Tool execution panicked",
						"tool", tc.Name,
						"tool_call_id", tc.ID,
						"panic", r,
					)
					errorContent := []agentctx.ContentBlock{
						agentctx.TextContent{Type: "text", Text: fmt.Sprintf("Tool '%s' panicked: %v", tc.Name, r)},
					}
					outcomes <- indexedResult{
						index: idx,
						toolMsg: agentctx.AgentMessage{
							Role:       "tool",
							Content:    errorContent,
							ToolCallID: tc.ID,
							Timestamp:  time.Now().Unix(),
						},
					}
				}
			}()

			toolName := tc.Name
			toolCallID := tc.ID
			arguments := tc.Arguments
			toolStart := time.Now()

			toolSpan := traceevent.StartSpan(ctx, "tool_execution", traceevent.CategoryTool,
				traceevent.Field{Key: "tool", Value: toolName},
				traceevent.Field{Key: "tool_call_id", Value: toolCallID},
			)

			slog.Info("[AgentNew] Executing tool (concurrent)",
				"tool", toolName,
				"toolCallID", toolCallID,
				"index", idx,
			)
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_start",
				traceevent.Field{Key: "tool", Value: toolName},
				traceevent.Field{Key: "tool_call_id", Value: toolCallID},
				traceevent.Field{Key: "args", Value: arguments},
			)

			if a.eventEmitter != nil {
				a.eventEmitter.Emit(NewToolExecutionStartEvent(toolCallID, toolName, arguments))
			}

			// Find the tool
			tool := toolsByName[toolName]
			if tool == nil {
				toolSpan.AddField("error", true)
				toolSpan.AddField("error_message", "tool not found")
				toolSpan.End()
				traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_unresolved",
					traceevent.Field{Key: "normalized_name", Value: toolName},
					traceevent.Field{Key: "tool_call_id", Value: toolCallID},
				)
				errorContent := []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: fmt.Sprintf("Tool '%s' not found", toolName)},
				}
				truncatedContent := truncateToolContent(ctx, errorContent, DefaultToolOutputLimits(), toolName)
				result := agentctx.NewToolResultMessage(toolCallID, toolName, truncatedContent, true)
				if a.eventEmitter != nil {
					a.eventEmitter.Emit(NewToolExecutionEndEvent(toolCallID, toolName, &result, true))
				}
				traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
					traceevent.Field{Key: "tool", Value: toolName},
					traceevent.Field{Key: "tool_call_id", Value: toolCallID},
					traceevent.Field{Key: "duration_ms", Value: time.Since(toolStart).Milliseconds()},
					traceevent.Field{Key: "error", Value: true},
					traceevent.Field{Key: "error_message", Value: "tool not found"},
				)
				outcomes <- indexedResult{index: idx, toolMsg: result}
				return
			}

			// Execute the tool
			content, err := tool.Execute(ctx, arguments)
			var result agentctx.AgentMessage
			if err != nil {
				toolSpan.AddField("error", true)
				toolSpan.AddField("error_message", err.Error())
				toolSpan.End()
				slog.Error("[AgentNew] Tool execution failed",
					"tool", toolName,
					"error", err,
				)
				errorContent := []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: fmt.Sprintf("Tool execution failed: %v", err)},
				}
				truncatedContent := truncateToolContent(ctx, errorContent, DefaultToolOutputLimits(), toolName)
				result = agentctx.NewToolResultMessage(toolCallID, toolName, truncatedContent, true)
				if a.eventEmitter != nil {
					a.eventEmitter.Emit(NewToolExecutionEndEvent(toolCallID, toolName, &result, true))
				}
				traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
					traceevent.Field{Key: "tool", Value: toolName},
					traceevent.Field{Key: "tool_call_id", Value: toolCallID},
					traceevent.Field{Key: "duration_ms", Value: time.Since(toolStart).Milliseconds()},
					traceevent.Field{Key: "error", Value: true},
					traceevent.Field{Key: "error_message", Value: err.Error()},
				)
			} else {
				truncatedContent := truncateToolContent(ctx, content, DefaultToolOutputLimits(), toolName)
				result = agentctx.NewToolResultMessage(toolCallID, toolName, truncatedContent, false)
				toolSpan.AddField("duration_ms", time.Since(toolStart).Milliseconds())
				toolSpan.End()
				if a.eventEmitter != nil {
					a.eventEmitter.Emit(NewToolExecutionEndEvent(toolCallID, toolName, &result, false))
				}
				traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
					traceevent.Field{Key: "tool", Value: toolName},
					traceevent.Field{Key: "tool_call_id", Value: toolCallID},
					traceevent.Field{Key: "duration_ms", Value: time.Since(toolStart).Milliseconds()},
					traceevent.Field{Key: "error", Value: false},
				)
				slog.Info("[AgentNew] Tool execution completed (concurrent)",
					"tool", toolName,
					"toolCallID", toolCallID,
					"index", idx,
				)
			}
			outcomes <- indexedResult{index: idx, toolMsg: result}
		}(i, tc)
	}

	wg.Wait()
	close(outcomes)

	// Collect results and preserve original order
	resultByIndex := make(map[int]agentctx.AgentMessage, len(toolCalls))
	for outcome := range outcomes {
		resultByIndex[outcome.index] = outcome.toolMsg
	}

	results := make([]agentctx.AgentMessage, 0, len(toolCalls))
	for i := range toolCalls {
		if msg, ok := resultByIndex[i]; ok {
			results = append(results, msg)
		}
	}

	// Increment tool call counter for trigger detection
	a.snapshot.AgentState.ToolCallsSinceLastTrigger += len(toolCalls)
	a.snapshot.AgentState.UpdatedAt = time.Now()

	return results
}

// convertLLMMessageToAgent converts an LLM message to an AgentMessage.
func convertLLMMessageToAgent(msg llm.LLMMessage) agentctx.AgentMessage {
	agentMsg := agentctx.NewAssistantMessage()
	agentMsg.Content = []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: msg.Content},
	}

	// Handle tool calls if present
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			// Parse arguments from JSON string to map
			argsMap := make(map[string]any)
			if tc.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &argsMap); err != nil {
					// If parsing fails, create empty map
					argsMap = make(map[string]any)
				}
			}

			agentMsg.Content = append(agentMsg.Content, agentctx.ToolCallContent{
				ID:        tc.ID,
				Type:      "toolCall",
				Name:      tc.Function.Name,
				Arguments: argsMap,
			})
		}
	}

	return agentMsg
}

// insertBeforeLastUserMessage inserts a message before the last user message in the list.
// If there's no user message, it appends to the end.
func insertBeforeLastUserMessage(messages []llm.LLMMessage, newMsg llm.LLMMessage) []llm.LLMMessage {
	// Find the last user message index
	lastUserIndex := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserIndex = i
			break
		}
	}

	// If no user message found, append to end
	if lastUserIndex == -1 {
		return append(messages, newMsg)
	}

	// Insert before the last user message
	result := make([]llm.LLMMessage, 0, len(messages)+1)
	result = append(result, messages[:lastUserIndex]...)
	result = append(result, newMsg)
	result = append(result, messages[lastUserIndex:]...)
	return result
}

// normalizeMessageSequence ensures the message sequence conforms to OpenAI API requirements.
// OpenAI API format: system -> user -> assistant -> tool × n -> user -> assistant -> tool × n -> ...
// Multiple tool messages CAN follow one assistant message (parallel tool calls).
//
// Strategy: Work backwards from the end, finding the longest valid suffix.
// Then work forwards through that suffix to validate the sequence.
func normalizeMessageSequence(ctx context.Context, messages []llm.LLMMessage) []llm.LLMMessage {
	if len(messages) == 0 {
		return messages
	}

	originalCount := len(messages)
	traceevent.Log(ctx, traceevent.CategoryEvent, "message_normalize_start",
		traceevent.Field{Key: "original_count", Value: originalCount},
	)

	// Step 1: Find the longest valid suffix (working backwards)
	// Valid pattern (from end): [tool × n] -> user -> assistant -> [tool × n] -> user -> ... -> [system]
	suffix := findValidSuffix(ctx, messages)
	if len(suffix) == 0 {
		traceevent.Log(ctx, traceevent.CategoryEvent, "message_normalize_empty_suffix",
			traceevent.Field{Key: "original_count", Value: originalCount},
		)
		return suffix
	}

	// Step 2: Validate the suffix sequence and filter out invalid messages
	result := validateSequence(ctx, suffix)

	traceevent.Log(ctx, traceevent.CategoryEvent, "message_normalize_complete",
		traceevent.Field{Key: "original_count", Value: originalCount},
		traceevent.Field{Key: "filtered_count", Value: len(result)},
		traceevent.Field{Key: "dropped_count", Value: originalCount - len(result)},
	)

	return result
}

// findValidSuffix finds the longest valid suffix working backwards.
// The message pattern from the front is: ... → user → assistant → tool × n → user → assistant → ...
// When working backwards, we see: ... → tool × n → assistant → user → tool × n → assistant → ...
func findValidSuffix(ctx context.Context, messages []llm.LLMMessage) []llm.LLMMessage {

	type state int
	const (
		stateStart state = iota
		stateAfterUser      // Just saw a user message (expecting assistant or tools from previous round)
		stateAfterAssistant // Just saw an assistant message (expecting tools)
		stateInTools        // In a sequence of tool messages
	)

	currentState := stateStart
	suffix := make([]llm.LLMMessage, 0, len(messages))

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		// Skip empty messages
		// BUT: assistant messages with tool_calls should be preserved
		// AND: tool messages with empty content should be preserved
		isEmpty := msg.Content == ""
		hasToolCalls := len(msg.ToolCalls) > 0
		isTool := msg.Role == "tool" || msg.Role == "toolResult"

		if isEmpty && !hasToolCalls && !isTool {
			continue
		}

		accepted := false

		switch currentState {
		case stateStart:
			// At the end, we can have user, assistant, or tool
			if msg.Role == "user" {
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				currentState = stateAfterUser
			} else if msg.Role == "assistant" {
				// CRITICAL: If the first message is an assistant with tool_calls,
				// we MUST include at least one tool result after it.
				// If we can't find a tool result, this is likely a bug and we should
				// reject this assistant message to prevent sending an invalid sequence.
				hasToolCalls := len(msg.ToolCalls) > 0
				if hasToolCalls {
					// Check if there's a tool result after this assistant message
					hasToolResult := false
					for j := i + 1; j < len(messages); j++ {
						if messages[j].Role == "tool" {
							hasToolResult = true
							break
						}
					}
					if !hasToolResult {
						// Assistant with tool_calls but no tool result - this is invalid!
						// Skip this message and continue looking for valid messages
						toolCallID := "unknown"
						if len(msg.ToolCalls) > 0 {
							toolCallID = msg.ToolCalls[0].ID
						}
						slog.Warn("[AgentNew] Skipping assistant message with tool_calls but no tool result",
							"tool_call_id", toolCallID,
							"index", i,
							"total_messages", len(messages),
						)
						traceevent.Log(ctx, traceevent.CategoryEvent, "message_normalize_skip_assistant_no_result",
							traceevent.Field{Key: "tool_call_id", Value: toolCallID},
							traceevent.Field{Key: "index", Value: i},
							traceevent.Field{Key: "total_messages", Value: len(messages)},
						)
						continue
					}
				}
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				currentState = stateAfterAssistant
			} else if msg.Role == "tool" {
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				currentState = stateInTools
			}

		case stateAfterUser:
			// After user, expect assistant (from this round) or tool (from previous round)
			// When going backward, we can also encounter a user message from a previous round
			if msg.Role == "assistant" {
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				currentState = stateAfterAssistant
			} else if msg.Role == "tool" {
				// Tools from previous round
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				currentState = stateInTools
			} else if msg.Role == "user" {
				// User from previous round (when going backward through multi-round conversation)
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				// Stay in stateAfterUser to continue looking for assistant/tools from earlier rounds
			} else {
				// Unexpected role (e.g., system, etc.) - skip and continue
				// This allows us to skip over unexpected messages rather than breaking
				continue
			}

		case stateAfterAssistant:
			// After assistant, expect tools or user (for multi-round conversations)
			if msg.Role == "tool" {
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				currentState = stateInTools
			} else if msg.Role == "user" {
				// User message from previous round (multi-round conversation without tools)
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				currentState = stateAfterUser
			} else if msg.Role == "assistant" {
				// Consecutive assistant messages - this is invalid but may happen due to bugs
				// Skip this message and continue looking for a valid pattern
				// This prevents losing all previous messages when we encounter invalid consecutive assistants
				toolCallID := "unknown"
				if len(msg.ToolCalls) > 0 {
					toolCallID = msg.ToolCalls[0].ID
				}
				slog.Warn("[AgentNew] Skipping consecutive assistant message during normalization",
					"tool_call_id", toolCallID,
					"index", i,
					"total_messages", len(messages),
				)
				traceevent.Log(ctx, traceevent.CategoryEvent, "message_normalize_skip_consecutive_assistant",
					traceevent.Field{Key: "tool_call_id", Value: toolCallID},
					traceevent.Field{Key: "index", Value: i},
					traceevent.Field{Key: "remaining_messages", Value: len(suffix)},
				)
				continue
			}

		case stateInTools:
			// In tool sequence, expect more tools or the assistant that created them
			if msg.Role == "tool" {
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				// Stay in stateInTools
			} else if msg.Role == "assistant" {
				// We expect this assistant to have tool_calls (it created the tools)
				// If it doesn't have tool_calls, this might be an invalid sequence
				hasToolCalls := len(msg.ToolCalls) > 0
				if !hasToolCalls {
					// Assistant without tool_calls in a tool sequence - this is suspicious
					// Skip this message and continue looking
					slog.Warn("[AgentNew] Skipping assistant message without tool_calls in tool sequence",
						"index", i,
						"total_messages", len(messages),
					)
					traceevent.Log(ctx, traceevent.CategoryEvent, "message_normalize_skip_assistant_no_calls",
						traceevent.Field{Key: "index", Value: i},
						traceevent.Field{Key: "total_messages", Value: len(messages)},
					)
					continue
				}
				suffix = append([]llm.LLMMessage{msg}, suffix...)
				accepted = true
				currentState = stateAfterUser
			}
		}

		// If we didn't accept this message, stop
		if !accepted {
			break
		}
	}

	return suffix
}

// validateSequence validates and filters a message sequence.
func validateSequence(ctx context.Context, messages []llm.LLMMessage) []llm.LLMMessage {

	type state int
	const (
		stateStart state = iota  // Can start with system, user, or assistant
		stateExpectAssistant      // After system or user
		stateExpectToolOrUser     // After assistant (tool × n or user)
	)

	currentState := stateStart
	valid := make([]llm.LLMMessage, 0, len(messages))

	for _, msg := range messages {
		accepted := false

		switch currentState {
		case stateStart:
			// Can start with system, user, or assistant
			// Assistant (with or without tool_calls) is valid as start for multi-round conversations
			if msg.Role == "system" || msg.Role == "user" {
				valid = append(valid, msg)
				accepted = true
				currentState = stateExpectAssistant
			} else if msg.Role == "assistant" {
				// Assistant messages are valid as start (middle of multi-round conversation)
				valid = append(valid, msg)
				accepted = true
				currentState = stateExpectToolOrUser
			}

		case stateExpectAssistant:
			if msg.Role == "assistant" {
				valid = append(valid, msg)
				accepted = true
				currentState = stateExpectToolOrUser
			}

		case stateExpectToolOrUser:
			// After assistant, expect tools or user
			// Assistant with tool_calls is also valid (new round starting after tool responses)
			// Regular assistant (without tool_calls) is also valid (end of conversation)
			if msg.Role == "tool" || msg.Role == "user" {
				valid = append(valid, msg)
				accepted = true
				if msg.Role == "user" {
					currentState = stateExpectAssistant
				}
				// If tool, stay in stateExpectToolOrUser
			} else if msg.Role == "assistant" {
				// Accept all assistant messages (with or without tool_calls)
				valid = append(valid, msg)
				accepted = true
				// Stay in stateExpectToolOrUser (expecting more tools or user)
			}
		}

		if !accepted {
			break
		}
	}

	return valid
}

// appendToLastUserMessage appends runtime state and LLM context to the last user message.
// This avoids creating consecutive user messages which would break the API format.
func appendToLastUserMessage(messages []llm.LLMMessage, runtimeState, llmContext string, ctx context.Context) []llm.LLMMessage {
	// Find the last user message
	lastUserIndex := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserIndex = i
			break
		}
	}

	// If no user message found, create one with the content
	if lastUserIndex == -1 {
		if runtimeState == "" && llmContext == "" {
			return messages
		}
		content := runtimeState
		if llmContext != "" {
			if content != "" {
				content += "\n\n" + llmContext
			} else {
				content = llmContext
			}
		}
		return append(messages, llm.LLMMessage{
			Role:    "user",
			Content: content,
		})
	}

	// Append to the last user message
	lastMsg := messages[lastUserIndex]
	content := lastMsg.Content

	if runtimeState != "" {
		if content != "" {
			content += "\n\n" + runtimeState
		} else {
			content = runtimeState
		}
	}
	if llmContext != "" {
		if content != "" {
			content += "\n\n" + llmContext
		} else {
			content = llmContext
		}
	}

	messages[lastUserIndex] = llm.LLMMessage{
		Role:    "user",
		Content: content,
	}

	return messages
}

// checkDuplicateToolSignatures checks if the same tool call signature (name + parameters)
// is repeated too many times consecutively, which indicates an infinite loop.
func (a *AgentNew) checkDuplicateToolSignatures(
	assistantMsg *agentctx.AgentMessage,
	signatureCount map[string]int,
	maxDuplicates int,
) error {
	toolCalls := assistantMsg.ExtractToolCalls()
	if len(toolCalls) == 0 {
		return nil
	}

	// Build signature for each tool call
	// Signature is: toolName + sorted args hash
	for _, tc := range toolCalls {
		// Create signature from tool name and parameters
		signature := buildToolCallSignature(tc)

		// Increment consecutive count for this signature
		signatureCount[signature]++

		slog.Info("[AgentNew] Tool call signature tracked",
			"signature", signature,
			"tool", tc.Name,
			"count", signatureCount[signature],
			"max", maxDuplicates,
		)

		// Check if we've exceeded the max duplicates
		if signatureCount[signature] >= maxDuplicates {
			return fmt.Errorf("agent appears to be stuck in a loop: tool call '%s' repeated %d times consecutively",
				tc.Name, maxDuplicates)
		}
	}

	// Reset counts for signatures that weren't in this batch
	// This ensures we only count CONSECUTIVE duplicates
	currentSignatures := make(map[string]bool)
	for _, tc := range toolCalls {
		signature := buildToolCallSignature(tc)
		currentSignatures[signature] = true
	}

	for sig := range signatureCount {
		if !currentSignatures[sig] {
			delete(signatureCount, sig)
		}
	}

	return nil
}

// buildToolCallSignature creates a unique signature for a tool call.
// The signature is based on tool name and normalized parameters.
func buildToolCallSignature(tc agentctx.ToolCallContent) string {
	// Convert arguments to a stable string representation
	argsJSON := "{}"
	if tc.Arguments != nil {
		if bytes, err := json.Marshal(tc.Arguments); err == nil {
			argsJSON = string(bytes)
		}
	}
	return tc.Name + ":" + argsJSON
}
