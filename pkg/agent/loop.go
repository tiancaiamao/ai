package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/llm"
)

const (
	defaultLLMMaxRetries  = 1               // Maximum retry attempts for LLM calls
	defaultRetryBaseDelay = 1 * time.Second // Base delay for exponential backoff
)

// LoopConfig contains configuration for the agent loop.
type LoopConfig struct {
	Model          llm.Model
	APIKey         string
	Executor       *ExecutorPool // Tool executor with concurrency control
	Metrics        *Metrics      // Metrics collector
	ToolOutput     ToolOutputLimits
	MaxLLMRetries  int           // Maximum number of retries for LLM calls
	RetryBaseDelay time.Duration // Base delay for exponential backoff
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
	for {
		// Check for context cancellation
		select {
		case <-ctx.Done():
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		default:
		}

		// Stream assistant response with retry logic
		msg, err := streamAssistantResponseWithRetry(ctx, agentCtx, config, stream)
		if err != nil {
			slog.Error("Error streaming response", "error", err)
			stream.Push(NewTurnEndEvent(msg, nil))
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}

		if msg == nil {
			// Message was nil (aborted)
			stream.Push(NewAgentEndEvent(agentCtx.Messages))
			return
		}

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

		var toolResults []AgentMessage
		if hasMoreToolCalls {
			toolResults = executeToolCalls(ctx, agentCtx.Tools, msg, stream, config.Executor, config.Metrics, config.ToolOutput)
			for _, result := range toolResults {
				agentCtx.Messages = append(agentCtx.Messages, result)
				newMessages = append(newMessages, result)
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

// streamAssistantResponseWithRetry streams the assistant's response with retry logic.
func streamAssistantResponseWithRetry(
	ctx context.Context,
	agentCtx *AgentContext,
	config *LoopConfig,
	stream *llm.EventStream[AgentEvent, []AgentMessage],
) (*AgentMessage, error) {
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
			slog.Info("[Loop] Retrying LLM call",
				"attempt", attempt,
				"maxRetries", maxRetries,
				"delay", delay)

			select {
			case <-time.After(delay):
				// Continue with retry
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		msg, err := streamAssistantResponse(ctx, agentCtx, config, stream)
		if err == nil {
			return msg, nil
		}

		lastErr = err
		slog.Error("[Loop] LLM call failed", "attempt", attempt, "maxRetries", maxRetries, "error", err)

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			return nil, ctx.Err()
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
	// Create timeout context for LLM calls
	llmTimeout := 120 * time.Second
	llmCtx, llmCancel := context.WithTimeout(ctx, llmTimeout)
	defer llmCancel()

	// Convert messages to LLM format
	llmMessages := ConvertMessagesToLLM(agentCtx.Messages)

	slog.Debug("[Loop] Sending messages to LLM", "count", len(llmMessages))

	// Convert tools to LLM format
	llmTools := ConvertToolsToLLM(agentCtx.Tools)

	// Build LLM context
	llmCtxParams := llm.LLMContext{
		SystemPrompt: agentCtx.SystemPrompt,
		Messages:     llmMessages,
		Tools:        llmTools,
	}

	// Stream LLM response
	llmStart := time.Now()
	if config.Metrics != nil {
		config.Metrics.RecordLLMStart()
	}
	recorded := false
	firstTokenRecorded := false
	firstTokenLatency := time.Duration(0)
	defer func() {
		if config.Metrics != nil && !recorded && ctx.Err() != nil {
			config.Metrics.RecordLLMCall(0, 0, 0, 0, time.Since(llmStart), firstTokenLatency, ctx.Err())
		}
	}()

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
				if !firstTokenRecorded {
					firstTokenRecorded = true
					firstTokenLatency = time.Since(llmStart)
				}
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
			if config.Metrics != nil && !recorded {
				config.Metrics.RecordLLMCall(
					e.Usage.InputTokens,
					e.Usage.OutputTokens,
					0,
					0,
					time.Since(llmStart),
					firstTokenLatency,
					nil,
				)
				recorded = true
			}
			if partialMessage != nil && textBuilder.Len() > 0 {
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

			stream.Push(NewMessageEndEvent(finalMessage))
			return &finalMessage, nil

		case llm.LLMErrorEvent:
			if config.Metrics != nil && !recorded {
				config.Metrics.RecordLLMCall(0, 0, 0, 0, time.Since(llmStart), firstTokenLatency, e.Error)
				recorded = true
			}
			return nil, e.Error
		}
	}

	return partialMessage, nil
}

// executeToolCalls executes tool calls from an assistant message.
func executeToolCalls(
	ctx context.Context,
	tools []Tool,
	assistantMsg *AgentMessage,
	stream *llm.EventStream[AgentEvent, []AgentMessage],
	executor *ExecutorPool,
	metrics *Metrics,
	toolOutputLimits ToolOutputLimits,
) []AgentMessage {
	toolCalls := assistantMsg.ExtractToolCalls()
	results := make([]AgentMessage, 0, len(toolCalls))

	for _, tc := range toolCalls {
		stream.Push(NewToolExecutionStartEvent(tc.ID, tc.Name, tc.Arguments))

		// Find tool
		var tool Tool
		for _, t := range tools {
			if t.Name() == tc.Name {
				tool = t
				break
			}
		}

		if tool == nil {
			if metrics != nil {
				metrics.RecordToolExecution(tc.Name, 0, fmt.Errorf("tool not found"), 0)
			}
			content := truncateToolContent([]ContentBlock{
				TextContent{Type: "text", Text: "Tool not found"},
			}, toolOutputLimits)
			result := NewToolResultMessage(tc.ID, tc.Name, content, true)
			results = append(results, result)
			stream.Push(NewToolExecutionEndEvent(tc.ID, tc.Name, &result, true))
			continue
		}

		// Execute tool with concurrency control and retry
		var content []ContentBlock
		var err error

		start := time.Now()
		if executor != nil {
			content, err = executor.Execute(ctx, tool, tc.Arguments)
		} else {
			// Fallback to direct execution
			content, err = tool.Execute(ctx, tc.Arguments)
		}
		if metrics != nil {
			metrics.RecordToolExecution(tc.Name, time.Since(start), err, 0)
		}

		if err != nil {
			content := truncateToolContent([]ContentBlock{
				TextContent{Type: "text", Text: err.Error()},
			}, toolOutputLimits)
			result := NewToolResultMessage(tc.ID, tc.Name, content, true)
			results = append(results, result)
			stream.Push(NewToolExecutionEndEvent(tc.ID, tc.Name, &result, true))
		} else {
			content = truncateToolContent(content, toolOutputLimits)
			result := NewToolResultMessage(tc.ID, tc.Name, content, false)
			results = append(results, result)
			stream.Push(NewToolExecutionEndEvent(tc.ID, tc.Name, &result, false))
		}
	}

	return results
}
