package main

import (
	"os"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/run"
)

func TestNotifyParentAgent_NoParent(t *testing.T) {
	// When AI_PARENT_RUN_ID is not set, notifyParentAgent should be a no-op.
	os.Unsetenv("AI_PARENT_RUN_ID")
	// Should not panic or do anything.
	notifyParentAgent("test-run-id", run.StatusDone, "test-agent")
}

func TestNotifyParentAgent_ParentGone(t *testing.T) {
	// When the parent doesn't exist, notifyParentAgent should not panic.
	os.Setenv("AI_PARENT_RUN_ID", "nonexistent-parent")
	defer os.Unsetenv("AI_PARENT_RUN_ID")

	// Should not panic, just log a warning to stderr.
	notifyParentAgent("child-orphan", run.StatusDone, "orphan-agent")
}

func TestFormatAgentNotification(t *testing.T) {
	tests := []struct {
		name     string
		runID    string
		status   string
		agentName string
		want     []string // substrings that must appear
		dontWant []string // substrings that must NOT appear
	}{
		{
			name:     "completed with name",
			runID:    "child-run-456",
			status:   run.StatusDone,
			agentName: "my-subagent",
			want:     []string{"<agent:notification>", "</agent:notification>", "done", "child-run-456", "my-subagent"},
		},
		{
			name:     "failed with name",
			runID:    "child-failed-789",
			status:   run.StatusFailed,
			agentName: "worker-1",
			want:     []string{"failed", "child-failed-789", "worker-1"},
		},
				{
			name:     "empty name uses run ID prefix",
			runID:    "abcdefgh12345678",
			status:   run.StatusDone,
			agentName: "",
			want:     []string{"<name>abcdefgh</name>", "<run_id>abcdefgh12345678</run_id>"},
		},
		{
			name:     "full XML structure",
			runID:    "run-xyz",
			status:   run.StatusDone,
			agentName: "test",
			want: []string{
				"<agent:notification>",
				"<status>done</status>",
				"<run_id>run-xyz</run_id>",
				"<name>test</name>",
				"</agent:notification>",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAgentNotification(tt.runID, tt.status, tt.agentName)

			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("notification missing expected substring %q\n got: %s", want, got)
				}
			}
			for _, dontWant := range tt.dontWant {
				if strings.Contains(got, dontWant) {
					t.Errorf("notification should not contain %q\n got: %s", dontWant, got)
				}
			}
		})
	}
}

func TestFormatAgentNotification_NewlinesBetweenTags(t *testing.T) {
	// Ensure tags are on separate lines for readability.
	got := formatAgentNotification("run-1", run.StatusDone, "agent")
	if !strings.Contains(got, "\n") {
		t.Error("notification should have newlines between tags")
	}

	// Should have exactly the right structure: opening tag, 3 inner tags, closing tag.
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 5 {
		t.Errorf("expected 5 lines (open + 3 tags + close), got %d:\n%s", len(lines), got)
	}
}