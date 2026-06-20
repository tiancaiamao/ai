package ls

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

func TestFormatAge(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name      string
		startedAt int64
		want      string
	}{
		{"just now", now, "just now"},
		{"1 second ago", now - 1, "just now"},
		{"4 seconds ago", now - 4, "just now"},
		{"5 seconds ago", now - 5, "5s"},
		{"30 seconds ago", now - 30, "30s"},
		{"59 seconds ago", now - 59, "59s"},
		{"1 minute ago", now - 60, "1m"},
		{"5 minutes ago", now - 300, "5m"},
		{"59 minutes ago", now - 3540, "59m"},
		{"1 hour ago", now - 3600, "1h"},
		{"2 hours ago", now - 7200, "2h"},
		{"23 hours ago", now - 82800, "23h"},
		{"1 day ago", now - 86400, "1d"},
		{"3 days ago", now - 259200, "3d"},
		{"29 days ago", now - 2505600, "29d"},
		{"100 days ago", now - 8640000, "100d"},
		{"future", now + 100, "just now"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAge(tt.startedAt)
			if got != tt.want {
				t.Errorf("formatAge(%d) = %q, want %q", tt.startedAt, got, tt.want)
			}
		})
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		s      string
		maxLen int
		want   string
	}{
		{"short", 30, "short"},
		{"exactly30chars__________________xx", 34, "exactly30chars__________________xx"},
		{"this is a very long path that exceeds 30 chars", 30, "…ng path that exceeds 30 chars"},
		{"ab", 1, "…"},
		{"a", 1, "a"},
		{"", 30, ""},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got := truncateStr(tt.s, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestColorizeStatus(t *testing.T) {
	// Verify each status produces output that contains the status string
	// and ANSI escape sequences.
	statuses := []string{"running", "done", "failed", "killed", "unknown"}
	for _, s := range statuses {
		got := colorizeStatus(s)
		if got == "" {
			t.Errorf("colorizeStatus(%q) returned empty string", s)
		}
		// All known statuses should contain the status text.
		if s != "unknown" {
			if !contains(got, s) {
				t.Errorf("colorizeStatus(%q) = %q, want to contain %q", s, got, s)
			}
		}
	}

	// "running" should be green
	if !contains(colorizeStatus("running"), "\x1b[32m") {
		t.Error("running status should be green")
	}
	// "done" should be gray
	if !contains(colorizeStatus("done"), "\x1b[90m") {
		t.Error("done status should be gray")
	}
	// "failed" should be red
	if !contains(colorizeStatus("failed"), "\x1b[31m") {
		t.Error("failed status should be red")
	}
	// "killed" should be yellow
	if !contains(colorizeStatus("killed"), "\x1b[33m") {
		t.Error("killed status should be yellow")
	}
}

func TestEmitJSON(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	now := time.Now().Unix()
	runs := []tui.RunMeta{
		{
			ID:        "test1",
			CWD:       "/tmp/test",
			Name:      "test run",
			Status:    tui.StatusRunning,
			StartedAt: now,
		},
		{
			ID:        "test2",
			CWD:       "/tmp/test2",
			Name:      "another test",
			Status:    tui.StatusDone,
			StartedAt: now - 3600,
		},
	}

	emitJSON(runs)

	// Restore stdout and read captured output
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify output is valid JSON
	var entries []lsRunEntry
	if err := json.Unmarshal([]byte(output), &entries); err != nil {
		t.Fatalf("output is not valid JSON: %v\nOutput: %s", err, output)
	}

	// Verify entries
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ID != "test1" {
		t.Errorf("expected ID test1, got %s", entries[0].ID)
	}
	if entries[1].ID != "test2" {
		t.Errorf("expected ID test2, got %s", entries[1].ID)
	}
}

func TestEmitTable(t *testing.T) {
	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	now := time.Now().Unix()
	runs := []tui.RunMeta{
		{
			ID:        "test1",
			CWD:       "/tmp/test",
			Name:      "test run",
			Status:    tui.StatusRunning,
			StartedAt: now,
		},
		{
			ID:        "test2",
			CWD:       "/tmp/test2",
			Name:      "another test",
			Status:    tui.StatusDone,
			StartedAt: now - 3600,
		},
	}

	emitTable(runs)

	// Restore stdout and read captured output
	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify output contains header
	if !contains(output, "ID") || !contains(output, "STATUS") || !contains(output, "NAME") {
		t.Errorf("output should contain table header\nGot:\n%s", output)
	}

	// Verify output contains run IDs
	if !contains(output, "test1") || !contains(output, "test2") {
		t.Errorf("output should contain run IDs\nGot:\n%s", output)
	}

	// Empty list should produce no output
	r2, w2, _ := os.Pipe()
	os.Stdout = w2
	emitTable([]tui.RunMeta{})
	w2.Close()
	os.Stdout = oldStdout

	var buf2 bytes.Buffer
	buf2.ReadFrom(r2)
	output2 := buf2.String()
	if output2 != "" {
		t.Errorf("empty list should produce no output, got: %q", output2)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
