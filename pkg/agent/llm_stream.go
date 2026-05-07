package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

	// Build runtime appendix (llm context + context meta) as a user message
	// injected BEFORE the last user message for better LLM attention.
	// Placing runtime_state close to the decision point improves context management.
	//
	// LLMContext content is always injected when non-empty.
	// runtime_state telemetry is ALWAYS injected from turn 1 so path info is available immediately.

	// Block A: LLMContext content injection — whenever non-empty.
	var llmContextContent string
	if agentCtx.LLMContext != "" {
		llmContextContent = agentCtx.LLMContext
	}

	// Block B: runtime_state telemetry — always, from turn 1.
	const defaultContextWindow = 200000 // matches internal/winai/interpreter.go default
	tokensMax := defaultContextWindow
	if config.ContextWindow > 0 {
		tokensMax = config.ContextWindow
	}
	tokensUsedApprox := EstimateConversationTokens(agentCtx.RecentMessages)

	// Update AgentState with token usage info
	agentCtx.AgentState.TokensUsed = tokensUsedApprox
	agentCtx.AgentState.TokensLimit = tokensMax
	agentCtx.AgentState.TotalTurns = len(agentCtx.RecentMessages)

	// Update CWD in AgentState so checkpoints preserve it for session restore
	if config.GetWorkingDir != nil {
		agentCtx.AgentState.CurrentWorkingDir = config.GetWorkingDir()
	}
	if config.GetStartupPath != nil {
		agentCtx.AgentState.WorkspaceRoot = config.GetStartupPath()
	}

	currentWorkdir := agentCtx.AgentState.CurrentWorkingDir
	startupPath := ""
	if config.GetStartupPath != nil {
		startupPath = config.GetStartupPath()
	}

	// Build meta for runtime snapshot from AgentState
	metaTokensUsed := agentCtx.AgentState.TokensUsed
	if metaTokensUsed == 0 {
		metaTokensUsed = tokensUsedApprox
	}
	metaTokensMax := agentCtx.AgentState.TokensLimit
	if metaTokensMax == 0 {
		metaTokensMax = tokensMax
	}
	metaTokensPercent := float64(0)
	if metaTokensMax > 0 {
		metaTokensPercent = float64(metaTokensUsed) / float64(metaTokensMax) * 100
	}

	meta := agentctx.ContextMeta{
		TokensUsed:        metaTokensUsed,
		TokensMax:         metaTokensMax,
		TokensPercent:     metaTokensPercent,
		MessagesInHistory: len(agentCtx.RecentMessages),
		LLMContextSize:    len(agentCtx.LLMContext),
	}

	runtimeMetaSnapshot, _ := updateRuntimeMetaSnapshot(agentCtx, meta, defaultRuntimeMetaHeartbeatTurns, currentWorkdir, startupPath)
	runtimeAppendix := buildRuntimeUserAppendix(llmContextContent, runtimeMetaSnapshot)

	// Insert runtime_state before the last user message for better attention.
	if runtimeAppendix != "" {
		runtimeMsg := llm.LLMMessage{
			Role:    "user",
			Content: runtimeAppendix,
		}
		llmMessages = insertBeforeLastUserMessage(llmMessages, runtimeMsg)
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
	model := getEffectiveModel(config)
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

	type toolCallState struct {
		id        string
		name      string
		callType  string
		arguments string
	}

	var partialMessage *agentctx.AgentMessage
	var textBuilder strings.Builder
	toolCalls := map[int]*toolCallState{}

	buildContent := func(text string, calls map[int]*toolCallState) []agentctx.ContentBlock {
		content := make([]agentctx.ContentBlock, 0, 1+len(calls))
		if text != "" {
			content = append(content, agentctx.TextContent{
				Type: "text",
				Text: text,
			})
		}

		if len(calls) == 0 {
			return content
		}

		indexes := make([]int, 0, len(calls))
		for idx := range calls {
			indexes = append(indexes, idx)
		}
		sort.Ints(indexes)

		for _, idx := range indexes {
			call := calls[idx]
			argsMap := make(map[string]any)
			if call.arguments != "" {
				if err := json.Unmarshal([]byte(call.arguments), &argsMap); err != nil {
					argsMap = make(map[string]any)
				}
			}

			content = append(content, agentctx.ToolCallContent{
				ID:        call.id,
				Type:      "toolCall",
				Name:      call.name,
				Arguments: argsMap,
			})
		}

		return content
	}

	for event := range llmStream.Iterator(ctx) {
		if event.Done {
			break
		}

		switch e := event.Value.(type) {
		case llm.LLMStartEvent:
			partialMessage = new(agentctx.AgentMessage)
			*partialMessage = agentctx.NewAssistantMessage()
			textBuilder.Reset()
			toolCalls = map[int]*toolCallState{}
			stream.Push(NewMessageStartEvent(*partialMessage))
			stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
				Type:         "text_start",
				ContentIndex: 0,
			}))

		case llm.LLMTextDeltaEvent:
			if partialMessage != nil {
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
				traceevent.Log(ctx, traceevent.CategoryLLM, "text_delta",
					traceevent.Field{Key: "content_index", Value: e.Index},
					traceevent.Field{Key: "delta", Value: e.Delta},
				)
				textBuilder.WriteString(e.Delta)
				partialMessage.Content = buildContent(textBuilder.String(), toolCalls)
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "text_delta",
					ContentIndex: e.Index,
					Delta:        e.Delta,
				}))
			}

		case llm.LLMThinkingDeltaEvent:
			if partialMessage != nil {
				if thinkingLevel == "off" {
					break
				}
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
				traceevent.Log(ctx, traceevent.CategoryLLM, "thinking_delta",
					traceevent.Field{Key: "content_index", Value: e.Index},
					traceevent.Field{Key: "delta", Value: e.Delta},
				)
				// Add thinking content to the message
				thinkingContent := agentctx.ThinkingContent{
					Type:     "thinking",
					Thinking: e.Delta,
				}
				partialMessage.Content = append(partialMessage.Content, thinkingContent)
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "thinking_delta",
					ContentIndex: e.Index,
					Delta:        e.Delta,
				}))
			}

		case llm.LLMToolCallDeltaEvent:
			if partialMessage != nil {
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
				traceevent.Log(ctx, traceevent.CategoryLLM, "tool_call_delta",
					traceevent.Field{Key: "content_index", Value: e.Index},
					traceevent.Field{Key: "tool_call_id", Value: e.ToolCall.ID},
					traceevent.Field{Key: "tool_name", Value: e.ToolCall.Function.Name},
				)
				call, ok := toolCalls[e.Index]
				if !ok {
					call = &toolCallState{}
					toolCalls[e.Index] = call
				}

				if e.ToolCall.ID != "" {
					call.id = e.ToolCall.ID
				}
				if e.ToolCall.Type != "" {
					call.callType = e.ToolCall.Type
				}
				if e.ToolCall.Function.Name != "" {
					call.name = e.ToolCall.Function.Name
				}
				if e.ToolCall.Function.Arguments != "" {
					// Anthropic API sends incremental arguments as streaming delta, so we need to concatenate
					// (not replace) to accumulate the full JSON
					call.arguments += e.ToolCall.Function.Arguments
				}

				partialMessage.Content = buildContent(textBuilder.String(), toolCalls)
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "toolcall_delta",
					ContentIndex: e.Index,
				}))
			}

		case llm.LLMDoneEvent:
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
			if partialMessage != nil {
				stream.Push(NewMessageUpdateEvent(*partialMessage, AssistantMessageEvent{
					Type:         "text_end",
					ContentIndex: 0,
				}))
			}
			var finalMessage agentctx.AgentMessage
			model := getEffectiveModel(config)
			if partialMessage != nil {
				// Prefer the incrementally built message so thinking/tool-tag content
				// emitted via deltas is not lost when done payload omits it.
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
				// Check for incomplete tool calls and log for debugging
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

		case llm.LLMErrorEvent:
			errVal := e.Error
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
	sections = append(sections, `Remember: runtime_state is telemetry, not user intent.
Follow the Turn Protocol defined in the system prompt.
Path authority: use LLM Context Path/Detail dir from system prompt; ignore cwd-relative examples.`)
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

	tokensUsedApprox := normalizeApprox(meta.TokensUsed)
	toolPressure := collectRuntimeToolPressure(agentCtx.RecentMessages)
	toolOutputsSummary := buildToolOutputsSummary(agentCtx.RecentMessages)

	// runtime_state is purely informational - no directives or commands
	snapshot := fmt.Sprintf(`<agent:runtime_state comment="telemetry snapshot, updated periodically"/>
context_meta:
  tokens_band: %s
  tokens_used_approx: %d
  tokens_max: %d
  messages_in_history_bucket: %s
  llm_context_size_bucket: %s
workspace:
  current_workdir: %s
  startup_path: %s
tool_output_pressure:
  stale_tool_outputs: %d
  tool_outputs_summary: %s
  large_tool_outputs: %d
  largest_tool_output_bucket: %s
compact_decision_signals:
  tokens_percent: %.1f
  context_usage_percent: %.1f
  topic_shift_since_last_user: llm_judge
  phase_completed_recently: llm_judge
  llm_judge_hint: Compare the latest user intent with recent task thread and milestone status, then set COMPACT confidence accordingly.`,
		band,
		tokensUsedApprox,
		meta.TokensMax,
		runtimeMessageBucket(meta.MessagesInHistory),
		runtimeSizeBucket(meta.LLMContextSize),
		runtimeYAMLString(currentWorkdir),
		runtimeYAMLString(startupPath),
		toolPressure.StaleCount,
		toolOutputsSummary,
		toolPressure.LargeCount,
		runtimeToolOutputSizeBucket(toolPressure.LargestChars),
		meta.TokensPercent,
		meta.TokensPercent,
	)

	agentCtx.AgentState.RuntimeMetaSnapshot = snapshot
	agentCtx.AgentState.RuntimeMetaBand = band
	agentCtx.AgentState.RuntimeMetaTurns = 0

	return snapshot, true
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
