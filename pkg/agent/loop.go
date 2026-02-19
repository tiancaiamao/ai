package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

const (
	defaultLLMMaxRetries               = 1               // Maximum retry attempts for LLM calls
	defaultRetryBaseDelay              = 1 * time.Second // Base delay for exponential backoff
	defaultLoopMaxConsecutiveToolCalls = 6
	defaultLoopMaxToolCallsPerName     = 60
)

// LoopConfig contains configuration for the agent loop.
type LoopConfig struct {
	Model                   llm.Model
	APIKey                  string
	Executor                *ExecutorPool // Tool executor with concurrency control
	Metrics                 *Metrics      // Metrics collector
	ToolOutput              ToolOutputLimits
	Compactor               Compactor     // Optional compactor for context-length recovery
	ToolCallCutoff          int           // Summarize oldest tool outputs when visible tool results exceed this
	ToolSummaryStrategy     string        // llm, heuristic, off
	ThinkingLevel           string        // off, minimal, low, medium, high, xhigh
	MaxLLMRetries           int           // Maximum number of retries for LLM calls
	RetryBaseDelay          time.Duration // Base delay for exponential backoff
	MaxConsecutiveToolCalls int           // Loop guard: max consecutive identical tool call signature (0=default, <0=disabled)
	MaxToolCallsPerName     int           // Loop guard: max total tool calls per tool name in one run (0=default, <0=disabled)
	MaxTurns                int           // Maximum conversation turns (0=default=unlimited)
}

var streamAssistantResponseFn = streamAssistantResponse
var summarizeToolResultFn = summarizeToolResultWithLLM
var summarizeToolResultsBatchFn = summarizeToolResultsBatchWithLLM

type llmAttemptKeyType struct{}

var llmAttemptKey = llmAttemptKeyType{}

func shouldRetryLLMError(err error) bool {
	if err == nil {
		return false
	}
	if llm.IsContextLengthExceeded(err) {
		return false
	}
	if llm.IsRateLimit(err) {
		return true
	}
	var apiErr *llm.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			return false
		}
	}
	return true
}

func jitterDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	// +/-20% deterministic jitter from clock to avoid retry bursts.
	span := delay / 5
	if span <= 0 {
		return delay
	}
	offset := time.Duration(time.Now().UnixNano()%int64(2*span)) - span
	jittered := delay + offset
	if jittered <= 0 {
		return delay
	}
	return jittered
}

// RunLoop starts a new agent loop with the given prompts.
func RunLoop(
	ctx context.Context,
	prompts []AgentMessage,
	agentCtx *AgentContext,
	config *LoopConfig,
) *llm.EventStream[AgentEvent, []AgentMessage] {
	stream := llm.NewEventStream[AgentEvent, []AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []AgentMessage { return e.Messages },
	)

	go func() {
		defer stream.End(nil)

		newMessages := append([]AgentMessage{}, prompts...)
		currentCtx := &AgentContext{
			SystemPrompt: agentCtx.SystemPrompt,
			Messages:     append(agentCtx.Messages, prompts...),
			Tools:        agentCtx.Tools,
		}

		stream.Push(NewAgentStartEvent())
		stream.Push(NewTurnStartEvent())

		for _, msg := range prompts {
			stream.Push(NewMessageStartEvent(msg))
			stream.Push(NewMessageEndEvent(msg))
		}

		runInnerLoop(ctx, currentCtx, newMessages, config, stream)
	}()

	return stream
}

