package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
	"github.com/tiancaiamao/ai/pkg/truncate"
)

const (
	// Match Codex default truncation order of magnitude: 10,000 bytes/chars.
	defaultToolOutputMaxChars = 10_000
	// Hard safety cap to avoid configuration values that can exhaust model context.
	maxToolOutputMaxChars = 30_000

	// find_skill loads skill files which can be long (orchestration skills
	// routinely exceed 20K chars). Use a higher limit to avoid cutting off
	// critical rules at the end of the file.
	skillToolOutputMaxChars = 30_000

	// toolOutputOffloadCap is the hard size cap for offloading full tool output
	// to disk. Outputs larger than this are truncated without offload.
	toolOutputOffloadCap = 16 << 20 // 16MB
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
// When truncation occurs, the full original output is written to a file (under
// the session dir, falling back to /tmp) and the truncation marker points at
// it, so the model can retrieve the full output with the `read` tool instead
// of re-running the command. It emits a traceevent for observability.
func truncateToolContent(ctx context.Context, content []agentctx.ContentBlock, limits ToolOutputLimits, toolName, toolCallID, sessionDir, runID string) []agentctx.ContentBlock {
	if len(content) == 0 {
		return content
	}

	maxChars := normalizeToolOutputLimits(limits).MaxChars

	// find_skill returns full skill files which are often >10K chars.
	// Use a higher limit so critical rules at the end aren't truncated.
	if toolName == "find_skill" && maxChars < skillToolOutputMaxChars {
		maxChars = skillToolOutputMaxChars
	}

	result := make([]agentctx.ContentBlock, 0, len(content))
	// 1-based index of truncated text blocks within this tool result. Multiple
	// truncated blocks share the tool call id, so blocks after the first get a
	// "-N" filename suffix to avoid overwriting each other's offload files.
	truncatedBlockIndex := 0
	for _, block := range content {
		switch b := block.(type) {
		case agentctx.TextContent:
			originalLen := len(b.Text)

			// Check if truncation is needed
			if originalLen > maxChars {
				// Offload the full output so truncation is recoverable via `read`.
				truncatedBlockIndex++
				nameID := toolCallID
				if truncatedBlockIndex > 1 {
					nameID = fmt.Sprintf("%s-%d", toolCallID, truncatedBlockIndex)
				}
				offloadPath := offloadTruncatedToolOutput(b.Text, nameID, sessionDir, runID)
				markerSuffix := ""
				if offloadPath != "" {
					markerSuffix = ", full output: " + offloadPath
				}

				// Apply truncation
				truncated := truncate.TruncateWithMarkerSuffix(b.Text, maxChars, markerSuffix)
				removedTokens := truncate.ApproxTokenCount(b.Text) - truncate.ApproxTokenCount(truncated)

				// 🔍 Emit observability event
				fields := []traceevent.Field{
					{Key: "tool", Value: toolName},
					{Key: "original_chars", Value: originalLen},
					{Key: "truncated_chars", Value: len(truncated)},
					{Key: "max_chars", Value: maxChars},
					{Key: "removed_tokens", Value: removedTokens},
					{Key: "compression_ratio", Value: float64(len(truncated)) / float64(originalLen)},
				}
				if offloadPath != "" {
					fields = append(fields, traceevent.Field{Key: "offload_path", Value: offloadPath})
				} else if len(b.Text) > toolOutputOffloadCap {
					fields = append(fields, traceevent.Field{Key: "offload_skipped", Value: "output exceeds offload size cap"})
				} else {
					fields = append(fields, traceevent.Field{Key: "offload_skipped", Value: "write failed"})
				}
				traceevent.Log(ctx, traceevent.CategoryTool, "tool_output_truncated", fields...)

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

// offloadTruncatedToolOutput writes the full original tool output to disk and
// returns its absolute path, or "" if offload was skipped (oversized) or the
// write failed. The file is named by tool call id when available (stable
// across retries of the same call), else by content hash.
func offloadTruncatedToolOutput(text, toolCallID, sessionDir, runID string) string {
	if len(text) > toolOutputOffloadCap {
		return ""
	}

	name := sanitizeToolOutputFilename(toolCallID)
	if name == "" {
		sum := sha256.Sum256([]byte(text))
		name = hex.EncodeToString(sum[:8])
	}

	var path string
	if sessionDir != "" {
		path = filepath.Join(sessionDir, "toolout", name+".txt")
	} else if runID != "" {
		path = filepath.Join("/tmp", fmt.Sprintf("ai-toolout-%s-%s.txt", runID, name))
	} else {
		path = filepath.Join("/tmp", fmt.Sprintf("ai-toolout-%s.txt", name))
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}

	// Idempotency: don't rewrite identical content.
	if existing, err := os.ReadFile(path); err == nil && string(existing) == text {
		return path
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return ""
	}
	return path
}

// sanitizeToolOutputFilename allows only [A-Za-z0-9_-]; anything else (in
// particular path separators from untrusted tool call ids) falls back to a
// content hash.
func sanitizeToolOutputFilename(name string) string {
	if name == "" {
		return ""
	}
	for _, r := range name {
		if !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	return name
}
