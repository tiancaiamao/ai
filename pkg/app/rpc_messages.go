package app

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// FormatMessagesForDisplay converts AgentMessages into a structured summary
// for the /messages command. It returns the last `count` messages with
// previews truncated to maxPreviewLen characters.
func FormatMessagesForDisplay(messages []agentctx.AgentMessage, count int, maxPreviewLen int) MessagesResult {
	total := len(messages)

	start := total - count
	if start < 0 {
		start = 0
	}
	showing := total - start

	formatted := make([]FormattedMessage, 0, showing)
	for i := start; i < total; i++ {
		msg := messages[i]
		fm := FormattedMessage{
			Index: i,
			Role:  msg.Role,
		}

		preview := msg.ExtractText()
		if preview == "" {
			if thinking := msg.ExtractThinking(); thinking != "" {
				preview = "(thinking) " + thinking
			}
		}
		if len(preview) > maxPreviewLen {
			preview = preview[:maxPreviewLen] + "..."
		}
		fm.Preview = preview

		toolCalls := msg.ExtractToolCalls()
		if len(toolCalls) > 0 {
			names := make([]string, 0, len(toolCalls))
			for _, tc := range toolCalls {
				names = append(names, tc.Name)
			}
			fm.ToolCalls = names
		}

		if msg.ToolName != "" {
			fm.ToolName = msg.ToolName
		}
		fm.IsError = msg.IsError

		formatted = append(formatted, fm)
	}

	return MessagesResult{
		Total:    total,
		Showing:  showing,
		Messages: formatted,
	}
}