// runInnerLoop contains the core loop logic.
func runInnerLoop(
	ctx context.Context,
	agentCtx *AgentContext,
	newMessages []AgentMessage,
	config *LoopConfig,
	stream *llm.EventStream[AgentEvent, []AgentMessage],
) {
	span := traceevent.StartSpan(ctx, "runInnerLoop", traceevent.CategoryEvent)
	defer span.End()

	const maxCompactionRecoveries = 1
	compactionRecoveries := 0
	loopGuard := newToolLoopGuard(config)
	asyncSummarizer := newAsyncToolSummarizer(ctx, config)
	if asyncSummarizer != nil {
		defer asyncSummarizer.Close()
	}

	// Turn counter for MaxTurns limit
	turnCount := 0

	for {
		if asyncSummarizer != nil {
			asyncSummarizer.applyReady(agentCtx)
		}

		// Check for context cancellation
		select {
		case <-ctx.Done():
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		default:
		}

		// Check for max turns limit
		if config.MaxTurns > 0 && turnCount >= config.MaxTurns {
			slog.Info("[Loop] max turns limit reached",
				"turns", turnCount,
				"maxTurns", config.MaxTurns)
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}
		turnCount++

		// Compact before each LLM request so long-running tool loops do not keep
		// carrying stale outputs into the next turn.
		if config.Compactor != nil && config.Compactor.ShouldCompact(agentCtx.Messages) {
			before := len(agentCtx.Messages)
			compactionSpan := traceevent.StartSpan(ctx, "compaction", traceevent.CategoryEvent,
				traceevent.Field{Key: "source", Value: "pre_llm_threshold"},
				traceevent.Field{Key: "auto", Value: true},
				traceevent.Field{Key: "before_messages", Value: before},
				traceevent.Field{Key: "trigger", Value: "pre_llm_threshold"},
			)
			stream.Push(NewCompactionStartEvent(CompactionInfo{
				Auto:    true,
				Before:  before,
				Trigger: "pre_llm_threshold",
			}))

			compacted, compactErr := config.Compactor.Compact(agentCtx.Messages)
			if compactErr != nil {
				compactErr = WithErrorStack(compactErr)
				slog.Error("Pre-LLM compaction failed", "error", compactErr)
				compactionSpan.AddField("error", true)
				compactionSpan.AddField("error_message", compactErr.Error())
				if stack := ErrorStack(compactErr); stack != "" {
					compactionSpan.AddField("error_stack", stack)
				}
				compactionSpan.End()
				stream.Push(NewCompactionEndEvent(CompactionInfo{
					Auto:    true,
					Before:  before,
					Error:   compactErr.Error(),
					Trigger: "pre_llm_threshold",
				}))
			} else {
				if compacted != nil {
					agentCtx.Messages = compacted
				}
				after := len(agentCtx.Messages)
				compactionSpan.AddField("after_messages", after)
				compactionSpan.End()
				stream.Push(NewCompactionEndEvent(CompactionInfo{
					Auto:    true,
					Before:  before,
					After:   after,
					Trigger: "pre_llm_threshold",
				}))
			}
		}

		// Stream assistant response with retry logic
		msg, err := streamAssistantResponseWithRetry(ctx, agentCtx, config, stream)
		if err != nil {
			if llm.IsContextLengthExceeded(err) && config.Compactor != nil && compactionRecoveries < maxCompactionRecoveries {
				before := len(agentCtx.Messages)
				compactionSpan := traceevent.StartSpan(ctx, "compaction", traceevent.CategoryEvent,
					traceevent.Field{Key: "source", Value: "context_limit_recovery"},
					traceevent.Field{Key: "auto", Value: true},
					traceevent.Field{Key: "before_messages", Value: before},
					traceevent.Field{Key: "trigger", Value: "context_limit_recovery"},
				)
				stream.Push(NewCompactionStartEvent(CompactionInfo{
					Auto:    true,
					Before:  before,
					Trigger: "context_limit_recovery",
				}))
				compacted, compactErr := config.Compactor.Compact(agentCtx.Messages)
				if compactErr != nil {
					compactErr = WithErrorStack(compactErr)
					slog.Error("Compaction recovery failed", "error", compactErr)
					compactionSpan.AddField("error", true)
					compactionSpan.AddField("error_message", compactErr.Error())
					if stack := ErrorStack(compactErr); stack != "" {
						compactionSpan.AddField("error_stack", stack)
					}
					compactionSpan.End()
					stream.Push(NewCompactionEndEvent(CompactionInfo{
						Auto:    true,
						Before:  before,
						Error:   compactErr.Error(),
						Trigger: "context_limit_recovery",
					}))
				} else {
					compactionRecoveries++
					agentCtx.Messages = compacted
					compactionSpan.AddField("after_messages", len(compacted))
					compactionSpan.End()
					stream.Push(NewCompactionEndEvent(CompactionInfo{
						Auto:    true,
						Before:  before,
						After:   len(compacted),
						Trigger: "context_limit_recovery",
					}))
					continue
				}
			}

			slog.Error("Error streaming response", "error", err)
			traceFields := []traceevent.Field{
				{Key: "error_message", Value: err.Error()},
			}
			if stack := ErrorStack(err); stack != "" {
				traceFields = append(traceFields, traceevent.Field{Key: "error_stack", Value: stack})
			}
			traceevent.Log(ctx, traceevent.CategoryEvent, "run_loop_error", traceFields...)
			stream.Push(NewErrorEvent(err))
			stream.Push(NewTurnEndEvent(msg, nil))
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}

		if msg == nil {
			// Message was nil (aborted)
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}

		agentCtx.Messages = append(agentCtx.Messages, *msg)
		newMessages = append(newMessages, *msg)

		// Check for error or abort
		if msg.StopReason == "error" || msg.StopReason == "aborted" {
			stream.Push(NewTurnEndEvent(msg, nil))
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}

		// Check for tool calls
		toolCalls := msg.ExtractToolCalls()
		hasMoreToolCalls := len(toolCalls) > 0
		if hasMoreToolCalls && loopGuard != nil {
			if blocked, reason := loopGuard.Observe(toolCalls); blocked {
				slog.Warn("[Loop] tool call loop guard triggered", "reason", reason)
				stream.Push(NewLoopGuardTriggeredEvent(LoopGuardInfo{Reason: reason}))
				traceevent.Log(ctx, traceevent.CategoryEvent, "tool_loop_guard_triggered",
					traceevent.Field{Key: "reason", Value: reason},
					traceevent.Field{Key: "call_count", Value: len(toolCalls)},
				)
				sanitizeMessageForToolLoopGuard(msg, reason)
				agentCtx.Messages[len(agentCtx.Messages)-1] = *msg
				newMessages[len(newMessages)-1] = *msg
				hasMoreToolCalls = false
			}
		}

		var toolResults []AgentMessage
		if hasMoreToolCalls {
			toolResults = executeToolCalls(ctx, agentCtx.Tools, agentCtx.GetAllowedToolsMap(), msg, stream, config.Executor, config.Metrics, config.ToolOutput)
			for _, result := range toolResults {
				agentCtx.Messages = append(agentCtx.Messages, result)
				newMessages = append(newMessages, result)
			}
			if asyncSummarizer != nil {
				asyncSummarizer.schedule(agentCtx)
				asyncSummarizer.applyReady(agentCtx)
			} else {
				maybeSummarizeToolResults(ctx, agentCtx, config)
			}
		}

		stream.Push(NewTurnEndEvent(msg, toolResults))

		// If no more tool calls, end the conversation
		if !hasMoreToolCalls {
			break
		}
	}

	stream.Push(NewAgentEndEvent(agentCtx.Messages))
}

