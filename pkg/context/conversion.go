package context

import (
	"encoding/json"
	"strings"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// ConvertMessagesToLLM converts agent messages to LLM messages.
// This is shared between the agent loop and the compactor so that both
// produce identical token sequences, enabling prefix-cache reuse.
func ConvertMessagesToLLM(messages []AgentMessage) []llm.LLMMessage {
	llmMessages := make([]llm.LLMMessage, 0, len(messages))

	for _, msg := range messages {
		if !msg.IsAgentVisible() {
			continue
		}

		role := msg.Role
		if role == "toolResult" {
			role = "tool"
		}
		llmMsg := llm.LLMMessage{
			Role: role,
		}

		// Extract content and thinking
		for _, block := range msg.Content {
			switch b := block.(type) {
			case TextContent:
				llmMsg.Content = b.Text
			case ImageContent:
				llmMsg.ContentParts = append(llmMsg.ContentParts, llm.ContentPart{
					Type: "image_url",
					ImageURL: &struct {
						URL string `json:"url"`
					}{
						URL: b.Data,
					},
				})
			case ThinkingContent:
				llmMsg.Thinking = b.Thinking
			}
		}

		// Extract tool calls (from assistant messages)
		if msg.Role == "assistant" {
			toolCalls := msg.ExtractToolCalls()
			if len(toolCalls) > 0 {
				llmMsg.ToolCalls = make([]llm.ToolCall, len(toolCalls))
				for i, tc := range toolCalls {
					argsJSON, _ := json.Marshal(tc.Arguments)
					llmMsg.ToolCalls[i] = llm.ToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: llm.FunctionCall{
							Name:      tc.Name,
							Arguments: string(argsJSON),
						},
					}
				}
			}
		}

		// Tool result message
		if msg.Role == "toolResult" {
			llmMsg.ToolCallID = msg.ToolCallID
			llmMsg.Content = msg.ExtractText()
		}

		llmMessages = append(llmMessages, llmMsg)
	}

	return sanitizeToolCallProtocol(llmMessages)
}

// sanitizeToolCallProtocol ensures generated messages follow strict assistant/tool pairing rules:
// - a tool message must follow a pending assistant tool call
// - unresolved tool calls are stripped before appending non-tool messages
func sanitizeToolCallProtocol(messages []llm.LLMMessage) []llm.LLMMessage {
	if len(messages) == 0 {
		return messages
	}

	sanitized := make([]llm.LLMMessage, 0, len(messages))
	pendingToolCalls := map[string]struct{}{}

	clearPending := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		sanitized = stripPendingToolCallsFromLastAssistant(sanitized, pendingToolCalls)
		pendingToolCalls = map[string]struct{}{}
	}

	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			clearPending()
			sanitized = append(sanitized, msg)
			for _, toolCall := range msg.ToolCalls {
				if strings.TrimSpace(toolCall.ID) == "" {
					continue
				}
				pendingToolCalls[toolCall.ID] = struct{}{}
			}
		case "tool":
			toolCallID := strings.TrimSpace(msg.ToolCallID)
			if toolCallID == "" || len(pendingToolCalls) == 0 {
				continue
			}
			if _, ok := pendingToolCalls[toolCallID]; !ok {
				continue
			}
			sanitized = append(sanitized, msg)
			delete(pendingToolCalls, toolCallID)
		default:
			clearPending()
			sanitized = append(sanitized, msg)
		}
	}

	clearPending()
	return sanitized
}

func stripPendingToolCallsFromLastAssistant(messages []llm.LLMMessage, pending map[string]struct{}) []llm.LLMMessage {
	if len(messages) == 0 || len(pending) == 0 {
		return messages
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "assistant" {
			continue
		}
		if len(messages[i].ToolCalls) == 0 {
			return messages
		}

		filteredToolCalls := make([]llm.ToolCall, 0, len(messages[i].ToolCalls))
		for _, toolCall := range messages[i].ToolCalls {
			if _, drop := pending[toolCall.ID]; drop {
				continue
			}
			filteredToolCalls = append(filteredToolCalls, toolCall)
		}
		messages[i].ToolCalls = filteredToolCalls

		if len(messages[i].ToolCalls) == 0 &&
			strings.TrimSpace(messages[i].Content) == "" &&
			len(messages[i].ContentParts) == 0 {
			return append(messages[:i], messages[i+1:]...)
		}
		return messages
	}

	return messages
}

// ConvertToolsToLLM converts agent tools to LLM tools.
func ConvertToolsToLLM(tools []Tool) []llm.LLMTool {
	llmTools := make([]llm.LLMTool, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))

	for _, tool := range tools {
		if tool == nil {
			continue
		}
		name := tool.Name()
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		llmTools = append(llmTools, llm.LLMTool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        name,
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		})
	}

	return llmTools
}
