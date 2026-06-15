package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// streamAssistantResponse streams the assistant's response from the LLM.
func streamAssistantResponse(
	ctx context.Context,
	agentCtx *agentctx.AgentContext,
	config *LoopConfig,
	stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
) (*agentctx.AgentMessage, error) {
	thinkingLevel := prompt.NormalizeThinkingLevel(config.ThinkingLevel)

	// Create timeout context for LLM calls
	llmTimeout := config.LLMTotalTimeout
	if llmTimeout == 0 {
		llmTimeout = defaultLLMTotalTimeout
	}
	llmCtx, llmCancel := context.WithTimeout(ctx, llmTimeout)
	defer llmCancel()

	var llmMessages []llm.LLMMessage

	selectedMessages, _ := selectMessagesForLLM(agentCtx)
	llmMessages = ConvertMessagesToLLM(ctx, selectedMessages)

	systemPrompt := agentCtx.SystemPrompt
	if instruction := prompt.ThinkingInstruction(thinkingLevel); instruction != "" {
		if strings.TrimSpace(systemPrompt) == "" {
			systemPrompt = instruction
		} else {
			systemPrompt = systemPrompt + "\n\n" + instruction
		}
	}

	// Resolve model early — needed for cache mode detection before runtime_state injection.
	model := getEffectiveModel(config)

	// Resolve cache mode and determine runtime_state injection strategy.
	// Cache-first (e.g. DeepSeek): persist runtime_state as AgentMessage in RecentMessages
	//   for higher prefix cache hit rates across turns.
	// Context-first (default): ephemeral injection before last user message, current behavior.
	runtimeAppendix := injectRuntimeMeta(agentCtx, config)
	if runtimeAppendix != "" {
		cacheMode := ResolveCacheMode(config.CacheMode, model.ID)
		policy := DefaultMutationPolicy(cacheMode)

		switch policy.RuntimeStateStrategy() {
		case RuntimeStatePersist:
			// Cache-first: create a persistent AgentMessage and append to RecentMessages,
			// then rebuild LLM messages so the new runtime_state is included.
			// Remove any stale runtime_state left by a previous retry attempt of this turn.
			agentCtx.RecentMessages = removeRuntimeStateMessages(agentCtx.RecentMessages)
			runtimeAgentMsg := agentctx.AgentMessage{
				Role:      "user",
				Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: runtimeAppendix}},
				Timestamp: time.Now().UnixMilli(),
				Metadata:  &agentctx.MessageMetadata{Kind: "runtime_state"},
			}
			agentCtx.RecentMessages = append(agentCtx.RecentMessages, runtimeAgentMsg)
			selectedMessages, _ = selectMessagesForLLM(agentCtx)
			llmMessages = ConvertMessagesToLLM(ctx, selectedMessages)

		case RuntimeStateEphemeral:
			// Context-first: current behavior unchanged — temporary injection.
			runtimeMsg := llm.LLMMessage{
				Role:    "user",
				Content: runtimeAppendix,
			}
			llmMessages = insertBeforeLastUserMessage(llmMessages, runtimeMsg)
		}
	}

	// Inject skills + instructions as a single user message before the first
	// user message. Both are stable within a session (skills rarely change,
	// instructions are AGENTS.md content), so merging them into one message
	// and placing them in the prefix maximizes provider prefix cache hits.
	//
	// They are NOT persisted to RecentMessages — re-injected on every LLM call.
	//
	// Final ordering:
	//   [system_prompt, <skills+instructions>, user1, asst1, ..., <runtime_state>, user_input]
	//
	// runtime_state is injected separately below (before last user) because it
	// can change when the user calls change_workspace, and we don't want it to
	// break the stable prefix cache.
	if config.AgentContextPrefix != "" {
		prefixMsg := llm.LLMMessage{
			Role:    "user",
			Content: config.AgentContextPrefix,
		}
		llmMessages = insertBeforeFirstUserMessage(llmMessages, prefixMsg)
	}

	// Convert tools to LLM format
	llmTools := ConvertToolsToLLM(ctx, agentCtx.Tools)

	llmCtxParams := llm.LLMContext{
		SystemPrompt: systemPrompt,
		Messages:     llmMessages,
		Tools:        llmTools,
	}
	//	emitLLMRequestSnapshot(ctx, config.Model, llmCtxParams)

	// Stream LLM response
	llmStart := time.Now()
	llmSpan := traceevent.StartSpan(ctx, "llm_call", traceevent.CategoryLLM,
		traceevent.Field{Key: "model", Value: model.ID},
		traceevent.Field{Key: "provider", Value: model.Provider},
		traceevent.Field{Key: "api", Value: model.API},
		traceevent.Field{Key: "attempt", Value: llmAttemptFromContext(ctx)},
		traceevent.Field{Key: "timeout_ms", Value: llmTimeout.Milliseconds()},
	)
	defer llmSpan.End()
	firstTokenRecorded := false
	firstTokenLatency := time.Duration(0)

	llmStream := llm.StreamLLM(
		llmCtx,
		model,
		llmCtxParams,
		getEffectiveAPIKey(config),
		config.LLMFirstResponseTimeout,
	)

	var partialMessage *agentctx.AgentMessage
	chunkState := NewStreamChunkState()

	for event := range llmStream.Iterator(ctx) {
		if event.Done {
			break
		}

		result := processStreamChunk(chunkState, event.Value, thinkingLevel)

		switch result.EventType {
		case ChunkStart:
			partialMessage = new(agentctx.AgentMessage)
			*partialMessage = agentctx.NewAssistantMessage()
			stream.Push(NewMessageStartEvent(*partialMessage))
			stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
				Type:         "text_start",
				ContentIndex: 0,
			}))

		case ChunkTextDelta:
			if partialMessage != nil {
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
				traceevent.Log(ctx, traceevent.CategoryLLM, "text_delta",
					traceevent.Field{Key: "content_index", Value: result.ContentIndex},
					traceevent.Field{Key: "delta", Value: result.Delta},
				)
				partialMessage.Content = result.Content
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "text_delta",
					ContentIndex: result.ContentIndex,
					Delta:        result.Delta,
				}))
			}

		case ChunkThinkingDelta:
			if partialMessage != nil {
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
				traceevent.Log(ctx, traceevent.CategoryLLM, "thinking_delta",
					traceevent.Field{Key: "content_index", Value: result.ContentIndex},
					traceevent.Field{Key: "delta", Value: result.Delta},
				)
				partialMessage.Content = result.Content
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "thinking_delta",
					ContentIndex: result.ContentIndex,
					Delta:        result.Delta,
				}))
			}

		case ChunkToolCallDelta:
			if partialMessage != nil {
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
				e := event.Value.(llm.LLMToolCallDeltaEvent)
				traceevent.Log(ctx, traceevent.CategoryLLM, "tool_call_delta",
					traceevent.Field{Key: "content_index", Value: e.Index},
					traceevent.Field{Key: "tool_call_id", Value: e.ToolCall.ID},
					traceevent.Field{Key: "tool_name", Value: e.ToolCall.Function.Name},
				)
				partialMessage.Content = result.Content
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "toolcall_delta",
					ContentIndex: result.ContentIndex,
				}))
			}

		case ChunkDone:
			e := result.DoneEvent
			llmSpan.AddField("input_tokens", e.Usage.InputTokens)
			llmSpan.AddField("output_tokens", e.Usage.OutputTokens)
			llmSpan.AddField("total_tokens", e.Usage.TotalTokens)
			llmSpan.AddField("stop_reason", e.StopReason)
			elapsed := time.Since(llmStart)
			if elapsed > 0 {
				seconds := elapsed.Seconds()
				llmSpan.AddField("input_tokens_per_sec", float64(e.Usage.InputTokens)/seconds)
				llmSpan.AddField("output_tokens_per_sec", float64(e.Usage.OutputTokens)/seconds)
				llmSpan.AddField("total_tokens_per_sec", float64(e.Usage.TotalTokens)/seconds)
			}
			if firstTokenLatency > 0 {
				llmSpan.AddField("first_token_ms", firstTokenLatency.Milliseconds())
			}

			// Check if stopReason indicates context length exceeded.
			// Some providers return this as a normal completion (ChunkDone) rather
			// than an error (ChunkError). We must convert it to a Go error so the
			// context_limit_recovery path in loop.go can trigger compaction.
			if llm.IsContextLengthStopReason(e.StopReason) {
				ctxErr := &llm.ContextLengthExceededError{
					Message: fmt.Sprintf("LLM returned stopReason=%s indicating context window exceeded", e.StopReason),
				}
				llmSpan.AddField("context_limit_detected", true)
				traceevent.Log(ctx, traceevent.CategoryLLM, "context_limit_from_stop_reason",
					traceevent.Field{Key: "stop_reason", Value: e.StopReason},
					traceevent.Field{Key: "input_tokens", Value: e.Usage.InputTokens},
				)
				return nil, WithErrorStack(ctxErr)
			}

			if partialMessage != nil {
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "text_end",
					ContentIndex: 0,
				}))
			}
			var finalMessage agentctx.AgentMessage
			model := getEffectiveModel(config)
			if partialMessage != nil {
				finalMessage = *partialMessage
			} else if e.Message != nil {
				finalMessage = ConvertLLMMessageToAgent(*e.Message)
			} else {
				finalMessage = agentctx.NewAssistantMessage()
			}

			finalMessage.API = model.API
			finalMessage.Provider = model.Provider
			finalMessage.Model = model.ID
			finalMessage.Timestamp = time.Now().UnixMilli()
			finalMessage.StopReason = e.StopReason
			finalMessage.Usage = &agentctx.Usage{
				InputTokens:  e.Usage.InputTokens,
				OutputTokens: e.Usage.OutputTokens,
				TotalTokens:  e.Usage.TotalTokens,
			}

			// Try to inject tool calls from tagged text
			if updated, ok := injectToolCallsFromTaggedText(finalMessage); ok {
				finalMessage = updated
			} else if updated, ok := injectToolCallsFromThinking(finalMessage); ok {
				finalMessage = updated
			} else {
				text := finalMessage.ExtractText()
				if len(text) > 0 && strings.Contains(text, "<") {
					issues := DetectIncompleteToolCalls(text)
					traceevent.Log(ctx, traceevent.CategoryTool, "assistant_tool_tag_parse_failed",
						traceevent.Field{Key: "stop_reason", Value: e.StopReason},
						traceevent.Field{Key: "text_preview", Value: truncateLine(text, 500)},
						traceevent.Field{Key: "issues", Value: issues},
						traceevent.Field{Key: "issue_count", Value: len(issues)},
					)
				}
			}

			stream.Push(NewMessageEndEvent(finalMessage))
			return &finalMessage, nil

		case ChunkError:
			errVal := result.Error
			if errVal == nil {
				errVal = errors.New("unknown llm error")
			}
			if errors.Is(errVal, context.DeadlineExceeded) {
				errVal = fmt.Errorf("llm request timeout after %s: %w", llmTimeout, errVal)
			}
			wrappedErr := WithErrorStack(errVal)
			meta := classifyLLMError(wrappedErr)
			llmSpan.AddField("error", wrappedErr.Error())
			llmSpan.AddField("error_type", meta.ErrorType)
			if meta.StatusCode > 0 {
				llmSpan.AddField("error_status_code", meta.StatusCode)
			}
			if meta.RetryAfter > 0 {
				llmSpan.AddField("retry_after_ms", meta.RetryAfter.Milliseconds())
			}
			llmSpan.AddField("retryable", shouldRetryLLMError(wrappedErr))
			if stack := ErrorStack(wrappedErr); stack != "" {
				llmSpan.AddField("error_stack", stack)
			}
			if firstTokenLatency > 0 {
				llmSpan.AddField("first_token_ms", firstTokenLatency.Milliseconds())
			}
			return nil, wrappedErr
		}
	}

	// If the iterator exited without sending DoneEvent or ErrorEvent, the
	// stream was truncated. Return an error so the retry logic can kick in.
	if partialMessage != nil && partialMessage.StopReason == "" {
		return nil, fmt.Errorf("LLM stream ended without completion (no DoneEvent received)")
	}

	return partialMessage, nil
}

