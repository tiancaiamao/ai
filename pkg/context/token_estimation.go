package context

import "encoding/json"

// EstimateTokens estimates total token usage for the given context components.
// Accounts for system prompt, tools schema, and all messages
// (including thinking, tool calls, and images).
// Uses ~4 chars per token heuristic.
func EstimateTokens(systemPrompt string, tools []Tool, messages []AgentMessage) int {
	total := len(systemPrompt)

	// Include tool schema tokens
	total += EstimateToolsTokens(tools) * 4 // convert back to chars for summation

	for _, msg := range messages {
		total += estimateMessageChars(msg)
	}
	return total / 4
}

// EstimateToolsTokens estimates token count for the given tool schemas.
// Serializes tool definitions to JSON and applies chars/4 heuristic.
func EstimateToolsTokens(tools []Tool) int {
	if len(tools) == 0 {
		return 0
	}

	// Build a lightweight JSON representation matching what's sent to the LLM
	type toolFunc struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters,omitempty"`
	}
	type toolDef struct {
		Type     string   `json:"type"`
		Function toolFunc `json:"function"`
	}

	defs := make([]toolDef, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		defs = append(defs, toolDef{
			Type: "function",
			Function: toolFunc{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}

	data, err := json.Marshal(defs)
	if err != nil {
		return 0
	}
	return len(data) / 4
}

// EstimateMessageTokens estimates token count for a single message.
// Accounts for all content block types (text, thinking, tool calls, images).
// Non-agent-visible messages return 0.
// Uses ~4 chars per token heuristic.
func EstimateMessageTokens(msg AgentMessage) int {
	if !msg.IsAgentVisible() {
		return 0
	}

	charCount := 0
	for _, block := range msg.Content {
		switch b := block.(type) {
		case TextContent:
			charCount += len(b.Text)
		case ThinkingContent:
			charCount += len(b.Thinking)
		case ToolCallContent:
			charCount += len(b.Name)
			if b.Arguments != nil {
				if argBytes, err := json.Marshal(b.Arguments); err == nil {
					charCount += len(argBytes)
				}
			}
		case ImageContent:
			// Roughly estimate images as 1200 tokens (4800 chars)
			charCount += 4800
		}
	}

	if charCount == 0 {
		charCount = len(msg.ExtractText())
	}
	if charCount == 0 {
		return 0
	}

	// Rough approximation: 1 token per 4 characters
	return (charCount + 3) / 4
}

// EstimateTokenPercent returns token usage as a fraction of the limit.
func EstimateTokenPercent(used, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(used) / float64(total)
}

// estimateMessageChars counts characters in a message for token estimation.
func estimateMessageChars(msg AgentMessage) int {
	if !msg.IsAgentVisible() {
		return 0
	}

	charCount := 0
	for _, block := range msg.Content {
		switch b := block.(type) {
		case TextContent:
			charCount += len(b.Text)
		case ThinkingContent:
			charCount += len(b.Thinking)
		case ToolCallContent:
			charCount += len(b.Name)
			if b.Arguments != nil {
				if argBytes, err := json.Marshal(b.Arguments); err == nil {
					charCount += len(argBytes)
				}
			}
		case ImageContent:
			charCount += 4800
		}
	}

	if charCount == 0 {
		charCount = len(msg.ExtractText())
	}
	return charCount
}
