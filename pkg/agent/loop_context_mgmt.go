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
	"github.com/tiancaiamao/ai/pkg/tools/context_mgmt"
	"github.com/tiancaiamao/ai/pkg/traceevent"
	"log/slog"
)

// executeContextMgmtTools executes the context management tools (LLM call + tool execution).
// This is called by executeContextMgmtStep (span and trigger tracking are handled by caller).
//
// The caller must hold the snapshot lock.
func (a *AgentNew) executeContextMgmtTools(ctx context.Context, urgency string) error {
	slog.Info("[AgentNew] Entering context management mode",
		"urgency", urgency,
		"turn", a.snapshot.AgentState.TotalTurns,
	)

	// 1. Build context management messages (multi-message structure)
	messages := a.buildContextMgmtMessages()

	// 2. Get context management tools
	ctxMgmtTools := context_mgmt.GetContextMgmtTools(a.sessionDir, a.snapshot, a.journal)

	// 3. Build LLM request for context management
	systemPrompt := prompt.BuildSystemPrompt(agentctx.ModeContextMgmt)

	llmCtx := llm.LLMContext{
		SystemPrompt: systemPrompt,
		Messages:     messages,
		Tools:        ConvertToolsToLLM(ctx, ctxMgmtTools),
	}

	// 4. Call LLM
	llmSpan := traceevent.StartSpan(ctx, "llm_call", traceevent.CategoryLLM,
		traceevent.Field{Key: "mode", Value: "context_management"},
		traceevent.Field{Key: "model", Value: a.model.ID},
		traceevent.Field{Key: "provider", Value: a.model.Provider},
		traceevent.Field{Key: "api", Value: a.model.API},
		traceevent.Field{Key: "timeout_ms", Value: int64((2 * time.Minute) / time.Millisecond)},
	)
	// Resolve LLM caller: use injected caller for testing, or default to llm.StreamLLM
	callFn := a.callLLM
	if callFn == nil {
		callFn = llm.StreamLLM
	}
	stream := callFn(
		ctx,
		*a.model,
		llmCtx,
		a.apiKey,
		2*time.Minute,
	)

	// 5. Process tool calls
	toolCalls, err := a.extractToolCallsFromStream(ctx, stream)
	if err != nil {
		llmSpan.AddField("error", true)
		llmSpan.AddField("error_message", err.Error())
		llmSpan.End()
		traceevent.Log(ctx, traceevent.CategoryEvent, "context_management_decision",
			traceevent.Field{Key: "action", Value: "retry"},
			traceevent.Field{Key: "reason", Value: err.Error()},
		)
		slog.Warn("[AgentNew] Context management LLM call failed", "error", err)
		return fmt.Errorf("context management LLM call failed: %w", err)
	}
	llmSpan.AddField("tool_calls", len(toolCalls))
	llmSpan.End()

	// 6. Execute tool calls
	// Track whether checkpoint is needed (only for update_llm_context)
	// Note: compact_messages creates its own checkpoint in performCompaction
	needsCheckpoint := false
	for _, toolCall := range toolCalls {
		slog.Info("[AgentNew] Context management tool call",
			"tool", toolCall.Function.Name,
			"args", toolCall.Function.Arguments,
		)

		if toolCall.Function.Name == "no_action" {
			traceevent.Log(ctx, traceevent.CategoryEvent, "context_management_decision",
				traceevent.Field{Key: "action", Value: "no_action"},
				traceevent.Field{Key: "reason", Value: "llm_selected_no_action"},
			)
			// No action needed
			continue
		}

		traceevent.Log(ctx, traceevent.CategoryEvent, "context_management_decision",
			traceevent.Field{Key: "action", Value: toolCall.Function.Name},
			traceevent.Field{Key: "reason", Value: "tool_selected"},
		)
		// Execute the tool
		if err := a.executeContextMgmtTool(ctx, toolCall, ctxMgmtTools); err != nil {
			slog.Error("[AgentNew] Context management tool execution failed",
				"tool", toolCall.Function.Name,
				"error", err,
			)
			return fmt.Errorf("context management tool execution failed: %w", err)
		}

		// Check if this tool requires checkpoint creation
		// Only update_llm_context needs checkpoint here
		// compact_messages creates its own checkpoint in performCompaction
		// truncate_messages only records event to journal
		if toolCall.Function.Name == "update_llm_context" {
			needsCheckpoint = true
		}
	}

	// 7. Create checkpoint if needed (only for update_llm_context)
	if needsCheckpoint {
		slog.Info("[AgentNew] Creating checkpoint after update_llm_context")
		if err := a.createCheckpoint(ctx); err != nil {
			return fmt.Errorf("failed to create checkpoint: %w", err)
		}
	}

	// 8. Reset trigger counters after context management completes
	// This ensures that the next trigger check starts fresh
	a.snapshot.AgentState.LastTriggerTurn = a.snapshot.AgentState.TotalTurns
	a.snapshot.AgentState.TurnsSinceLastTrigger = 0
	a.snapshot.AgentState.ToolCallsSinceLastTrigger = 0
	a.snapshot.AgentState.UpdatedAt = time.Now()

	slog.Info("[AgentNew] Context management mode completed",
		"needs_checkpoint", needsCheckpoint,
		"tool_calls_reset", a.snapshot.AgentState.ToolCallsSinceLastTrigger,
	)

	return nil
}

