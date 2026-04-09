package agent

import (
	"context"
	"encoding/json"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"strings"

	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// ConvertMessagesToLLM converts agent messages to LLM messages.
func ConvertMessagesToLLM(ctx context.Context, messages []agentctx.AgentMessage) []llm.LLMMessage {
	span := traceevent.StartSpan(ctx, "ConvertMessagesToLLM", traceevent.CategoryEvent)
	defer span.End()

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

		// Extract content
		for _, block := range msg.Content {
			switch b := block.(type) {
			case agentctx.TextContent:
				llmMsg.Content = b.Text
			case agentctx.ImageContent:
				// For multimodal, use ContentParts
				llmMsg.ContentParts = append(llmMsg.ContentParts, llm.ContentPart{
					Type: "image_url",
					ImageURL: &struct {
						URL string `json:"url"`
					}{
						URL: b.Data,
					},
				})
			}
		}

		// Extract tool calls (from assistant messages)
		if msg.Role == "assistant" {
			toolCalls := msg.ExtractToolCalls()
			if len(toolCalls) > 0 {
				llmMsg.ToolCalls = make([]llm.ToolCall, len(toolCalls))
				for i, tc := range toolCalls {
					// Convert arguments map to JSON string
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

		// agentctx.Tool result message
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

// ConvertLLMMessageToAgent converts an LLM message to an agent message.
func ConvertLLMMessageToAgent(llmMsg llm.LLMMessage) agentctx.AgentMessage {
	agentMsg := agentctx.NewAssistantMessage()
	agentMsg.Content = []agentctx.ContentBlock{}

	// Add thinking content first (for reasoning models)
	if llmMsg.Thinking != "" {
		agentMsg.Content = append(agentMsg.Content, agentctx.ThinkingContent{
			Type:     "thinking",
			Thinking: llmMsg.Thinking,
		})
	}

	// Add text content
	if llmMsg.Content != "" {
		agentMsg.Content = append(agentMsg.Content, agentctx.TextContent{
			Type: "text",
			Text: llmMsg.Content,
		})
	}

	// Add tool calls
	if len(llmMsg.ToolCalls) > 0 {
		for _, tc := range llmMsg.ToolCalls {
			// Parse arguments JSON string
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = make(map[string]any)
			}

			agentMsg.Content = append(agentMsg.Content, agentctx.ToolCallContent{
				ID:        tc.ID,
				Type:      "toolCall",
				Name:      tc.Function.Name,
				Arguments: args,
			})
		}
	}

	return agentMsg
}

// ConvertToolsToLLM converts agent tools to LLM tools.
func ConvertToolsToLLM(ctx context.Context, tools []agentctx.Tool) []llm.LLMTool {
	span := traceevent.StartSpan(ctx, "ConvertToolsToLLM", traceevent.CategoryEvent)
	defer span.End()

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
