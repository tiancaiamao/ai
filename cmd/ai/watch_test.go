package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/run"
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

func TestProcessEvent_LiveThinkingDelta_RendersImmediately(t *testing.T) {
	m := newWatchModel("", "test", 0, false)
	m.mode = "live"

	// A short delta without sentence boundary should still be visible immediately.
	m.processEvent(&run.FormattedEvent{
		Kind: run.KindThinking,
		Role: "thinking",
		Text: "abc",
		Raw:  "abc",
	})

	out := m.content.String()
	if !strings.Contains(out, "thinking: ") {
		t.Fatalf("expected thinking prefix, got %q", out)
	}
	if !strings.Contains(out, "abc") {
		t.Fatalf("expected delta content rendered immediately, got %q", out)
	}
}

func TestFollowWatchSummary_ExitsOnAgentEnd_WithTimeout(t *testing.T) {
	// Regression test: followWatchSummary must exit on agent_end
	// regardless of the watchTimeout value.
	// Previously, a positive watchTimeout caused it to skip agent_end
	// and keep waiting (wasting the full timeout duration).
	events := strings.Join([]string{
		`{"type":"text_delta","delta":"hello "}`,
		`{"type":"text_delta","delta":"world"}`,
		`{"type":"agent_end"}`,
		// Extra lines after agent_end — should never be reached.
		`{"type":"text_delta","delta":"should not appear"}`,
	}, "\n")

	scanner := bufio.NewScanner(strings.NewReader(events))

	// Capture stdout and stderr via pipes.
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	// Read captured output in background.
	var stdout, stderrBuf bytes.Buffer
	outDone := make(chan struct{})
	go func() {
		io.Copy(&stdout, rOut)
		close(outDone)
	}()
	errDone := make(chan struct{})
	go func() {
		io.Copy(&stderrBuf, rErr)
		close(errDone)
	}()

	done := make(chan struct{})
	go func() {
		// Use a positive timeout — the bug was that this prevented exit on agent_end.
		followWatchSummary(scanner, 0, 15*time.Minute)
		close(done)
	}()

	// Should exit quickly, not wait 15 minutes.
	select {
	case <-done:
		// Good — exited on agent_end.
	case <-time.After(5 * time.Second):
		t.Fatal("followWatchSummary did not exit on agent_end within 5s (would have waited full timeout)")
	}

	// Close write ends and restore before reading.
	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	<-outDone
	<-errDone

	// Should have printed the accumulated assistant text.
	if !strings.Contains(stdout.String(), "hello world") {
		t.Errorf("expected stdout to contain 'hello world', got %q", stdout.String())
	}
	// Should NOT contain text that comes after agent_end.
	if strings.Contains(stdout.String(), "should not appear") {
		t.Error("should not have processed events after agent_end")
	}
	// Stderr should contain __seq marker.
	if !strings.Contains(stderrBuf.String(), "__seq:3") {
		t.Errorf("expected stderr to contain '__seq:3', got %q", stderrBuf.String())
	}
}

func TestFollowWatchSummary_ExitsOnAgentEnd_WithoutTimeout(t *testing.T) {
	// When watchTimeout is -1 (not set), should also exit on agent_end.
	events := strings.Join([]string{
		`{"type":"text_delta","delta":"done"}`,
		`{"type":"agent_end"}`,
	}, "\n")

	scanner := bufio.NewScanner(strings.NewReader(events))

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	var stdout, stderrBuf bytes.Buffer
	outDone := make(chan struct{})
	go func() { io.Copy(&stdout, rOut); close(outDone) }()
	errDone := make(chan struct{})
	go func() { io.Copy(&stderrBuf, rErr); close(errDone) }()

	done := make(chan struct{})
	go func() {
		followWatchSummary(scanner, 0, -1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("followWatchSummary did not exit on agent_end")
	}

	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	<-outDone
	<-errDone

	if !strings.Contains(stdout.String(), "done") {
		t.Errorf("expected stdout to contain 'done', got %q", stdout.String())
	}
	if !strings.Contains(stderrBuf.String(), "__seq:2") {
		t.Errorf("expected stderr to contain '__seq:2', got %q", stderrBuf.String())
	}
}

func TestFollowWatchSummary_StreamEndsWithoutAgentEnd(t *testing.T) {
	// If stream ends without agent_end, should print whatever text was accumulated.
	events := strings.Join([]string{
		`{"type":"text_delta","delta":"partial"}`,
	}, "\n")

	scanner := bufio.NewScanner(strings.NewReader(events))

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	var stdout bytes.Buffer
	outDone := make(chan struct{})
	go func() { io.Copy(&stdout, rOut); close(outDone) }()
	errDone := make(chan struct{})
	var stderrBuf bytes.Buffer
	go func() { io.Copy(&stderrBuf, rErr); close(errDone) }()

	done := make(chan struct{})
	go func() {
		followWatchSummary(scanner, 0, 0)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("followWatchSummary did not exit when stream ended")
	}

	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	<-outDone
	<-errDone

	if !strings.Contains(stdout.String(), "partial") {
		t.Errorf("expected stdout to contain 'partial', got %q", stdout.String())
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
