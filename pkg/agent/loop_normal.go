package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/traceevent"
	"log/slog"
)

// ExecuteNormalMode executes a turn in normal mode.
func (a *AgentNew) ExecuteNormalMode(ctx context.Context, userMessage string) error {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	// 1. Check trigger conditions
	agentctx.LogSnapshotEvaluated(ctx, a.snapshot)
	shouldTrigger, urgency, reason := a.triggerChecker.ShouldTrigger(a.snapshot)
	agentctx.LogTriggerChecked(ctx, shouldTrigger, urgency, reason, a.snapshot)
	if shouldTrigger && urgency != agentctx.UrgencySkip {
		slog.Info("[AgentNew] Context management trigger detected",
			"urgency", urgency,
			"reason", reason,
		)

		// We need to switch to context management mode
		// But we can't do it while holding the lock, so we'll return a special error
		// The caller should handle this by calling ExecuteContextMgmtMode
		return &ContextMgmtTriggerError{
			Urgency: urgency,
			Reason:  reason,
		}
	}

	// 2. Append user message to snapshot
	userMsg := agentctx.AgentMessage{
		Role:         "user",
		Content:      []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: userMessage}},
		Timestamp:    time.Now().Unix(),
		AgentVisible: true,
		UserVisible:  true,
	}

	a.snapshot.RecentMessages = append(a.snapshot.RecentMessages, userMsg)
	traceevent.Log(ctx, traceevent.CategoryEvent, "message_start",
		traceevent.Field{Key: "role", Value: "user"},
		traceevent.Field{Key: "chars", Value: len(userMessage)},
	)
	traceevent.Log(ctx, traceevent.CategoryEvent, "message_end",
		traceevent.Field{Key: "role", Value: "user"},
		traceevent.Field{Key: "chars", Value: len(userMessage)},
	)

	// 3. Persist to journal
	if err := a.journal.AppendMessage(userMsg); err != nil {
		return fmt.Errorf("failed to append message: %w", err)
	}

	// 4. Build LLM request
	llmMessages, systemPrompt := a.buildNormalModeRequest(ctx)

	llmCtx := llm.LLMContext{
		SystemPrompt: systemPrompt,
		Messages:     llmMessages,
		Tools:        ConvertToolsToLLM(ctx, a.allTools),
	}

	// 5. Call LLM
	llmStart := time.Now()
	llmSpan := traceevent.StartSpan(ctx, "llm_call", traceevent.CategoryLLM,
		traceevent.Field{Key: "model", Value: a.model.ID},
		traceevent.Field{Key: "provider", Value: a.model.Provider},
		traceevent.Field{Key: "api", Value: a.model.API},
		traceevent.Field{Key: "timeout_ms", Value: int64((2 * time.Minute) / time.Millisecond)},
	)
	defer llmSpan.End()

	stream := llm.StreamLLM(
		ctx,
		*a.model,
		llmCtx,
		a.apiKey,
		2*time.Minute, // Chunk interval timeout
	)

	// 6. Process response
	assistantMsg, toolResults, err := a.processLLMResponse(ctx, stream, llmStart)
	if err != nil {
		llmSpan.AddField("error", true)
		llmSpan.AddField("error_message", err.Error())
		return err
	}
	if assistantMsg != nil {
		llmSpan.AddField("stop_reason", assistantMsg.StopReason)
		if assistantMsg.Usage != nil {
			llmSpan.AddField("input_tokens", assistantMsg.Usage.InputTokens)
			llmSpan.AddField("output_tokens", assistantMsg.Usage.OutputTokens)
			llmSpan.AddField("total_tokens", assistantMsg.Usage.TotalTokens)
		}
	}

	// 7. Append assistant message
	a.snapshot.RecentMessages = append(a.snapshot.RecentMessages, *assistantMsg)
	traceevent.Log(ctx, traceevent.CategoryEvent, "message_end",
		traceevent.Field{Key: "role", Value: "assistant"},
		traceevent.Field{Key: "stop_reason", Value: assistantMsg.StopReason},
		traceevent.Field{Key: "chars", Value: len(assistantMsg.ExtractText())},
	)
	if err := a.journal.AppendMessage(*assistantMsg); err != nil {
		return fmt.Errorf("failed to append assistant message: %w", err)
	}

	// 8. Append tool results
	for _, result := range toolResults {
		a.snapshot.RecentMessages = append(a.snapshot.RecentMessages, result)
		if err := a.journal.AppendMessage(result); err != nil {
			slog.Warn("[AgentNew] Failed to append tool result", "error", err)
		}
	}

	// 9. Update turn count
	a.snapshot.AgentState.TotalTurns++
	a.snapshot.AgentState.TurnsSinceLastTrigger++
	a.snapshot.AgentState.UpdatedAt = time.Now()

	return nil
}

