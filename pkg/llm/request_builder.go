package llm

import (
	"fmt"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/prompt"
)

// BuildRequest builds an LLM request from a ContextSnapshot.
func BuildRequest(snapshot *agentctx.ContextSnapshot, mode agentctx.AgentMode, tools []agentctx.Tool, model string) (*LLMRequest, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("snapshot cannot be nil")
	}

	request := &LLMRequest{
		Model: model,
	}

	// 1. System prompt (stable for caching)
	request.SystemPrompt = prompt.BuildSystemPrompt(mode)

	// 2. RecentMessages (only non-truncated, agent-visible)
	toolResults := snapshot.GetVisibleToolResults()
	request.Messages = make([]LLMMessage, 0, len(snapshot.RecentMessages))

	for i := range snapshot.RecentMessages {
		msg := &snapshot.RecentMessages[i]
		if !msg.IsAgentVisible() {
			continue
		}
		if msg.IsTruncated() {
			continue
		}

		// Mode-specific rendering for tool results
		var content string
		if mode == agentctx.ModeContextMgmt && msg.Role == "toolResult" {
			// Calculate stale for this tool result
			stale := calculateStaleForMessage(msg, toolResults)
			content = agentctx.RenderToolResult(&snapshot.RecentMessages[i], mode, stale)
		} else {
			content = msg.RenderContent()
		}

		request.Messages = append(request.Messages, LLMMessage{
			Role:    msg.Role,
			Content: content,
		})
	}

	// 3. Inject <agent:xxx> messages BEFORE last user message
	lastUserIndex := findLastUserMessageIndex(request.Messages)

	// Inject llm_context (if exists and in normal mode)
	if mode == agentctx.ModeNormal && snapshot.LLMContext != "" {
		llmContextMsg := LLMMessage{
			Role:    "user",
			Content: fmt.Sprintf("<agent:llm_context>\n%s\n</agent:llm_context>", snapshot.LLMContext),
		}
		request.Messages = insertBefore(request.Messages, lastUserIndex, llmContextMsg)
	}

	// Inject runtime_state (in both modes for visibility)
	runtimeStateMsg := LLMMessage{
		Role:    "user",
		Content: buildRuntimeStateXML(snapshot, mode),
	}
	request.Messages = insertBefore(request.Messages, lastUserIndex, runtimeStateMsg)

	// 4. Tools (mode-specific)
	request.Tools = ConvertToolsToLLM(tools)

	return request, nil
}

// calculateStaleForMessage calculates the stale score for a specific message.
func calculateStaleForMessage(msg *agentctx.AgentMessage, allToolResults []agentctx.AgentMessage) int {
	for i := range allToolResults {
		if allToolResults[i].ToolCallID == msg.ToolCallID {
			return agentctx.CalculateStale(i, len(allToolResults))
		}
	}
	return 0
}

// findLastUserMessageIndex finds the index of the last user message.
func findLastUserMessageIndex(messages []LLMMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return i
		}
	}
	return len(messages) // No user message found, append to end
}

// insertBefore inserts a message at the specified index.
func insertBefore(messages []LLMMessage, index int, newMsg LLMMessage) []LLMMessage {
	if index > len(messages) {
		index = len(messages)
	}
	if index < 0 {
		index = 0
	}
	result := make([]LLMMessage, 0, len(messages)+1)
	result = append(result, messages[:index]...)
	result = append(result, newMsg)
	result = append(result, messages[index:]...)
	return result
}

// buildRuntimeStateXML builds the runtime state XML.
func buildRuntimeStateXML(snapshot *agentctx.ContextSnapshot, mode agentctx.AgentMode) string {
	tokenPercent := snapshot.EstimateTokenPercent()
	staleCount := snapshot.CountStaleOutputs(10)

	content := fmt.Sprintf(`<agent:runtime_state>
tokens_used: %d
tokens_limit: %d
tokens_percent: %.1f
recent_messages: %d
stale_outputs: %d
turn: %d
urgency: %s
</agent:runtime_state>`,
		snapshot.EstimateTokens(),
		snapshot.AgentState.TokensLimit,
		tokenPercent*100,
		len(snapshot.RecentMessages),
		staleCount,
		snapshot.AgentState.TotalTurns,
		determineUrgency(snapshot, mode),
	)

	return content
}

// determineUrgency determines the urgency level.
func determineUrgency(snapshot *agentctx.ContextSnapshot, mode agentctx.AgentMode) string {
	if mode == agentctx.ModeContextMgmt {
		tokenPercent := snapshot.EstimateTokenPercent()
		if tokenPercent >= 0.75 {
			return "urgent"
		} else if tokenPercent >= 0.40 {
			return "high"
		} else if tokenPercent >= 0.25 {
			return "medium"
		}
	}
	return "none"
}

// ConvertToolsToLLM converts context.Tool to LLMTool.
func ConvertToolsToLLM(tools []agentctx.Tool) []LLMTool {
	if tools == nil {
		return nil
	}

	llmTools := make([]LLMTool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		llmTools = append(llmTools, LLMTool{
			Type: "function",
			Function: ToolFunction{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}
	return llmTools
}

// LLMRequest represents an LLM API request.
type LLMRequest struct {
	Model        string
	SystemPrompt string
	Messages     []LLMMessage
	Tools        []LLMTool
}
