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
	"log/slog"
)

// ExecuteNormalMode executes a turn in normal mode.
func (a *AgentNew) ExecuteNormalMode(ctx context.Context, userMessage string) error {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	// 1. Check trigger conditions
	shouldTrigger, urgency, reason := a.triggerChecker.ShouldTrigger(a.snapshot)
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
		return err
	}

	// 7. Append assistant message
	a.snapshot.RecentMessages = append(a.snapshot.RecentMessages, *assistantMsg)
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
			partialMessage = &agentctx.AgentMessage{}
			*partialMessage = agentctx.NewAssistantMessage()
			textBuilder.Reset()
			toolCalls = map[int]*llm.ToolCall{}

		case llm.LLMTextDeltaEvent:
			if partialMessage != nil {
				textBuilder.WriteString(e.Delta)
				partialMessage.Content = []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: textBuilder.String()},
				}
			}

		case llm.LLMToolCallDeltaEvent:
			if partialMessage != nil {
				call, ok := toolCalls[e.Index]
				if !ok {
					call = &llm.ToolCall{}
					toolCalls[e.Index] = call
				}

				if e.ToolCall.ID != "" {
					call.ID = e.ToolCall.ID
				}
				if e.ToolCall.Function.Name != "" {
					call.Function.Name = e.ToolCall.Function.Name
				}
				if e.ToolCall.Function.Arguments != "" {
					call.Function.Arguments = e.ToolCall.Function.Arguments
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
							// Parse arguments
							// For simplicity, we'll just store the raw string
							argsMap = map[string]any{
								"raw": tc.Function.Arguments,
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

		slog.Info("[AgentNew] Executing tool",
			"tool", toolName,
			"toolCallID", toolCallID,
			"args", arguments,
		)

		// Find the tool
		tool := toolsByName[toolName]
		if tool == nil {
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
			continue
		}

		// Execute the tool
		content, err := tool.Execute(ctx, arguments)
		if err != nil {
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
			continue
		}

		// Return success result
		result := agentctx.NewToolResultMessage(toolCallID, toolName, content, false)
		results = append(results, result)

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