// buildContextMgmtMessages builds the message sequence for context management mode.
// This uses a multi-message structure instead of putting everything in one user message.
func (a *AgentNew) buildContextMgmtMessages() []llm.LLMMessage {
	messages := []llm.LLMMessage{}

	// 1. Current state and request
	tokenPercent := a.snapshot.EstimateTokenPercent()
	staleCount := a.snapshot.CountStaleOutputs(10)

	stateMsg := fmt.Sprintf(`<current_state>
Recent messages: %d
Tokens used: %.1f%%
Stale outputs: %d
Tool calls since last management: %d
Total turns: %d
</current_state>

Please review the context and decide what action to take.
- **update_llm_context** - Rewrite the LLM Context to reflect current state
- **truncate_messages** - Remove old tool outputs to save space (specify message_ids)
- **compact_messages** - Summarize old messages and keep recent ones (aggressive space saving)
- **no_action** - Context is healthy, no action needed`,
		len(a.snapshot.RecentMessages),
		tokenPercent*100,
		staleCount,
		a.snapshot.AgentState.ToolCallsSinceLastTrigger,
		a.snapshot.AgentState.TotalTurns,
	)

	messages = append(messages, llm.LLMMessage{
		Role:    "user",
		Content: stateMsg,
	})

	// 2. Current LLMContext (if exists)
	if a.snapshot.LLMContext != "" {
		messages = append(messages, llm.LLMMessage{
			Role:    "user",
			Content: "## Current LLM Context\n" + a.snapshot.LLMContext,
		})
	}

	// 3. Stale tool outputs (candidates for truncation)
	staleOutputs := a.getStaleToolOutputs()
	if len(staleOutputs) > 0 {
		var staleBuilder strings.Builder
		staleBuilder.WriteString("## Stale Tool Outputs (candidates for truncation)\n")
		for _, output := range staleOutputs {
			staleBuilder.WriteString(a.renderToolResultForMgmt(output))
			staleBuilder.WriteString("\n")
		}
		messages = append(messages, llm.LLMMessage{
			Role:    "user",
			Content: staleBuilder.String(),
		})
	}

	// 4. Recent messages (last N, maintaining original structure)
	// Only include messages that are relevant for context understanding
	recent := a.getLastNMessages(agentctx.RecentMessagesShowInMgmt)
	for _, msg := range recent {
		// Skip truncated messages
		if msg.IsTruncated() {
			continue
		}
		// Convert to LLM message format
		llmMsg := a.convertMessageToLLMFormat(msg)
		if llmMsg != nil {
			messages = append(messages, *llmMsg)
		}
	}

	return messages
}

