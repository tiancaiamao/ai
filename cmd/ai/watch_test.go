package main

import (
	"strings"
	"testing"
)

func TestWrapContent_PlainTextShortLine(t *testing.T) {
	// Line shorter than width — no wrapping.
	input := "hello world"
	got := wrapContent(input, 80)
	if got != input {
		t.Errorf("expected %q, got %q", input, got)
	}
}

func TestWrapContent_PlainTextLongLine(t *testing.T) {
	// Line longer than width — should be wrapped.
	input := strings.Repeat("abcdefghij ", 10) // 110 chars
	got := wrapContent(input, 40)
	for _, line := range strings.Split(got, "\n") {
		// Each wrapped line should be at most ~40 visible chars.
		// We allow some slack since word boundaries may not align exactly.
		visible := stripAnsi(line)
		if len(visible) > 50 {
			t.Errorf("wrapped line too long (%d chars): %q", len(visible), visible)
		}
	}
}

func TestWrapContent_MultipleLines(t *testing.T) {
	// Multiple short lines — should remain unchanged.
	input := "line1\nline2\nline3"
	got := wrapContent(input, 80)
	if got != input {
		t.Errorf("expected %q, got %q", input, got)
	}
}

func TestWrapContent_EmptyString(t *testing.T) {
	got := wrapContent("", 80)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestWrapContent_ZeroWidth(t *testing.T) {
	input := "some text"
	got := wrapContent(input, 0)
	if got != input {
		t.Errorf("expected %q with zero width, got %q", input, got)
	}
}

func TestWrapContent_WithANSI(t *testing.T) {
	// ANSI styled text should be preserved and wrapped correctly.
	// Simulate a styled line (lipgloss output).
	styled := "\x1b[36m" + strings.Repeat("tool output line ", 10) + "\x1b[0m"
	got := wrapContent(styled, 40)

	// The result should still contain the ANSI codes.
	if !strings.Contains(got, "\x1b[36m") {
		t.Error("expected ANSI escape code \\x1b[36m to be preserved")
	}
	if !strings.Contains(got, "\x1b[0m") {
		t.Error("expected ANSI reset \\x1b[0m to be preserved")
	}

	// Should have been wrapped into multiple lines.
	if !strings.Contains(got, "\n") {
		t.Error("expected wrapped content to contain newlines")
	}
}

func TestWrapContent_MixedANSIAndPlain(t *testing.T) {
	// Mix of styled and plain lines.
	input := "plain line\n\x1b[31mred styled line that is quite long and should be wrapped at word boundaries\x1b[0m\nanother plain"
	got := wrapContent(input, 30)

	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines, got %d: %q", len(lines), got)
	}
}

func TestWrapContent_CJKWideCharacters(t *testing.T) {
	// CJK characters are double-width, wrapping should account for that.
	input := strings.Repeat("你好世界", 10) // 40 wide chars = 80 visible columns
	got := wrapContent(input, 40)

	// Should have been wrapped.
	if !strings.Contains(got, "\n") {
		t.Error("expected CJK content to be wrapped")
	}
}

func TestWrapContent_PreservesTrailingNewlines(t *testing.T) {
	// Content ending with newline should be handled.
	input := "line1\nline2\n"
	got := wrapContent(input, 80)
	// The trailing newline produces an empty string after split, which is skipped.
	if !strings.HasPrefix(got, "line1\nline2") {
		t.Errorf("expected preserved content, got %q", got)
	}
}

// stripAnsi removes ANSI escape sequences for measuring visible width.
func stripAnsi(s string) string {
	var result []byte
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEscape = false
			}
			continue
		}
		result = append(result, s[i])
	}
	return string(result)
}
