package agent

import (
	"strings"
	"testing"
)

func TestTruncateToolContentByLines(t *testing.T) {
	content := []ContentBlock{
		TextContent{Type: "text", Text: "line1\nline2\nline3"},
	}
	limits := ToolOutputLimits{MaxLines: 2, MaxBytes: 0}

	truncated := truncateToolContent(content, limits)
	text := extractTextFromBlocks(truncated)

	if !strings.HasPrefix(text, "line1\nline2") {
		t.Fatalf("expected output to keep first two lines, got: %q", text)
	}
	if !strings.Contains(text, "tool output truncated") {
		t.Fatalf("expected truncation notice, got: %q", text)
	}
}

func TestTruncateToolContentByBytes(t *testing.T) {
	content := []ContentBlock{
		TextContent{Type: "text", Text: "123456789"},
	}
	limits := ToolOutputLimits{MaxLines: 0, MaxBytes: 5}

	truncated := truncateToolContent(content, limits)
	text := extractTextFromBlocks(truncated)

	if !strings.HasPrefix(text, "12345") {
		t.Fatalf("expected output to be byte-truncated, got: %q", text)
	}
	if !strings.Contains(text, "tool output truncated") {
		t.Fatalf("expected truncation notice, got: %q", text)
	}
}
