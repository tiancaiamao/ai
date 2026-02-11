package agent

import (
	"os"
	"path/filepath"
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

func TestTruncateToolContentHeadTailMode(t *testing.T) {
	content := []ContentBlock{
		TextContent{Type: "text", Text: "l1\nl2\nl3\nl4\nl5\nl6"},
	}
	limits := ToolOutputLimits{
		MaxLines:     4,
		MaxBytes:     0,
		MaxChars:     0,
		TruncateMode: "head_tail",
	}

	truncated := truncateToolContent(content, limits)
	text := extractTextFromBlocks(truncated)

	if !strings.Contains(text, "l1\nl2") {
		t.Fatalf("expected head lines in output, got: %q", text)
	}
	if !strings.Contains(text, "l5\nl6") {
		t.Fatalf("expected tail lines in output, got: %q", text)
	}
	if !strings.Contains(text, "truncated 2 lines") {
		t.Fatalf("expected head-tail marker, got: %q", text)
	}
}

func TestTruncateToolContentSpillsLargeOutputToFile(t *testing.T) {
	content := []ContentBlock{
		TextContent{Type: "text", Text: strings.Repeat("a", 64)},
	}
	limits := ToolOutputLimits{
		MaxLines:             0,
		MaxBytes:             0,
		MaxChars:             0,
		LargeOutputThreshold: 32,
	}

	truncated := truncateToolContent(content, limits)
	text := extractTextFromBlocks(truncated)

	if !strings.Contains(text, "tool output too large") || !strings.Contains(text, "Saved to: ") {
		t.Fatalf("expected spill notice, got: %q", text)
	}

	lines := strings.Split(text, "\n")
	var savedPath string
	for _, line := range lines {
		if strings.HasPrefix(line, "Saved to: ") {
			savedPath = strings.TrimSpace(strings.TrimPrefix(line, "Saved to: "))
		}
	}
	if savedPath == "" {
		t.Fatalf("expected saved path in spill notice, got: %q", text)
	}

	if !filepath.IsAbs(savedPath) {
		t.Fatalf("expected absolute saved path, got: %s", savedPath)
	}

	info, err := os.Stat(savedPath)
	if err != nil {
		t.Fatalf("expected spilled file to exist: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected spilled file to be non-empty")
	}

	_ = os.Remove(savedPath)
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
