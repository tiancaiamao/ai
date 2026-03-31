package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/tools/context_mgmt"
	"log/slog"
)

// ExecuteContextMgmtMode executes the context management flow.
func (a *AgentNew) ExecuteContextMgmtMode(ctx context.Context, urgency string) error {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	slog.Info("[AgentNew] Entering context management mode",
		"urgency", urgency,
		"turn", a.snapshot.AgentState.TotalTurns,
	)

	// 1. Build context management input
	input := a.buildContextMgmtInput()

	// 2. Get context management tools
	ctxMgmtTools := context_mgmt.GetContextMgmtTools(a.sessionDir, a.snapshot, a.journal)

	// 3. Build LLM request for context management
	systemPrompt := prompt.BuildSystemPrompt(agentctx.ModeContextMgmt)

	llmCtx := llm.LLMContext{
		SystemPrompt: systemPrompt,
		Messages: []llm.LLMMessage{
			{Role: "user", Content: input},
		},
		Tools: ConvertToolsToLLM(ctx, ctxMgmtTools),
	}

	// 4. Call LLM
	stream := llm.StreamLLM(
		ctx,
		*a.model,
		llmCtx,
		a.apiKey,
		2*time.Minute,
	)

	// 5. Process tool calls
	toolCalls, err := a.extractToolCallsFromStream(ctx, stream)
	if err != nil {
		slog.Warn("[AgentNew] Context management LLM call failed, retrying", "error", err)
		return a.retryContextMgmt(ctx, urgency, err)
	}

	// 6. Execute tool calls
	actionTaken := false
	for _, toolCall := range toolCalls {
		slog.Info("[AgentNew] Context management tool call",
			"tool", toolCall.Function.Name,
			"args", toolCall.Function.Arguments,
		)

		if toolCall.Function.Name == "no_action" {
			// Update LastTriggerTurn but don't create checkpoint
			if err := a.executeNoAction(ctx, toolCall); err != nil {
				slog.Warn("[AgentNew] no_action execution failed", "error", err)
			}
		} else {
			// Execute the tool
			if err := a.executeContextMgmtTool(ctx, toolCall, ctxMgmtTools); err != nil {
				slog.Error("[AgentNew] Context management tool execution failed",
					"tool", toolCall.Function.Name,
					"error", err,
				)
				return err
			}
			actionTaken = true
		}
	}

	// 7. Create checkpoint if action was taken
	if actionTaken {
		slog.Info("[AgentNew] Creating checkpoint after context management")
		if err := a.createCheckpoint(ctx); err != nil {
			return fmt.Errorf("failed to create checkpoint: %w", err)
		}
	}

	// 8. Update trigger tracking
	a.snapshot.AgentState.LastTriggerTurn = a.snapshot.AgentState.TotalTurns
	a.snapshot.AgentState.TurnsSinceLastTrigger = 0
	a.snapshot.AgentState.UpdatedAt = time.Now()

	slog.Info("[AgentNew] Context management mode completed",
		"action_taken", actionTaken,
	)

	return nil
}

// buildContextMgmtInput builds the specialized input for Context Management mode.
func (a *AgentNew) buildContextMgmtInput() string {
	var input strings.Builder

	// 1. Current state
	tokenPercent := a.snapshot.EstimateTokenPercent()
	staleCount := a.snapshot.CountStaleOutputs(10)

	input.WriteString("<current_state>\n")
	input.WriteString(fmt.Sprintf("Recent messages: %d\n", len(a.snapshot.RecentMessages)))
	input.WriteString(fmt.Sprintf("Tokens used: %.1f%%\n", tokenPercent*100))
	input.WriteString(fmt.Sprintf("Stale outputs: %d\n", staleCount))
	input.WriteString(fmt.Sprintf("Turns since last management: %d\n",
		a.snapshot.AgentState.TurnsSinceLastTrigger))
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
		if msg.Role == "toolResult" && !msg.IsTruncated() && msg.IsAgentVisible() {
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
	var toolCalls []llm.ToolCall

	for event := range stream.Iterator(ctx) {
		if event.Done {
			break
		}

		switch e := event.Value.(type) {
		case llm.LLMToolCallDeltaEvent:
			// Collect tool calls
			toolCalls = append(toolCalls, llm.ToolCall{
				ID: e.ToolCall.ID,
				Function: llm.FunctionCall{
					Name:      e.ToolCall.Function.Name,
					Arguments: e.ToolCall.Function.Arguments,
				},
			})

		case llm.LLMDoneEvent:
			return toolCalls, nil

		case llm.LLMErrorEvent:
			return nil, e.Error
		}
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

	slog.Info("[AgentNew] Context management: no action taken",
		"turn", a.snapshot.AgentState.TotalTurns,
	)

	return nil
}

// executeContextMgmtTool executes a context management tool.
func (a *AgentNew) executeContextMgmtTool(ctx context.Context, toolCall llm.ToolCall, availableTools []agentctx.Tool) error {
	// Find the tool
	var targetTool agentctx.Tool
	for _, tool := range availableTools {
		if tool.Name() == toolCall.Function.Name {
			targetTool = tool
			break
		}
	}

	if targetTool == nil {
		return fmt.Errorf("tool not found: %s", toolCall.Function.Name)
	}

	// Parse arguments
	var args map[string]any
	if toolCall.Function.Arguments != "" {
		// For simplicity, assume arguments are already parsed or handle raw string
		args = map[string]any{
			"raw": toolCall.Function.Arguments,
		}
	}

	// Execute the tool
	content, err := targetTool.Execute(ctx, args)
	if err != nil {
		return err
	}

	// Log result
	slog.Info("[AgentNew] Context management tool executed",
		"tool", toolCall.Function.Name,
		"result", content,
	)

	return nil
}
