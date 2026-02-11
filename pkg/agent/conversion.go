package agent

import (
	"encoding/json"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// ConvertMessagesToLLM converts agent messages to LLM messages.
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

		// Extract content
		for _, block := range msg.Content {
			switch b := block.(type) {
			case TextContent:
				llmMsg.Content = b.Text
			case ImageContent:
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

		// Tool result message
		if msg.Role == "toolResult" {
			llmMsg.ToolCallID = msg.ToolCallID
			// Extract text content
			llmMsg.Content = msg.ExtractText()
		}

		llmMessages = append(llmMessages, llmMsg)
	}

	return llmMessages
}

// ConvertLLMMessageToAgent converts an LLM message to an agent message.
func ConvertLLMMessageToAgent(llmMsg llm.LLMMessage) AgentMessage {
	agentMsg := NewAssistantMessage()
	agentMsg.Content = []ContentBlock{}

	// Add text content
	if llmMsg.Content != "" {
		agentMsg.Content = append(agentMsg.Content, TextContent{
			Type: "text",
			Text: llmMsg.Content,
		})
	}

	// Add tool calls
	if len(llmMsg.ToolCalls) > 0 {
		for _, tc := range llmMsg.ToolCalls {
			// Parse arguments JSON string
			var args map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &args)

			agentMsg.Content = append(agentMsg.Content, ToolCallContent{
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
func ConvertToolsToLLM(tools []Tool) []llm.LLMTool {
	llmTools := make([]llm.LLMTool, len(tools))

	for i, tool := range tools {
		llmTools[i] = llm.LLMTool{
			Type: "function",
			Function: llm.ToolFunction{
				Name:        tool.Name(),
				Description: tool.Description(),
				Parameters:  tool.Parameters(),
			},
		}
	}

	return llmTools
}