type toolLoopGuard struct {
	maxConsecutive int
	maxPerToolName int

	lastSignature  string
	consecutiveRun int
	toolCallTotals map[string]int
}

func newToolLoopGuard(config *LoopConfig) *toolLoopGuard {
	if config == nil {
		return nil
	}
	maxConsecutive := resolveLoopGuardLimit(config.MaxConsecutiveToolCalls, defaultLoopMaxConsecutiveToolCalls)
	maxPerToolName := resolveLoopGuardLimit(config.MaxToolCallsPerName, defaultLoopMaxToolCallsPerName)
	if maxConsecutive == 0 && maxPerToolName == 0 {
		return nil
	}
	return &toolLoopGuard{
		maxConsecutive: maxConsecutive,
		maxPerToolName: maxPerToolName,
		toolCallTotals: make(map[string]int),
	}
}

func resolveLoopGuardLimit(value, defaultValue int) int {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return defaultValue
	}
	return value
}

func (g *toolLoopGuard) Observe(toolCalls []ToolCallContent) (bool, string) {
	for _, tc := range toolCalls {
		name := strings.ToLower(strings.TrimSpace(tc.Name))
		if name == "" {
			name = "unknown"
		}
		signature := name + ":" + hashAny(tc.Arguments)

		if signature == g.lastSignature {
			g.consecutiveRun++
		} else {
			g.lastSignature = signature
			g.consecutiveRun = 1
		}

		if g.maxConsecutive > 0 && g.consecutiveRun > g.maxConsecutive {
			return true, fmt.Sprintf("detected %d consecutive identical tool calls (%s)", g.consecutiveRun, name)
		}

		g.toolCallTotals[name]++
		if g.maxPerToolName > 0 && g.toolCallTotals[name] > g.maxPerToolName {
			return true, fmt.Sprintf("tool %q called %d times in one run", name, g.toolCallTotals[name])
		}
	}
	return false, ""
}

