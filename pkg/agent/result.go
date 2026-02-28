package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"strings"
)

// HeadlessResult represents the concise output for headless mode.
type HeadlessResult struct {
	Text     string     `json:"text"`
	Usage    UsageStats `json:"usage"`
	Error    string     `json:"error,omitempty"`
	ExitCode int        `json:"exit_code"`
}

// UsageStats represents token usage statistics for headless output.
type UsageStats struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// GetFinalAssistantText returns the text content of the last assistant message.
// It iterates in reverse to find the most recent assistant message and extracts
// all agentctx.TextContent from its content blocks.
func GetFinalAssistantText(messages []agentctx.AgentMessage) string {
	// Iterate in reverse to find the last assistant message
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role == "assistant" {
			return extractTextFromContent(msg.Content)
		}
	}
	return ""
}

// GetAssistantTexts returns all assistant text content concatenated with newlines.
// This is useful when you want to see the full conversation output from the assistant.
func GetAssistantTexts(messages []agentctx.AgentMessage) string {
	var texts []string
	for _, msg := range messages {
		if msg.Role == "assistant" {
			text := extractTextFromContent(msg.Content)
			if text != "" {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "\n")
}

// GetTotalUsage aggregates usage statistics from all assistant messages.
func GetTotalUsage(messages []agentctx.AgentMessage) UsageStats {
	var total UsageStats
	for _, msg := range messages {
		if msg.Role == "assistant" && msg.Usage != nil {
			total.InputTokens += msg.Usage.InputTokens
			total.OutputTokens += msg.Usage.OutputTokens
			total.TotalTokens += msg.Usage.TotalTokens
		}
	}
	return total
}

// extractTextFromContent extracts all text from content blocks.
func extractTextFromContent(content []agentctx.ContentBlock) string {
	var texts []string
	for _, block := range content {
		switch v := block.(type) {
		case agentctx.TextContent:
			if v.Text != "" {
				texts = append(texts, v.Text)
			}
		}
	}
	return strings.Join(texts, "")
}