func selectMessagesForLLM(agentCtx *agentctx.AgentContext) ([]agentctx.AgentMessage, string) {
	if agentCtx == nil {
		return nil, "empty_context"
	}
	if len(agentCtx.RecentMessages) == 0 {
		return nil, "no_messages"
	}
	return agentCtx.RecentMessages, "all_available_messages_no_runtime_clip"
}

func buildRuntimeUserAppendix(llmContextContent, runtimeMetaSnapshot string) string {
	sections := make([]string, 0, 3)
	if strings.TrimSpace(llmContextContent) != "" {
		sections = append(sections, fmt.Sprintf("<llm_context>\n%s\n</llm_context>", llmContextContent))
	}
	if strings.TrimSpace(runtimeMetaSnapshot) != "" {
		sections = append(sections, runtimeMetaSnapshot)
	}
	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n\n")
}

// buildRuntimeSystemAppendix is kept for backward-compatible tests/helpers.
// Runtime state is now injected as a user message, not appended to system prompt.
func buildRuntimeSystemAppendix(llmContextContent, runtimeMetaSnapshot string) string {
	return buildRuntimeUserAppendix(llmContextContent, runtimeMetaSnapshot)
}

func updateRuntimeMetaSnapshot(
	agentCtx *agentctx.AgentContext,
	meta agentctx.ContextMeta,
	heartbeatTurns int,
	currentWorkdir string,
	startupPath string,
	runID string,
) (string, bool) {
	if agentCtx == nil {
		return "", false
	}
	if heartbeatTurns <= 0 {
		heartbeatTurns = defaultRuntimeMetaHeartbeatTurns
	}

	agentCtx.AgentState.RuntimeMetaTurns++
	band := runtimeTokenBand(meta.TokensPercent)

	shouldRefresh := strings.TrimSpace(agentCtx.AgentState.RuntimeMetaSnapshot) == "" ||
		agentCtx.AgentState.RuntimeMetaBand != band ||
		agentCtx.AgentState.RuntimeMetaTurns >= heartbeatTurns

	if !shouldRefresh {
		return agentCtx.AgentState.RuntimeMetaSnapshot, false
	}

	// runtime_state is purely informational - no directives or commands
	// Build run_id line only when available (subagent spawned via ai serve).
	var runIDLine string
	if runID != "" {
		runIDLine = fmt.Sprintf("\n  run_id: %s", runID)
	}

	snapshot := fmt.Sprintf(`<agent:runtime_state"/>
%s
  current_workdir: %s
  startup_path: %s`,
		runIDLine,
		runtimeYAMLString(currentWorkdir),
		runtimeYAMLString(startupPath),
	)

	agentCtx.AgentState.RuntimeMetaSnapshot = snapshot
	agentCtx.AgentState.RuntimeMetaBand = band
	agentCtx.AgentState.RuntimeMetaTurns = 0

	return snapshot, true
}