// convertMessageToLLMFormat converts an AgentMessage to LLM message format.
// For context management mode, we include all messages regardless of agent_visible flag.
func (a *AgentNew) convertMessageToLLMFormat(msg agentctx.AgentMessage) *llm.LLMMessage {
	// Extract text content
	content := msg.ExtractText()

	// Handle tool calls
	var toolCalls []llm.ToolCall
	if msg.Role == "assistant" {
		calls := msg.ExtractToolCalls()
		for _, tc := range calls {
			// Convert arguments to JSON string
			argsJSON, _ := json.Marshal(tc.Arguments)
			toolCalls = append(toolCalls, llm.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: llm.FunctionCall{
					Name:      tc.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	// Build LLM message based on role
	switch msg.Role {
	case "user":
		return &llm.LLMMessage{
			Role:    "user",
			Content: content,
		}
	case "assistant":
		return &llm.LLMMessage{
			Role:      "assistant",
			Content:   content,
			ToolCalls: toolCalls,
		}
	case "toolResult":
		// Tool results are represented as tool messages with tool call ID
		// This follows OpenAI's API format
		return &llm.LLMMessage{
			Role:        "tool",
			Content:     content,
			ToolCallID:  msg.ToolCallID,
		}
	}

	return nil
}

// buildContextMgmtInput builds the context management input string (legacy, deprecated).
// This function is kept for reference but should not be used.
// Use buildContextMgmtMessages instead for better structure.
// Deprecated: Use buildContextMgmtMessages instead.
func (a *AgentNew) buildContextMgmtInput() string {
	var input strings.Builder

	// 1. Current state
	tokenPercent := a.snapshot.EstimateTokenPercent()
	staleCount := a.snapshot.CountStaleOutputs(10)

	input.WriteString("<current_state>\n")
	input.WriteString(fmt.Sprintf("Recent messages: %d\n", len(a.snapshot.RecentMessages)))
	input.WriteString(fmt.Sprintf("Tokens used: %.1f%%\n", tokenPercent*100))
	input.WriteString(fmt.Sprintf("Stale outputs: %d\n", staleCount))
	input.WriteString(fmt.Sprintf("Tool calls since last management: %d\n",
		a.snapshot.AgentState.ToolCallsSinceLastTrigger))
	input.WriteString(fmt.Sprintf("Total turns: %d\n", a.snapshot.AgentState.TotalTurns))
	input.WriteString("</current_state>\n\n")

	// 2. Current LLMContext
	if a.snapshot.LLMContext != "" {
		input.WriteString("## Current LLM Context\n")
		input.WriteString(a.snapshot.LLMContext)
		input.WriteString("\n\n")
	}

	// 3. Stale tool outputs (all visible tool results, ordered by stale)
	input.WriteString("## Stale Tool Outputs (candidates for truncation)\n")
	staleOutputs := a.getStaleToolOutputs()
	for _, output := range staleOutputs {
		input.WriteString(a.renderToolResultForMgmt(output))
		input.WriteString("\n")
	}
	input.WriteString("\n")

	// 4. Recent messages (last N)
	input.WriteString(fmt.Sprintf("## Recent Messages (last %d)\n", agentctx.RecentMessagesShowInMgmt))
	recent := a.getLastNMessages(agentctx.RecentMessagesShowInMgmt)
	for _, msg := range recent {
		if !msg.IsAgentVisible() || msg.IsTruncated() {
			continue
		}
		input.WriteString(msg.RenderContent())
		input.WriteString("\n")
	}

	return input.String()
}

// getStaleToolOutputs returns tool results ordered by staleness.
func (a *AgentNew) getStaleToolOutputs() []agentctx.AgentMessage {
	var results []agentctx.AgentMessage
	for _, msg := range a.snapshot.RecentMessages {
		// Include all tool results regardless of agent_visible flag
		// Context management needs to see ALL tool outputs to make compression decisions
		if msg.Role == "toolResult" && !msg.IsTruncated() {
			results = append(results, msg)
		}
	}
	// Already ordered from oldest (highest stale) to newest
	return results
}

// getLastNMessages returns the last N messages.
func (a *AgentNew) getLastNMessages(n int) []agentctx.AgentMessage {
	if len(a.snapshot.RecentMessages) <= n {
		return a.snapshot.RecentMessages
	}
	return a.snapshot.RecentMessages[len(a.snapshot.RecentMessages)-n:]
}

// renderToolResultForMgmt renders a tool result for context management mode.
func (a *AgentNew) renderToolResultForMgmt(msg agentctx.AgentMessage) string {
	content := msg.ExtractText()

	// Calculate stale
	toolResults := a.getStaleToolOutputs()
	stale := 0
	for i, result := range toolResults {
		if result.ToolCallID == msg.ToolCallID {
			stale = len(toolResults) - i - 1
			break
		}
	}

	// Handle large output preview
	const (
		ToolOutputMaxChars    = 2000
		ToolOutputPreviewHead = 1800
		ToolOutputPreviewTail = 200
	)

	if len(content) > ToolOutputMaxChars {
		head := content[:ToolOutputPreviewHead]
		tail := content[len(content)-ToolOutputPreviewTail:]
		truncatedChars := len(content) - ToolOutputPreviewHead - ToolOutputPreviewTail
		content = fmt.Sprintf("%s\n... (%d chars truncated) ...\n%s",
			head, truncatedChars, tail)
	}

	return fmt.Sprintf(
		`<agent:tool id="%s" name="%s" stale="%d" chars="%d">%s</agent:tool>`,
		msg.ToolCallID, msg.ToolName, stale, len(msg.ExtractText()), content,
	)
}

// extractToolCallsFromStream extracts tool calls from the LLM stream.
func (a *AgentNew) extractToolCallsFromStream(ctx context.Context, stream *llm.EventStream[llm.LLMEvent, llm.LLMMessage]) ([]llm.ToolCall, error) {
	toolCallsByIndex := make(map[int]*llm.ToolCall)
	order := make([]int, 0)

	for event := range stream.Iterator(ctx) {
		if event.Done {
			break
		}

		switch e := event.Value.(type) {
		case llm.LLMTextDeltaEvent:
			traceevent.Log(ctx, traceevent.CategoryLLM, "text_delta",
				traceevent.Field{Key: "content_index", Value: e.Index},
				traceevent.Field{Key: "delta", Value: e.Delta},
			)

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

			call, ok := toolCallsByIndex[e.Index]
			if !ok {
				call = &llm.ToolCall{}
				toolCallsByIndex[e.Index] = call
				order = append(order, e.Index)
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

		case llm.LLMDoneEvent:
			if len(order) == 0 && e.Message != nil && len(e.Message.ToolCalls) > 0 {
				return e.Message.ToolCalls, nil
			}
			toolCalls := make([]llm.ToolCall, 0, len(order))
			for _, idx := range order {
				call := toolCallsByIndex[idx]
				if call == nil {
					continue
				}
				if call.Function.Name == "" && call.ID == "" {
					continue
				}
				toolCalls = append(toolCalls, *call)
			}
			return toolCalls, nil

		case llm.LLMErrorEvent:
			return nil, e.Error
		}
	}

	toolCalls := make([]llm.ToolCall, 0, len(order))
	for _, idx := range order {
		call := toolCallsByIndex[idx]
		if call == nil {
			continue
		}
		if call.Function.Name == "" && call.ID == "" {
			continue
		}
		toolCalls = append(toolCalls, *call)
	}
	return toolCalls, nil
}

// retryContextMgmt retries context management on failure.
func (a *AgentNew) retryContextMgmt(ctx context.Context, urgency string, originalErr error) error {
	// Retry once with exponential backoff
	// If still fails, return error and resume normal mode
	slog.Info("[AgentNew] Retrying context management after failure")

	select {
	case <-time.After(2 * time.Second):
		// Retry
		return fmt.Errorf("context management failed: %w (retry attempted)", originalErr)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// executeNoAction handles the no_action case.
func (a *AgentNew) executeNoAction(ctx context.Context, toolCall llm.ToolCall) error {
	// Update snapshot state
	a.snapshot.AgentState.LastTriggerTurn = a.snapshot.AgentState.TotalTurns
	a.snapshot.AgentState.TurnsSinceLastTrigger = 0
	a.snapshot.AgentState.ToolCallsSinceLastTrigger = 0

	slog.Info("[AgentNew] Context management: no action taken",
		"turn", a.snapshot.AgentState.TotalTurns,
	)
	traceevent.Log(ctx, traceevent.CategoryEvent, "context_management_skipped",
		traceevent.Field{Key: "reason", Value: "no_action"},
		traceevent.Field{Key: "tool_call_id", Value: toolCall.ID},
	)

	return nil
}

// executeContextMgmtTool executes a context management tool.
func (a *AgentNew) executeContextMgmtTool(ctx context.Context, toolCall llm.ToolCall, availableTools []agentctx.Tool) error {
	toolSpan := traceevent.StartSpan(ctx, "tool_execution", traceevent.CategoryTool,
		traceevent.Field{Key: "mode", Value: "context_management"},
		traceevent.Field{Key: "tool", Value: toolCall.Function.Name},
		traceevent.Field{Key: "tool_call_id", Value: toolCall.ID},
	)
	defer toolSpan.End()

	traceevent.Log(ctx, traceevent.CategoryTool, "tool_start",
		traceevent.Field{Key: "tool", Value: toolCall.Function.Name},
		traceevent.Field{Key: "tool_call_id", Value: toolCall.ID},
		traceevent.Field{Key: "args", Value: toolCall.Function.Arguments},
	)

	// Find the tool
	var targetTool agentctx.Tool
	for _, tool := range availableTools {
		if tool.Name() == toolCall.Function.Name {
			targetTool = tool
			break
		}
	}

	if targetTool == nil {
		toolSpan.AddField("error", true)
		toolSpan.AddField("error_message", "tool not found")
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
			traceevent.Field{Key: "tool", Value: toolCall.Function.Name},
			traceevent.Field{Key: "tool_call_id", Value: toolCall.ID},
			traceevent.Field{Key: "error", Value: true},
			traceevent.Field{Key: "error_message", Value: "tool not found"},
		)
		return fmt.Errorf("tool not found: %s", toolCall.Function.Name)
	}

	// Parse arguments
	args := make(map[string]any)
	if strings.TrimSpace(toolCall.Function.Arguments) != "" {
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", err.Error())
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_call_invalid_args",
				traceevent.Field{Key: "tool", Value: toolCall.Function.Name},
				traceevent.Field{Key: "tool_call_id", Value: toolCall.ID},
				traceevent.Field{Key: "args", Value: toolCall.Function.Arguments},
				traceevent.Field{Key: "error", Value: err.Error()},
			)
			traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
				traceevent.Field{Key: "tool", Value: toolCall.Function.Name},
				traceevent.Field{Key: "tool_call_id", Value: toolCall.ID},
				traceevent.Field{Key: "error", Value: true},
				traceevent.Field{Key: "error_message", Value: err.Error()},
			)
			return fmt.Errorf("failed to parse arguments for %s: %w", toolCall.Function.Name, err)
		}
	}

	// Execute the tool
	start := time.Now()
	content, err := targetTool.Execute(ctx, args)
	if err != nil {
		toolSpan.AddField("error", true)
		toolSpan.AddField("error_message", err.Error())
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
			traceevent.Field{Key: "tool", Value: toolCall.Function.Name},
			traceevent.Field{Key: "tool_call_id", Value: toolCall.ID},
			traceevent.Field{Key: "duration_ms", Value: time.Since(start).Milliseconds()},
			traceevent.Field{Key: "error", Value: true},
			traceevent.Field{Key: "error_message", Value: err.Error()},
		)
		return err
	}

	// Special handling for compact_messages tool
	// The tool just indicates intent, we need to perform the actual compaction
	if toolCall.Function.Name == "compact_messages" {
		slog.Info("[AgentNew] Performing actual compaction",
			"turn", a.snapshot.AgentState.TotalTurns,
			"message_count", len(a.snapshot.RecentMessages),
		)

		if err := a.performCompaction(ctx); err != nil {
			toolSpan.AddField("error", true)
			toolSpan.AddField("error_message", err.Error())
			return fmt.Errorf("compaction failed: %w", err)
		}
	}

	// Log result
	slog.Info("[AgentNew] Context management tool executed",
		"tool", toolCall.Function.Name,
		"result", content,
	)
	traceevent.Log(ctx, traceevent.CategoryTool, "tool_end",
		traceevent.Field{Key: "tool", Value: toolCall.Function.Name},
		traceevent.Field{Key: "tool_call_id", Value: toolCall.ID},
		traceevent.Field{Key: "duration_ms", Value: time.Since(start).Milliseconds()},
		traceevent.Field{Key: "error", Value: false},
	)

	return nil
}
