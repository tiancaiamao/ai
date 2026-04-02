package context

import (
	"fmt"
	"strings"
)

// RenderToolResult renders a tool result for LLM consumption.
// Mode-specific: Normal mode hides ID, ContextMgmt mode exposes metadata.
func RenderToolResult(msg *AgentMessage, mode AgentMode, stale int) string {
	if mode == ModeNormal {
		// Standard rendering, hide ID
		return msg.RenderContent()
	}

	if mode == ModeContextMgmt {
		// Special rendering, expose ID + metadata
		content := msg.ExtractText()

		// Handle large output preview
		if len(content) > ToolOutputMaxChars {
			head := content[:ToolOutputPreviewHead]
			tail := content[len(content)-ToolOutputPreviewTail:]
			truncatedChars := len(content) - ToolOutputPreviewHead - ToolOutputPreviewTail
			content = fmt.Sprintf("%s\n... (%d chars truncated) ...\n%s",
				head, truncatedChars, tail)
		}

		return fmt.Sprintf(
			`<agent:tool id="%s" name="%s" stale="%d" chars="%d">%s</agent:tool>`,
			msg.ToolCallID, msg.ToolName, stale, len(msg.ExtractText()), content,
		)
	}

	return msg.RenderContent()
}

// RenderContent renders just the content without metadata.
func (m AgentMessage) RenderContent() string {
	// For tool results, extract and return text content
	if m.Role == "toolResult" {
		return m.ExtractText()
	}

	// For other message types, concatenate all text blocks
	var parts []string
	for _, block := range m.Content {
		if tc, ok := block.(TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