// removeRuntimeStateMessages removes all runtime_state messages from the slice.
// This prevents duplicate runtime_state messages from accumulating when
// streamAssistantResponse is retried (each retry appends a fresh one).
func removeRuntimeStateMessages(msgs []agentctx.AgentMessage) []agentctx.AgentMessage {
	filtered := msgs[:0]
	for _, msg := range msgs {
		if msg.Metadata != nil && msg.Metadata.Kind == "runtime_state" {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func insertBeforeLastUserMessage(messages []llm.LLMMessage, msg llm.LLMMessage) []llm.LLMMessage {
	if len(messages) == 0 {
		return []llm.LLMMessage{msg}
	}

	// Find the last user message index
	lastUserIdx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			lastUserIdx = i
			break
		}
	}

	// If no user message found, append to end
	if lastUserIdx == -1 {
		return append(messages, msg)
	}

	// Insert before the last user message
	result := make([]llm.LLMMessage, 0, len(messages)+1)
	result = append(result, messages[:lastUserIdx]...)
	result = append(result, msg)
	result = append(result, messages[lastUserIdx:]...)
	return result
}

// insertBeforeFirstUserMessage inserts msg immediately before the first user-role
// message. Used for skills injection to keep the system prompt + skills prefix
// stable across turns for provider prefix caching.
func insertBeforeFirstUserMessage(messages []llm.LLMMessage, msg llm.LLMMessage) []llm.LLMMessage {
	if len(messages) == 0 {
		return []llm.LLMMessage{msg}
	}

	// Find the first user message index
	firstUserIdx := -1
	for i := 0; i < len(messages); i++ {
		if messages[i].Role == "user" {
			firstUserIdx = i
			break
		}
	}

	// If no user message found, prepend to beginning
	if firstUserIdx == -1 {
		result := make([]llm.LLMMessage, 0, len(messages)+1)
		result = append(result, msg)
		result = append(result, messages...)
		return result
	}

	// Insert before the first user message
	result := make([]llm.LLMMessage, 0, len(messages)+1)
	result = append(result, messages[:firstUserIdx]...)
	result = append(result, msg)
	result = append(result, messages[firstUserIdx:]...)
	return result
}
