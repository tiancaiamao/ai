package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	defaultToolOutputMaxLines = 2000
	defaultToolOutputMaxBytes = 50 * 1024
)

// ToolOutputLimits controls truncation for tool result text.
type ToolOutputLimits struct {
	MaxLines int
	MaxBytes int
}

// DefaultToolOutputLimits returns the default tool output limits.
func DefaultToolOutputLimits() ToolOutputLimits {
	return ToolOutputLimits{
		MaxLines: defaultToolOutputMaxLines,
		MaxBytes: defaultToolOutputMaxBytes,
	}
}

type toolTruncationStats struct {
	truncated   bool
	truncatedBy string
	totalLines  int
	totalBytes  int
	outputLines int
	outputBytes int
}

func truncateToolContent(content []ContentBlock, limits ToolOutputLimits) []ContentBlock {
	if len(content) == 0 {
		return content
	}

	text := extractTextFromBlocks(content)
	if text == "" {
		return content
	}

	maxLines := limits.MaxLines
	maxBytes := limits.MaxBytes
	if maxLines <= 0 && maxBytes <= 0 {
		return content
	}

	truncatedText, stats := truncateToolText(text, maxLines, maxBytes)
	if !stats.truncated {
		return content
	}

	notice := fmt.Sprintf(
		"\n\n[tool output truncated: showing %d/%d lines, %s/%s bytes]",
		stats.outputLines,
		stats.totalLines,
		formatBytes(stats.outputBytes),
		formatBytes(stats.totalBytes),
	)

	return []ContentBlock{
		TextContent{
			Type: "text",
			Text: truncatedText + notice,
		},
	}
}

func extractTextFromBlocks(content []ContentBlock) string {
	var b strings.Builder
	for _, block := range content {
		if tc, ok := block.(TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func truncateToolText(text string, maxLines, maxBytes int) (string, toolTruncationStats) {
	totalLines := countLines(text)
	totalBytes := len([]byte(text))
	stats := toolTruncationStats{
		truncated:  false,
		totalLines: totalLines,
		totalBytes: totalBytes,
	}

	linesLimit := maxLines
	if linesLimit <= 0 {
		linesLimit = totalLines
	}
	bytesLimit := maxBytes
	if bytesLimit <= 0 {
		bytesLimit = totalBytes
	}

	if totalLines <= linesLimit && totalBytes <= bytesLimit {
		stats.outputLines = totalLines
		stats.outputBytes = totalBytes
		return text, stats
	}

	lines := strings.Split(text, "\n")
	outLines := make([]string, 0, minInt(linesLimit, len(lines)))
	usedBytes := 0
	truncatedBy := ""

	for i := 0; i < len(lines) && i < linesLimit; i++ {
		line := lines[i]
		lineBytes := len([]byte(line))
		addBytes := lineBytes
		if i > 0 {
			addBytes++
		}

		if usedBytes+addBytes > bytesLimit {
			if len(outLines) == 0 && bytesLimit > 0 {
				outLines = append(outLines, truncateStringByBytes(line, bytesLimit))
			}
			truncatedBy = "bytes"
			break
		}

		outLines = append(outLines, line)
		usedBytes += addBytes
	}

	if truncatedBy == "" && totalLines > linesLimit {
		truncatedBy = "lines"
	} else if truncatedBy == "" && totalBytes > bytesLimit {
		truncatedBy = "bytes"
	}

	output := strings.Join(outLines, "\n")
	outputBytes := len([]byte(output))
	outputLines := countLines(output)

	stats.truncated = true
	stats.truncatedBy = truncatedBy
	stats.outputLines = outputLines
	stats.outputBytes = outputBytes

	return output, stats
}

func truncateStringByBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}

	limit := maxBytes
	if limit > len(s) {
		limit = len(s)
	}

	for limit > 0 && !utf8.ValidString(s[:limit]) {
		limit--
	}
	if limit <= 0 {
		return ""
	}
	return s[:limit]
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func formatBytes(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	kb := float64(bytes) / 1024.0
	if kb < 1024 {
		return fmt.Sprintf("%.1fKB", kb)
	}
	mb := kb / 1024.0
	return fmt.Sprintf("%.1fMB", mb)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
