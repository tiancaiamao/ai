package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"strings"

	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// ConvertMessagesToLLM converts agent messages to LLM messages.
func ConvertMessagesToLLM(ctx context.Context, messages []agentctx.AgentMessage) []llm.LLMMessage {
	span := traceevent.StartSpan(ctx, "ConvertMessagesToLLM", traceevent.CategoryEvent)
	defer span.End()

	messages = dedupeMessagesForLLM(messages)
	lastUserIndex := findLastVisibleUserIndex(messages)
	protectedToolResults := protectedRecentToolResultIndexes(messages, recentToolResultsNoMetadata)

	// Pre-calculate age rank for each tool result message
	toolResultAgeRanks := make(map[int]int)
	toolResultCount := 0
	for i, msg := range messages {
		if !msg.IsAgentVisible() || msg.Role != "toolResult" {
			continue
		}
		if lastUserIndex < 0 || i >= lastUserIndex {
			continue
		}
		if _, protected := protectedToolResults[i]; protected {
			continue
		}
		toolResultCount++
		toolResultAgeRanks[i] = toolResultCount
	}

	llmMessages := make([]llm.LLMMessage, 0, len(messages))

	for i, msg := range messages {
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
			content := msg.ExtractText()
			if shouldInjectStaleToolMetadata(msg, i, lastUserIndex, protectedToolResults) {
				charCount := len(content)
				if n, ok := parseCharsFromAgentToolTag(content); ok {
					charCount = n
				}
				toolName := strings.TrimSpace(msg.ToolName)
				if toolName == "" {
					toolName = "unknown"
				}
				ageRank := toolResultAgeRanks[i]
				staleTag := fmt.Sprintf(
					`<agent:tool id="%s" name="%s" chars="%d" stale="%d" />`,
					msg.ToolCallID,
					toolName,
					charCount,
					ageRank,
				)
				if content == "" {
					content = staleTag
				} else {
					content = staleTag + "\n" + content
				}
			}
			llmMsg.Content = content
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

func shouldInjectStaleToolMetadata(
	msg agentctx.AgentMessage,
	messageIndex int,
	lastUserIndex int,
	protectedToolResults map[int]struct{},
) bool {
	if msg.Role != "toolResult" {
		return false
	}
	if lastUserIndex < 0 || messageIndex >= lastUserIndex {
		return false
	}
	if _, protected := protectedToolResults[messageIndex]; protected {
		return false
	}
	return !hasAgentToolMetadataTag(msg.ExtractText())
}

func dedupeMessagesForLLM(messages []agentctx.AgentMessage) []agentctx.AgentMessage {
	if len(messages) <= 1 {
		return messages
	}

	seenToolResults := make(map[string]struct{}, len(messages))
	seenSummaries := make(map[string]struct{}, len(messages))
	seenAssistantToolCalls := make(map[string]struct{}, len(messages))
	keptReverse := make([]agentctx.AgentMessage, 0, len(messages))

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

		// Deduplicate assistant messages with duplicate tool call sets.
		if key, ok := assistantToolCallsDedupKey(msg); ok {
			if _, seen := seenAssistantToolCalls[key]; seen {
				continue
			}
			seenAssistantToolCalls[key] = struct{}{}
		}

		keptReverse = append(keptReverse, msg)
	}

	for i, j := 0, len(keptReverse)-1; i < j; i, j = i+1, j-1 {
		keptReverse[i], keptReverse[j] = keptReverse[j], keptReverse[i]
	}

	return keptReverse
}

func toolResultDedupKey(msg agentctx.AgentMessage) (string, bool) {
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

func toolSummaryDedupKey(msg agentctx.AgentMessage) (string, bool) {
	if msg.Metadata == nil || msg.Metadata.Kind != "tool_summary" {
		return "", false
	}
	text := strings.TrimSpace(msg.ExtractText())
	if text == "" {
		return "", false
	}
	return hashString(text), true
}

func assistantToolCallsDedupKey(msg agentctx.AgentMessage) (string, bool) {
	if msg.Role != "assistant" {
		return "", false
	}

	toolCalls := msg.ExtractToolCalls()
	if len(toolCalls) == 0 {
		return "", false
	}

	parts := make([]string, 0, len(toolCalls))
	for i, tc := range toolCalls {
		callID := strings.TrimSpace(tc.ID)
		if callID != "" {
			parts = append(parts, "id:"+callID)
			continue
		}

		argsJSON, _ := json.Marshal(tc.Arguments)
		parts = append(parts, fmt.Sprintf(
			"idx:%d|name:%s|args:%s",
			i,
			strings.TrimSpace(tc.Name),
			string(argsJSON),
		))
	}
	return strings.Join(parts, ";"), true
}

func hashString(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
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
