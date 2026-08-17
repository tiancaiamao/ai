package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestIsLikelyTruncatedToolArguments(t *testing.T) {
	tests := []struct {
		name       string
		stopReason string
		err        error
		expected   bool
	}{
		{"nil error", "length", nil, false},
		{"not length stop", "end_turn", errors.New("missing path"), false},
		{"length + missing", "length", errors.New("missing path"), true},
		{"length + other error", "length", errors.New("invalid schema"), false},
		{"length + padded missing", " length ", errors.New("  Missing path "), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLikelyTruncatedToolArguments(tt.stopReason, tt.err)
			if got != tt.expected {
				t.Errorf("isLikelyTruncatedToolArguments(%q, %v) = %v, want %v",
					tt.stopReason, tt.err, got, tt.expected)
			}
		})
	}
}

func TestBuildInvalidToolArgsMessage(t *testing.T) {
	tools := []string{"read", "write", "edit", "bash", "grep", "unknown_tool"}
	for _, tool := range tools {
		msg := buildInvalidToolArgsMessage(tool, errors.New("missing path"), "end_turn")
		if !strings.Contains(msg, "Invalid tool arguments for '"+tool+"'") {
			t.Errorf("tool %q: message missing header: %q", tool, msg)
		}
		if !strings.Contains(msg, "missing path") {
			t.Errorf("tool %q: message missing error detail: %q", tool, msg)
		}
	}

	// Known tools should include format examples.
	readMsg := buildInvalidToolArgsMessage("read", errors.New("missing path"), "end_turn")
	if !strings.Contains(readMsg, "<path>file.txt</path>") {
		t.Errorf("read message missing format example: %q", readMsg)
	}
	bashMsg := buildInvalidToolArgsMessage("bash", errors.New("missing command"), "end_turn")
	if !strings.Contains(bashMsg, "<command>") {
		t.Errorf("bash message missing format example: %q", bashMsg)
	}
}

func TestBuildInvalidToolArgsMessage_Truncated(t *testing.T) {
	msg := buildInvalidToolArgsMessage("write", errors.New("missing path/content"), "length")
	if !strings.Contains(msg, "truncated because the assistant response hit max_tokens") {
		t.Errorf("truncated message missing truncation notice: %q", msg)
	}
	if !strings.Contains(msg, "<content>content here</content>") {
		t.Errorf("truncated write message missing format example: %q", msg)
	}
}

func TestBuildTruncatedToolArgsMessage(t *testing.T) {
	tools := []string{"read", "write", "edit", "bash", "grep", "other"}
	for _, tool := range tools {
		msg := buildTruncatedToolArgsMessage(tool, errors.New("missing x"))
		if !strings.Contains(msg, "truncation issue") {
			t.Errorf("tool %q: missing truncation explanation: %q", tool, msg)
		}
		if !strings.Contains(msg, "Expected format:") {
			t.Errorf("tool %q: missing format section: %q", tool, msg)
		}
	}
}
