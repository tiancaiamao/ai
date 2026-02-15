package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// ConvertMessagesToLLM converts agent messages to LLM messages.
func ConvertMessagesToLLM(ctx context.Context, messages []AgentMessage) []llm.LLMMessage {
	span := traceevent.StartSpan(ctx, "ConvertMessagesToLLM", traceevent.CategoryEvent)
	defer span.End()

	messages = dedupeMessagesForLLM(messages)
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

func dedupeMessagesForLLM(messages []AgentMessage) []AgentMessage {
	if len(messages) <= 1 {
		return messages
	}

	seenToolResults := make(map[string]struct{}, len(messages))
	seenSummaries := make(map[string]struct{}, len(messages))
	keptReverse := make([]AgentMessage, 0, len(messages))

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !msg.IsAgentVisible() {
			continue
		}

		if key, ok := toolResultDedupKey(msg); ok {
			if _, seen := seenToolResults[key]; seen {
				continue
			}
			seenToolResults[key] = struct{}{}
		}

		if key, ok := toolSummaryDedupKey(msg); ok {
			if _, seen := seenSummaries[key]; seen {
				continue
			}
			seenSummaries[key] = struct{}{}
		}

		keptReverse = append(keptReverse, msg)
	}

	for i, j := 0, len(keptReverse)-1; i < j; i, j = i+1, j-1 {
		keptReverse[i], keptReverse[j] = keptReverse[j], keptReverse[i]
	}

	return keptReverse
}

func toolResultDedupKey(msg AgentMessage) (string, bool) {
	if msg.Role != "toolResult" {
		return "", false
	}
	if callID := strings.TrimSpace(msg.ToolCallID); callID != "" {
		return "call_id:" + callID, true
	}
	toolName := strings.TrimSpace(msg.ToolName)
	if toolName == "" {
		toolName = "unknown"
	}
	return "tool_name:" + toolName + "|text_hash:" + hashString(msg.ExtractText()), true
}

func toolSummaryDedupKey(msg AgentMessage) (string, bool) {
	if msg.Metadata == nil || msg.Metadata.Kind != "tool_summary" {
		return "", false
	}
	text := strings.TrimSpace(msg.ExtractText())
	if text == "" {
		return "", false
	}
	return hashString(text), true
}

func hashString(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
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
func ConvertToolsToLLM(ctx context.Context, tools []Tool) []llm.LLMTool {
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
