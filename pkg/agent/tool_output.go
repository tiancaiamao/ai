package agent

import (
	"context"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
	"github.com/tiancaiamao/ai/pkg/truncate"
	"strings"
)

const (
	// Match Codex default truncation order of magnitude: 10,000 bytes/chars.
	defaultToolOutputMaxChars = 10_000
	// Hard safety cap to avoid configuration values that can exhaust model context.
	maxToolOutputMaxChars = defaultToolOutputMaxChars
)

// ToolOutputLimits defines truncation limits for tool output (simplified).
type ToolOutputLimits struct {
	MaxChars int
}

// DefaultToolOutputLimits returns default truncation limits.
func DefaultToolOutputLimits() ToolOutputLimits {
	return ToolOutputLimits{MaxChars: defaultToolOutputMaxChars}
}

func normalizeToolOutputLimits(limits ToolOutputLimits) ToolOutputLimits {
	maxChars := limits.MaxChars
	if maxChars <= 0 {
		maxChars = defaultToolOutputMaxChars
	}
	if maxChars > maxToolOutputMaxChars {
		maxChars = maxToolOutputMaxChars
	}
	return ToolOutputLimits{MaxChars: maxChars}
}

// truncateToolContent truncates tool content based on maxChars limit.
// It preserves images and other non-text content types (type-aware truncation).
// When truncation occurs, it emits a traceevent for observability.
func truncateToolContent(ctx context.Context, content []agentctx.ContentBlock, limits ToolOutputLimits, toolName string) []agentctx.ContentBlock {
	if len(content) == 0 {
		return content
	}

	maxChars := normalizeToolOutputLimits(limits).MaxChars

	result := make([]agentctx.ContentBlock, 0, len(content))
	for _, block := range content {
		switch b := block.(type) {
		case agentctx.TextContent:
			originalLen := len(b.Text)

			// Check if truncation is needed
			if originalLen > maxChars {
				// Apply truncation
				truncated := truncate.Truncate(b.Text, maxChars)
				removedTokens := truncate.ApproxTokenCount(b.Text) - truncate.ApproxTokenCount(truncated)

				// 🔍 Emit observability event
				traceevent.Log(ctx, traceevent.CategoryTool, "tool_output_truncated",
					traceevent.Field{Key: "tool", Value: toolName},
					traceevent.Field{Key: "original_chars", Value: originalLen},
					traceevent.Field{Key: "truncated_chars", Value: len(truncated)},
					traceevent.Field{Key: "max_chars", Value: maxChars},
					traceevent.Field{Key: "removed_tokens", Value: removedTokens},
					traceevent.Field{Key: "compression_ratio", Value: float64(len(truncated)) / float64(originalLen)},
				)

				result = append(result, agentctx.TextContent{
					Type: "text",
					Text: truncated,
				})
			} else {
				// No truncation needed, preserve as-is
				result = append(result, block)
			}
		case agentctx.ImageContent:
			// Image: preserve completely (type-aware)
			result = append(result, block)
		default:
			// Other types: preserve
			result = append(result, block)
		}
	}

	return result
}

// containsErrorPattern checks if text contains common error patterns.
func containsErrorPattern(text string) bool {
	text = strings.ToLower(text)
	errorPatterns := []string{
		"error:",
		"failed:",
		"exception:",
		"fatal:",
		"panic:",
		"undefined",
		"not found",
		"no such file",
		"permission denied",
		"cannot",
		"could not",
		"unable to",
	}

	for _, pattern := range errorPatterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}
