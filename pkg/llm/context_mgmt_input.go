package llm

import (
	"fmt"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// BuildContextMgmtInput builds the specialized input for Context Management mode.
func BuildContextMgmtInput(snapshot *agentctx.ContextSnapshot) string {
	var input strings.Builder

	// 1. Current state
	tokenPercent := snapshot.EstimateTokenPercent()
	staleCount := snapshot.CountStaleOutputs(10)

	input.WriteString("<current_state>\n")
	input.WriteString(fmt.Sprintf("Recent messages: %d\n", len(snapshot.RecentMessages)))
	input.WriteString(fmt.Sprintf("Tokens used: %.1f%%\n", tokenPercent*100))
	input.WriteString(fmt.Sprintf("Stale outputs: %d\n", staleCount))
	input.WriteString(fmt.Sprintf("Turns since last management: %d\n",
		snapshot.AgentState.TurnsSinceLastTrigger))
	input.WriteString(fmt.Sprintf("Tool calls since last management: %d\n",
		snapshot.AgentState.ToolCallsSinceLastTrigger))
	input.WriteString(fmt.Sprintf("Urgency: %s\n", determineUrgency(snapshot, agentctx.ModeContextMgmt)))
	input.WriteString("</current_state>\n\n")

	// 2. Current LLMContext
	if snapshot.LLMContext != "" {
		input.WriteString("## Current LLM Context\n")
		input.WriteString(snapshot.LLMContext)
		input.WriteString("\n\n")
	}

	// 3. Stale tool outputs (all visible tool results, ordered by stale)
	input.WriteString("## Stale Tool Outputs (candidates for truncation)\n")
	staleOutputs := getStaleToolOutputs(snapshot)
	for i := range staleOutputs {
		msg := &staleOutputs[i]
		toolResults := snapshot.GetVisibleToolResults()
		stale := calculateStaleForMessage(msg, toolResults)
		input.WriteString(agentctx.RenderToolResult(&staleOutputs[i], agentctx.ModeContextMgmt, stale))
		input.WriteString("\n")
	}
	input.WriteString("\n")

	// 4. Recent messages (last N)
	input.WriteString(fmt.Sprintf("## Recent Messages (last %d)\n", agentctx.RecentMessagesShowInMgmt))
	recent := getLastNMessages(snapshot.RecentMessages, agentctx.RecentMessagesShowInMgmt)
	for i := range recent {
		msg := &recent[i]
		if !msg.IsAgentVisible() || msg.IsTruncated() {
			continue
		}
		input.WriteString(msg.RenderContent())
		input.WriteString("\n")
	}

	return input.String()
}

// getStaleToolOutputs returns tool results ordered by staleness.
func getStaleToolOutputs(snapshot *agentctx.ContextSnapshot) []agentctx.AgentMessage {
	toolResults := snapshot.GetVisibleToolResults()
	// Already ordered from oldest (highest stale) to newest
	return toolResults
}

// getLastNMessages returns the last N messages.
func getLastNMessages(messages []agentctx.AgentMessage, n int) []agentctx.AgentMessage {
	if len(messages) <= n {
		return messages
	}
	return messages[len(messages)-n:]
}
