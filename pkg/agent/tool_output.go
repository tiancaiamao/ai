package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultToolOutputMaxLines          = 2000
	defaultToolOutputMaxBytes          = 50 * 1024
	defaultToolOutputMaxChars          = 200 * 1024
	defaultLargeOutputThresholdChars   = 200 * 1024
	defaultToolOutputTruncateMode      = "head"
	defaultToolOutputTruncateModeSplit = "head_tail"
)

// ToolOutputLimits controls truncation for tool result text.
type ToolOutputLimits struct {
	MaxLines             int
	MaxBytes             int
	MaxChars             int
	LargeOutputThreshold int
	TruncateMode         string // "head" or "head_tail"
}

// DefaultToolOutputLimits returns the default tool output limits.
func DefaultToolOutputLimits() ToolOutputLimits {
	return ToolOutputLimits{
		MaxLines:             defaultToolOutputMaxLines,
		MaxBytes:             defaultToolOutputMaxBytes,
		MaxChars:             defaultToolOutputMaxChars,
		LargeOutputThreshold: defaultLargeOutputThresholdChars,
		TruncateMode:         defaultToolOutputTruncateModeSplit,
	}
}

type toolTruncationStats struct {
	truncated   bool
	truncatedBy string
	totalLines  int
	totalBytes  int
	totalChars  int
	outputLines int
	outputBytes int
	outputChars int
}

func truncateToolContent(content []ContentBlock, limits ToolOutputLimits) []ContentBlock {
	if len(content) == 0 {
		return content
	}

	text := extractTextFromBlocks(content)
	if text == "" {
		return content
	}

	effective := normalizeToolOutputLimits(limits)
	maxLines := effective.MaxLines
	maxBytes := effective.MaxBytes
	maxChars := effective.MaxChars

	totalChars := countRunes(text)
	if effective.LargeOutputThreshold > 0 && totalChars > effective.LargeOutputThreshold {
		path, checksum, err := spillToolOutputToFile(text)
		if err == nil {
			notice := fmt.Sprintf(
				"[tool output too large: %d chars]\nSaved to: %s\nSHA256: %s",
				totalChars,
				path,
				checksum,
			)
			return []ContentBlock{
				TextContent{
					Type: "text",
					Text: notice,
				},
			}
		}
		text = fmt.Sprintf("[tool output spill failed: %v]\n\n%s", err, text)
	}

	if maxLines <= 0 && maxBytes <= 0 && maxChars <= 0 {
		return content
	}

	truncatedText, stats := truncateToolText(text, maxLines, maxBytes, maxChars, effective.TruncateMode)
	if !stats.truncated {
		if text == extractTextFromBlocks(content) {
			return content
		}
		return []ContentBlock{
			TextContent{
				Type: "text",
				Text: truncatedText,
			},
		}
	}

	notice := fmt.Sprintf(
		"\n\n[tool output truncated: showing %d/%d lines, %s/%s bytes, %d/%d chars]",
		stats.outputLines,
		stats.totalLines,
		formatBytes(stats.outputBytes),
		formatBytes(stats.totalBytes),
		stats.outputChars,
		stats.totalChars,
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

func truncateToolText(text string, maxLines, maxBytes, maxChars int, truncateMode string) (string, toolTruncationStats) {
	totalLines := countLines(text)
	totalBytes := len([]byte(text))
	totalChars := countRunes(text)
	stats := toolTruncationStats{
		truncated:  false,
		totalLines: totalLines,
		totalBytes: totalBytes,
		totalChars: totalChars,
	}

	linesLimit := maxLines
	if linesLimit <= 0 {
		linesLimit = totalLines
	}
	bytesLimit := maxBytes
	if bytesLimit <= 0 {
		bytesLimit = int(^uint(0) >> 1)
	}
	charsLimit := maxChars
	if charsLimit <= 0 {
		charsLimit = int(^uint(0) >> 1)
	}

	if totalLines <= linesLimit && totalBytes <= bytesLimit && totalChars <= charsLimit {
		stats.outputLines = totalLines
		stats.outputBytes = totalBytes
		stats.outputChars = totalChars
		return text, stats
	}

	output := truncateByLinesMode(text, linesLimit, truncateMode)
	truncatedBy := ""
	if output != text {
		truncatedBy = "lines"
	}

	output = truncateStringByRunes(output, charsLimit)
	if countRunes(output) < totalChars && truncatedBy == "" {
		truncatedBy = "chars"
	}

	output = truncateStringByBytes(output, bytesLimit)
	if len([]byte(output)) < totalBytes && truncatedBy == "" {
		truncatedBy = "bytes"
	}

	outputBytes := len([]byte(output))
	outputLines := countLines(output)
	outputChars := countRunes(output)

	stats.truncated = true
	stats.truncatedBy = truncatedBy
	stats.outputLines = outputLines
	stats.outputBytes = outputBytes
	stats.outputChars = outputChars

	return output, stats
}

func truncateByLinesMode(text string, linesLimit int, truncateMode string) string {
	if linesLimit <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= linesLimit {
		return text
	}

	mode := strings.ToLower(strings.TrimSpace(truncateMode))
	if mode == "" {
		mode = defaultToolOutputTruncateMode
	}
	if mode != defaultToolOutputTruncateModeSplit || linesLimit < 4 {
		return strings.Join(lines[:linesLimit], "\n")
	}

	headCount := linesLimit / 2
	tailCount := linesLimit - headCount
	if headCount == 0 || tailCount == 0 || headCount+tailCount > len(lines) {
		return strings.Join(lines[:linesLimit], "\n")
	}

	head := lines[:headCount]
	tail := lines[len(lines)-tailCount:]
	marker := fmt.Sprintf("... [truncated %d lines] ...", len(lines)-linesLimit)
	out := make([]string, 0, len(head)+len(tail)+1)
	out = append(out, head...)
	out = append(out, marker)
	out = append(out, tail...)
	return strings.Join(out, "\n")
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

func truncateStringByRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if countRunes(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func countRunes(s string) int {
	return utf8.RuneCountInString(s)
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

func normalizeToolOutputLimits(limits ToolOutputLimits) ToolOutputLimits {
	defaults := DefaultToolOutputLimits()
	if limits.TruncateMode == "" {
		limits.TruncateMode = defaults.TruncateMode
	}
	if limits.LargeOutputThreshold < 0 {
		limits.LargeOutputThreshold = defaults.LargeOutputThreshold
	}
	return limits
}

func spillToolOutputToFile(text string) (string, string, error) {
	sum := sha256.Sum256([]byte(text))
	checksum := hex.EncodeToString(sum[:])

	dir := filepath.Join(os.TempDir(), "ai_tool_outputs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}

	name := fmt.Sprintf("tool_output_%d.txt", time.Now().UnixNano())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", "", err
	}
	return path, checksum, nil
}