// buildNormalModeRequest builds the LLM request for normal mode.
func (a *AgentNew) buildNormalModeRequest(ctx context.Context) ([]llm.LLMMessage, string) {
	// Build system prompt
	systemPrompt := prompt.BuildSystemPrompt(agentctx.ModeNormal)

	// Convert recent messages to LLM format
	var llmMessages []llm.LLMMessage

	for _, msg := range a.snapshot.RecentMessages {
		if !msg.IsAgentVisible() || msg.IsTruncated() {
			continue
		}

		// Convert message to LLM format
		llmMsg := llm.LLMMessage{
			Role:    msg.Role,
			Content: msg.ExtractText(),
		}
		llmMessages = append(llmMessages, llmMsg)
	}

	// Inject runtime state before last user message
	runtimeState := a.buildRuntimeState()
	if runtimeState != "" {
		runtimeMsg := llm.LLMMessage{
			Role:    "user",
			Content: runtimeState,
		}
		llmMessages = insertBeforeLastUserMessage(llmMessages, runtimeMsg)
	}

	// Inject LLM context if available
	if a.snapshot.LLMContext != "" {
		llmContextMsg := llm.LLMMessage{
			Role:    "user",
			Content: fmt.Sprintf("<agent:llm_context>\n%s\n</agent:llm_context>", a.snapshot.LLMContext),
		}
		llmMessages = insertBeforeLastUserMessage(llmMessages, llmContextMsg)
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
func (a *AgentNew) processLLMResponse(
	ctx context.Context,
	stream *llm.EventStream[llm.LLMEvent, llm.LLMMessage],
	startTime time.Time,
) (*agentctx.AgentMessage, []agentctx.AgentMessage, error) {
	var partialMessage *agentctx.AgentMessage
	var textBuilder strings.Builder
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
			partialMessage = &agentctx.AgentMessage{}
			*partialMessage = agentctx.NewAssistantMessage()
			textBuilder.Reset()
			toolCalls = map[int]*llm.ToolCall{}

		case llm.LLMTextDeltaEvent:
			traceevent.Log(ctx, traceevent.CategoryLLM, "text_delta",
				traceevent.Field{Key: "content_index", Value: e.Index},
				traceevent.Field{Key: "delta", Value: e.Delta},
			)
			if partialMessage != nil {
				textBuilder.WriteString(e.Delta)
				partialMessage.Content = []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: textBuilder.String()},
				}
			}

		case llm.LLMThinkingDeltaEvent:
			traceevent.Log(ctx, traceevent.CategoryLLM, "thinking_delta",
				traceevent.Field{Key: "content_index", Value: e.Index},
				traceevent.Field{Key: "delta", Value: e.Delta},
			)

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

				// Update message content with tool calls
				var contentBlocks []agentctx.ContentBlock
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

				// Execute tool calls if present
				toolResults := a.executeToolsFromMessage(ctx, partialMessage)
				return partialMessage, toolResults, nil
			}

			if e.Message != nil {
				msg := convertLLMMessageToAgent(*e.Message)
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

	results := make([]agentctx.AgentMessage, 0, len(toolCalls))

	for _, tc := range toolCalls {
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
			// Return error result
			errorContent := []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: fmt.Sprintf("Tool '%s' not found", toolName),
				},
			}
			result := agentctx.NewToolResultMessage(toolCallID, toolName, errorContent, true)
			results = append(results, result)
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: toolName},
				traceevent.Field{Key: "tool_call_id", Value: toolCallID},
				traceevent.Field{Key: "duration_ms", Value: time.Since(toolStart).Milliseconds()},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: "tool not found"},
			)
			continue
		}

		// Execute the tool
		content, err := tool.Execute(ctx, arguments)
		if err != nil {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", err.Error())
			toolSpan.End()
			slog.Error("[AgentNew] Tool execution failed",
				"tool", toolName,
				"error", err,
			)
			// Return error result
			errorContent := []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: fmt.Sprintf("Tool execution failed: %v", err),
				},
			}
			result := agentctx.NewToolResultMessage(toolCallID, toolName, errorContent, true)
			results = append(results, result)
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: toolName},
				traceevent.Field{Key: "tool_call_id", Value: toolCallID},
				traceevent.Field{Key: "duration_ms", Value: time.Since(toolStart).Milliseconds()},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: err.Error()},
			)
			continue
		}

		// Return success result
		result := agentctx.NewToolResultMessage(toolCallID, toolName, content, false)
		results = append(results, result)
		toolSpan.AddField("duration_ms", time.Since(toolStart).Milliseconds())
		toolSpan.End()
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

	return results
}

// ContextMgmtTriggerError is returned when context management should be triggered.
type ContextMgmtTriggerError struct {
	Urgency string
	Reason  string
}

func (e *ContextMgmtTriggerError) Error() string {
	return fmt.Sprintf("context management triggered: urgency=%s, reason=%s", e.Urgency, e.Reason)
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