func sanitizeMessageForToolLoopGuard(msg *AgentMessage, reason string) {
	if msg == nil {
		return
	}

	filtered := make([]ContentBlock, 0, len(msg.Content)+1)
	for _, block := range msg.Content {
		switch block.(type) {
		case ToolCallContent:
			continue
		default:
			filtered = append(filtered, block)
		}
	}
	filtered = append(filtered, TextContent{
		Type: "text",
		Text: "\n\n[Loop guard] Stopped repeated tool execution to prevent an infinite loop.\nReason: " + strings.TrimSpace(reason),
	})
	msg.Content = filtered
	msg.StopReason = "aborted"
}

// streamAssistantResponseWithRetry streams the assistant's response with retry logic.
func streamAssistantResponseWithRetry(
	ctx context.Context,
	agentCtx *AgentContext,
	config *LoopConfig,
	stream *llm.EventStream[AgentEvent, []AgentMessage],
) (*AgentMessage, error) {
	span := traceevent.StartSpan(ctx, "streamAssistantResponseWithRetry", traceevent.CategoryEvent)
	defer span.End()

	maxRetries := config.MaxLLMRetries
	if maxRetries < 0 {
		maxRetries = defaultLLMMaxRetries
	}
	baseDelay := config.RetryBaseDelay
	if baseDelay < defaultRetryBaseDelay {
		baseDelay = defaultRetryBaseDelay
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<(attempt-1))
			if llm.IsRateLimit(lastErr) {
				// Respect provider backoff hint when available.
				retryAfter := llm.RetryAfter(lastErr)
				if retryAfter > delay {
					delay = retryAfter
				}
				if delay < 2*time.Second {
					delay = 2 * time.Second
				}
				delay = jitterDelay(delay)
			}
			slog.Info("[Loop] Retrying LLM call",
				"attempt", attempt,
				"maxRetries", maxRetries,
				"delay", delay,
				"rateLimit", llm.IsRateLimit(lastErr))

			select {
			case <-time.After(delay):
				// Continue with retry
			case <-ctx.Done():
				if lastErr != nil {
					return nil, lastErr
				}
				cause := context.Cause(ctx)
				if cause == nil {
					cause = ctx.Err()
				}
				return nil, WithErrorStack(cause)
			}
		}

		attemptCtx := context.WithValue(ctx, llmAttemptKey, attempt)
		msg, err := streamAssistantResponseFn(attemptCtx, agentCtx, config, stream)
		if err == nil {
			return msg, nil
		}

		if llm.IsContextLengthExceeded(err) {
			return nil, WithErrorStack(err)
		}

		lastErr = WithErrorStack(err)
		slog.Error("[Loop] LLM call failed", "attempt", attempt, "maxRetries", maxRetries, "error", err)

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			return nil, lastErr
		}
		if !shouldRetryLLMError(lastErr) {
			return nil, lastErr
		}
	}

	// All retries exhausted
	return nil, lastErr
}

// streamAssistantResponse streams the assistant's response from the LLM.
func streamAssistantResponse(
	ctx context.Context,
	agentCtx *AgentContext,
	config *LoopConfig,
	stream *llm.EventStream[AgentEvent, []AgentMessage],
) (*AgentMessage, error) {
	thinkingLevel := prompt.NormalizeThinkingLevel(config.ThinkingLevel)

	// Create timeout context for LLM calls
	llmTimeout := 120 * time.Second
	llmCtx, llmCancel := context.WithTimeout(ctx, llmTimeout)
	defer llmCancel()

	// Convert messages to LLM format
	llmMessages := ConvertMessagesToLLM(ctx, agentCtx.Messages)

	slog.Debug("[Loop] Sending messages to LLM", "count", len(llmMessages))

	// Convert tools to LLM format
	llmTools := ConvertToolsToLLM(ctx, agentCtx.Tools)

	// Build LLM context
	systemPrompt := agentCtx.SystemPrompt
	if instruction := prompt.ThinkingInstruction(thinkingLevel); instruction != "" {
		if strings.TrimSpace(systemPrompt) == "" {
			systemPrompt = instruction
		} else {
			systemPrompt = systemPrompt + "\n\n" + instruction
		}
	}
	llmCtxParams := llm.LLMContext{
		SystemPrompt: systemPrompt,
		Messages:     llmMessages,
		Tools:        llmTools,
	}
	//	emitLLMRequestSnapshot(ctx, config.Model, llmCtxParams)

	// Stream LLM response
	llmStart := time.Now()
	llmSpan := traceevent.StartSpan(ctx, "llm_call", traceevent.CategoryLLM,
		traceevent.Field{Key: "model", Value: config.Model.ID},
		traceevent.Field{Key: "provider", Value: config.Model.Provider},
		traceevent.Field{Key: "api", Value: config.Model.API},
	)
	defer llmSpan.End()
	firstTokenRecorded := false
	firstTokenLatency := time.Duration(0)

	llmStream := llm.StreamLLM(llmCtx, config.Model, llmCtxParams, config.APIKey)

	type toolCallState struct {
		id        string
		name      string
		callType  string
		arguments string
	}

	var partialMessage *AgentMessage
	var textBuilder strings.Builder
	toolCalls := map[int]*toolCallState{}

	buildContent := func(text string, calls map[int]*toolCallState) []ContentBlock {
		content := make([]ContentBlock, 0, 1+len(calls))
		if text != "" {
			content = append(content, TextContent{
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

			content = append(content, ToolCallContent{
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
			partialMessage = new(AgentMessage)
			*partialMessage = NewAssistantMessage()
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
				thinkingContent := ThinkingContent{
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
			var finalMessage AgentMessage
			if e.Message != nil {
				finalMessage = ConvertLLMMessageToAgent(*e.Message)
			} else if partialMessage != nil {
				finalMessage = *partialMessage
			} else {
				finalMessage = NewAssistantMessage()
			}

			finalMessage.API = config.Model.API
			finalMessage.Provider = config.Model.Provider
			finalMessage.Model = config.Model.ID
			finalMessage.Timestamp = time.Now().UnixMilli()
			finalMessage.StopReason = e.StopReason
			finalMessage.Usage = &Usage{
				InputTokens:  e.Usage.InputTokens,
				OutputTokens: e.Usage.OutputTokens,
				TotalTokens:  e.Usage.TotalTokens,
			}

			// Try to inject tool calls from tagged text
			if updated, ok := injectToolCallsFromTaggedText(finalMessage); ok {
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
					slog.Debug("[Loop] assistant response contains tags but no tool calls injected",
						"text_preview", truncateLine(text, 200),
						"stop_reason", e.StopReason,
						"issues", issues)
				}
			}

			stream.Push(NewMessageEndEvent(finalMessage))
			return &finalMessage, nil

		case llm.LLMErrorEvent:
			wrappedErr := WithErrorStack(e.Error)
			llmSpan.AddField("error", wrappedErr.Error())
			if stack := ErrorStack(wrappedErr); stack != "" {
				llmSpan.AddField("error_stack", stack)
			}
			if firstTokenLatency > 0 {
				llmSpan.AddField("first_token_ms", firstTokenLatency.Milliseconds())
			}
			return nil, wrappedErr
		}
	}

	return partialMessage, nil
}

type llmRequestSnapshot struct {
	Attempt           int
	RequestHash       string
	MessagesHash      string
	ToolsHash         string
	SystemPromptHash  string
	MessageCount      int
	UserMessages      int
	AssistantMessages int
	ToolMessages      int
	SystemChars       int
	LastRole          string
	LastMessageHash   string
	LastUserHash      string
}

func emitLLMRequestSnapshot(ctx context.Context, model llm.Model, llmCtx llm.LLMContext) {
	snapshot := buildLLMRequestSnapshot(ctx, model, llmCtx)
	traceevent.Log(ctx, traceevent.CategoryLLM, "llm_request_snapshot",
		traceevent.Field{Key: "attempt", Value: snapshot.Attempt},
		traceevent.Field{Key: "request_hash", Value: snapshot.RequestHash},
		traceevent.Field{Key: "messages_hash", Value: snapshot.MessagesHash},
		traceevent.Field{Key: "tools_hash", Value: snapshot.ToolsHash},
		traceevent.Field{Key: "system_prompt_hash", Value: snapshot.SystemPromptHash},
		traceevent.Field{Key: "message_count", Value: snapshot.MessageCount},
		traceevent.Field{Key: "user_messages", Value: snapshot.UserMessages},
		traceevent.Field{Key: "assistant_messages", Value: snapshot.AssistantMessages},
		traceevent.Field{Key: "tool_messages", Value: snapshot.ToolMessages},
		traceevent.Field{Key: "system_chars", Value: snapshot.SystemChars},
		traceevent.Field{Key: "last_role", Value: snapshot.LastRole},
		traceevent.Field{Key: "last_message_hash", Value: snapshot.LastMessageHash},
		traceevent.Field{Key: "last_user_hash", Value: snapshot.LastUserHash},
	)
	slog.Debug("[Loop] LLM request snapshot",
		"attempt", snapshot.Attempt,
		"requestHash", snapshot.RequestHash,
		"messagesHash", snapshot.MessagesHash,
		"toolsHash", snapshot.ToolsHash,
		"messageCount", snapshot.MessageCount,
		"lastRole", snapshot.LastRole)
}

func buildLLMRequestSnapshot(ctx context.Context, model llm.Model, llmCtx llm.LLMContext) llmRequestSnapshot {
	snapshot := llmRequestSnapshot{
		Attempt:          llmAttemptFromContext(ctx),
		MessagesHash:     hashAny(llmCtx.Messages),
		ToolsHash:        hashAny(llmCtx.Tools),
		SystemPromptHash: hashAny(llmCtx.SystemPrompt),
		MessageCount:     len(llmCtx.Messages),
		SystemChars:      len(llmCtx.SystemPrompt),
	}

	for _, msg := range llmCtx.Messages {
		switch msg.Role {
		case "user":
			snapshot.UserMessages++
		case "assistant":
			snapshot.AssistantMessages++
		case "tool":
			snapshot.ToolMessages++
		}
	}

	if n := len(llmCtx.Messages); n > 0 {
		last := llmCtx.Messages[n-1]
		snapshot.LastRole = last.Role
		snapshot.LastMessageHash = hashAny(last)
	}
	for i := len(llmCtx.Messages) - 1; i >= 0; i-- {
		if llmCtx.Messages[i].Role == "user" {
			snapshot.LastUserHash = hashAny(llmCtx.Messages[i])
			break
		}
	}

	reqMessages := llmCtx.Messages
	if strings.TrimSpace(llmCtx.SystemPrompt) != "" {
		systemMsg := llm.LLMMessage{
			Role:    "system",
			Content: llmCtx.SystemPrompt,
		}
		reqMessages = append([]llm.LLMMessage{systemMsg}, reqMessages...)
	}

	reqBody := map[string]any{
		"model":    model.ID,
		"messages": reqMessages,
		"stream":   true,
	}
	if len(llmCtx.Tools) > 0 {
		reqBody["tools"] = llmCtx.Tools
		reqBody["tool_choice"] = "auto"
	}
	snapshot.RequestHash = hashAny(reqBody)
	return snapshot
}

func llmAttemptFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	value := ctx.Value(llmAttemptKey)
	if attempt, ok := value.(int); ok {
		return attempt
	}
	return 0
}

func hashAny(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// executeToolCalls executes tool calls from an assistant message.
func executeToolCalls(
	ctx context.Context,
	tools []Tool,
	allowedTools map[string]bool,
	assistantMsg *AgentMessage,
	stream *llm.EventStream[AgentEvent, []AgentMessage],
	executor *ExecutorPool,
	_ *Metrics,
	toolOutputLimits ToolOutputLimits,
) []AgentMessage {
	toolCalls := assistantMsg.ExtractToolCalls()
	if len(toolCalls) == 0 {
		return nil
	}

	type toolExecutionPlan struct {
		index      int
		normalized ToolCallContent
		tool       Tool
		span       *traceevent.Span
	}
	type toolExecutionOutcome struct {
		plan     toolExecutionPlan
		content  []ContentBlock
		err      error
		duration time.Duration
	}

	resultsByIndex := make([]*AgentMessage, len(toolCalls))
	plans := make([]toolExecutionPlan, 0, len(toolCalls))
	toolsByName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		toolsByName[tool.Name()] = tool
	}
	availableToolNames := make([]string, 0, len(toolsByName))
	for name := range toolsByName {
		availableToolNames = append(availableToolNames, name)
	}
	sort.Strings(availableToolNames)

	for i, tc := range toolCalls {
		rawName := strings.ToLower(strings.TrimSpace(tc.Name))
		normalized := normalizeToolCall(tc)
		toolSpan := traceevent.StartSpan(ctx, "tool_execution", traceevent.CategoryTool,
			traceevent.Field{Key: "tool", Value: normalized.Name},
			traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
			traceevent.Field{Key: "raw_name", Value: rawName},
		)
		if normalized.Name != rawName {
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_normalized",
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "normalized_name", Value: normalized.Name},
				traceevent.Field{Key: "raw_args", Value: tc.Arguments},
				traceevent.Field{Key: "normalized_args", Value: normalized.Arguments},
			)
		}
		if isGenericToolName(normalized.Name) {
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_unresolved",
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "normalized_name", Value: normalized.Name},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
				traceevent.Field{Key: "available_tools", Value: availableToolNames},
			)
			slog.Warn("[Loop] unresolved tool call name",
				"rawName", rawName,
				"normalizedName", normalized.Name,
				"availableTools", availableToolNames)
		}
		args, argErr := coerceToolArguments(normalized.Name, normalized.Arguments)
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_start",
			traceevent.Field{Key: "tool", Value: normalized.Name},
			traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
			traceevent.Field{Key: "args", Value: normalized.Arguments},
		)
		if argErr != nil {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", argErr.Error())
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_invalid_args",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "raw_args", Value: tc.Arguments},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
				traceevent.Field{Key: "error", Value: argErr.Error()},
			)
			errorMsg := buildInvalidToolArgsMessage(normalized.Name, argErr)
			result := NewToolResultMessage(normalized.ID, normalized.Name, []ContentBlock{
				TextContent{Type: "text", Text: errorMsg},
			}, true)
			stream.Push(NewToolExecutionStartEvent(normalized.ID, normalized.Name, normalized.Arguments))
			stream.Push(NewToolExecutionEndEvent(normalized.ID, normalized.Name, &result, true))
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: 0},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: argErr.Error()},
			)
			resultCopy := result
			resultsByIndex[i] = &resultCopy
			continue
		}

		normalized.Arguments = args
		stream.Push(NewToolExecutionStartEvent(normalized.ID, normalized.Name, normalized.Arguments))

		tool := toolsByName[normalized.Name]
		if tool == nil {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", "tool not found")
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_unresolved",
				traceevent.Field{Key: "raw_name", Value: rawName},
				traceevent.Field{Key: "normalized_name", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
				traceevent.Field{Key: "available_tools", Value: availableToolNames},
			)
			slog.Warn("[Loop] tool not registered",
				"tool", normalized.Name,
				"rawName", rawName,
				"availableTools", availableToolNames)
			content := truncateToolContent([]ContentBlock{
				TextContent{Type: "text", Text: "Tool not found"},
			}, toolOutputLimits)
			result := NewToolResultMessage(normalized.ID, normalized.Name, content, true)
			stream.Push(NewToolExecutionEndEvent(normalized.ID, normalized.Name, &result, true))
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: 0},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: "tool not found"},
			)
			resultCopy := result
			resultsByIndex[i] = &resultCopy
			continue
		}

		// Check if tool is allowed by whitelist
		if allowedTools != nil && !allowedTools[normalized.Name] {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", "tool not allowed")
			toolSpan.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_not_allowed",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "args", Value: normalized.Arguments},
			)
			slog.Warn("[Loop] tool not allowed by whitelist",
				"tool", normalized.Name,
				"toolCallID", normalized.ID)
			content := truncateToolContent([]ContentBlock{
				TextContent{Type: "text", Text: fmt.Sprintf("Tool %q is not allowed in this context", normalized.Name)},
			}, toolOutputLimits)
			result := NewToolResultMessage(normalized.ID, normalized.Name, content, true)
			stream.Push(NewToolExecutionEndEvent(normalized.ID, normalized.Name, &result, true))
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: 0},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: "tool not allowed"},
			)
			resultCopy := result
			resultsByIndex[i] = &resultCopy
			continue
		}

		plans = append(plans, toolExecutionPlan{
			index:      i,
			normalized: normalized,
			tool:       tool,
			span:       toolSpan,
		})
	}

	outcomes := make(chan toolExecutionOutcome, len(plans))
	var wg sync.WaitGroup

	for _, plan := range plans {
		wg.Add(1)
		go func(plan toolExecutionPlan) {
			defer wg.Done()
			start := time.Now()
			var content []ContentBlock
			var err error
			if executor != nil {
				content, err = executor.Execute(ctx, plan.tool, plan.normalized.Arguments)
			} else {
				content, err = plan.tool.Execute(ctx, plan.normalized.Arguments)
			}
			outcomes <- toolExecutionOutcome{
				plan:     plan,
				content:  content,
				err:      err,
				duration: time.Since(start),
			}
		}(plan)
	}

	wg.Wait()
	close(outcomes)

	outcomeByIndex := make(map[int]toolExecutionOutcome, len(plans))
	for outcome := range outcomes {
		outcomeByIndex[outcome.plan.index] = outcome
	}

	for _, plan := range plans {
		outcome, ok := outcomeByIndex[plan.index]
		if !ok {
			continue
		}
		var result AgentMessage
		if outcome.err != nil {
			content := truncateToolContent([]ContentBlock{
				TextContent{Type: "text", Text: outcome.err.Error()},
			}, toolOutputLimits)
			result = NewToolResultMessage(plan.normalized.ID, plan.normalized.Name, content, true)
			stream.Push(NewToolExecutionEndEvent(plan.normalized.ID, plan.normalized.Name, &result, true))
			plan.span.AddField("error", true)
			plan.span.AddField("error_message", outcome.err.Error())
			plan.span.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: plan.normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: plan.normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: outcome.duration.Milliseconds()},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: outcome.err.Error()},
			)
		} else {
			content := truncateToolContent(outcome.content, toolOutputLimits)
			result = NewToolResultMessage(plan.normalized.ID, plan.normalized.Name, content, false)
			stream.Push(NewToolExecutionEndEvent(plan.normalized.ID, plan.normalized.Name, &result, false))
			plan.span.AddField("error", false)
			plan.span.End()
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: plan.normalized.Name},
				traceevent.Field{Key: "tool_call_id", Value: plan.normalized.ID},
				traceevent.Field{Key: "duration_ms", Value: outcome.duration.Milliseconds()},
				traceevent.Field{Key: "error", Value: false},
			)
		}

		resultCopy := result
		resultsByIndex[plan.index] = &resultCopy
	}

	results := make([]AgentMessage, 0, len(toolCalls))
	for _, result := range resultsByIndex {
		if result != nil {
			results = append(results, *result)
		}
	}
	return results
}

func buildInvalidToolArgsMessage(toolName string, argErr error) string {
	errorMsg := fmt.Sprintf("Invalid tool arguments for '%s': %v\n\nCorrect format:\n", toolName, argErr)
	switch toolName {
	case "read":
		errorMsg += `<read>
  <path>file.txt</path>
</read>`
	case "write":
		errorMsg += `<write>
  <path>file.txt</path>
  <content>content here</content>
</write>`
	case "edit":
		errorMsg += `<edit>
  <path>file.txt</path>
  <oldText>old text</oldText>
  <newText>new text</newText>
</edit>`
	case "bash":
		errorMsg += `<bash>
  <command>your command here</command>
</bash>

Alternatively:
<bash>command here</bash>`
	case "grep":
		errorMsg += `<grep>
  <pattern>search pattern</pattern>
  <path>optional path</path>
</grep>`
	}
	return errorMsg
}
