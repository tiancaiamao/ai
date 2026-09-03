package helpers

import (
	"os"
	"strings"
	"testing"

	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

func TestParseSystemPrompt(t *testing.T) {
	// Test 1: Empty string
	result, err := ParseSystemPrompt("")
	if err != nil || result != "" {
		t.Errorf("Expected empty string, got %q", result)
	}

	// Test 2: Plain text (no @ prefix)
	plainText := "You are a helpful assistant"
	result, err = ParseSystemPrompt(plainText)
	if err != nil || result != plainText {
		t.Errorf("Expected %q, got %q", plainText, result)
	}

	// Test 3: @ prefix with empty path
	result, err = ParseSystemPrompt("@")
	if err == nil {
		t.Errorf("Expected empty string for @ with no path, got %q", result)
	}

	// Test 4: @ prefix with whitespace only
	result, err = ParseSystemPrompt("@   ")
	if err == nil {
		t.Errorf("Expected empty string for @ with whitespace, got %q", result)
	}

	// Test 5: @ prefix with valid file
	tmpFile := t.TempDir() + "/test_prompt.md"
	content := "You are a test assistant"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	result, err = ParseSystemPrompt("@" + tmpFile)
	if err != nil || result != content {
		t.Errorf("Expected %q, got %q", content, result)
	}

	// Test 6: @ prefix with non-existent file
	result, err = ParseSystemPrompt("@/nonexistent/path/to/file.md")
	if err == nil {
		t.Errorf("Expected empty string for non-existent file, got %q", result)
	}

	// Test 7: Large file (truncate to 64KB)
	largeFile := t.TempDir() + "/large_prompt.md"
	largeContent := make([]byte, 70*1024) // 70KB
	for i := range largeContent {
		largeContent[i] = 'a'
	}
	if err := os.WriteFile(largeFile, largeContent, 0644); err != nil {
		t.Fatalf("Failed to create large temp file: %v", err)
	}
	result, err = ParseSystemPrompt("@" + largeFile)
	if err != nil || len(result) != 64*1024 {
		t.Errorf("Expected 64KB, got %d bytes", len(result))
	}

	// Test 8: @ with leading whitespace in path
	tmpFile2 := t.TempDir() + "/test_prompt2.md"
	if err := os.WriteFile(tmpFile2, []byte("Content 2"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	result, err = ParseSystemPrompt("@   " + tmpFile2)
	if err != nil || result != "Content 2" {
		t.Errorf("Expected 'Content 2', got %q", result)
	}
}

// saveTestRun writes a run.json for the given id/status under baseDir.
func saveTestRun(t *testing.T, baseDir, id, cwd, status string) {
	t.Helper()
	meta := &tui.RunMeta{
		ID:     id,
		PID:    os.Getpid(),
		CWD:    cwd,
		Status: status,
	}
	if err := tui.SaveRunMeta(meta, tui.RunMetaPath(baseDir, id)); err != nil {
		t.Fatalf("SaveRunMeta %s: %v", id, err)
	}
}

func TestResolveRunIDForHistory_AnyStatus(t *testing.T) {
	baseDir := t.TempDir()
	saveTestRun(t, baseDir, "aaa001", "/x", tui.StatusRunning)
	saveTestRun(t, baseDir, "bbb002", "/x", tui.StatusDone)
	saveTestRun(t, baseDir, "ccc003", "/x", tui.StatusFailed)
	saveTestRun(t, baseDir, "ddd004", "/x", tui.StatusKilled)

	for _, tc := range []struct{ id, wantStatus string }{
		{"aaa001", tui.StatusRunning},
		{"bbb002", tui.StatusDone},
		{"ccc003", tui.StatusFailed},
		{"ddd004", tui.StatusKilled},
	} {
		meta, err := ResolveRunIDForHistory(baseDir, tc.id)
		if err != nil {
			t.Fatalf("ResolveRunIDForHistory(%q): %v", tc.id, err)
		}
		if meta.ID != tc.id || meta.Status != tc.wantStatus {
			t.Errorf("got run %s (status %s), want %s (status %s)", meta.ID, meta.Status, tc.id, tc.wantStatus)
		}
	}
}

func TestResolveRunIDForHistory_PrefixMatchingAnyStatus(t *testing.T) {
	baseDir := t.TempDir()
	saveTestRun(t, baseDir, "abc123", "/x", tui.StatusDone)

	meta, err := ResolveRunIDForHistory(baseDir, "abc")
	if err != nil {
		t.Fatalf("prefix resolve: %v", err)
	}
	if meta.ID != "abc123" {
		t.Errorf("got %s, want abc123", meta.ID)
	}
}

func TestResolveRunIDForHistory_Errors(t *testing.T) {
	baseDir := t.TempDir()
	saveTestRun(t, baseDir, "abc123", "/x", tui.StatusDone)
	saveTestRun(t, baseDir, "abc456", "/x", tui.StatusFailed)

	// Nonexistent ID.
	if _, err := ResolveRunIDForHistory(baseDir, "zzz999"); err == nil {
		t.Error("expected error for nonexistent ID")
	}
	// Ambiguous prefix.
	if _, err := ResolveRunIDForHistory(baseDir, "abc"); err == nil {
		t.Error("expected error for ambiguous prefix")
	}
}

func TestResolveRunIDForHistory_RequiresID(t *testing.T) {
	baseDir := t.TempDir()
	// Even with runs present, empty id must error: cwd no longer identifies
	// the run (an agent's working directory can change during its lifetime).
	saveTestRun(t, baseDir, "done001", "/x", tui.StatusDone)
	_, err := ResolveRunIDForHistory(baseDir, "")
	if err == nil {
		t.Fatal("expected error when --id is omitted")
	}
	if !strings.Contains(err.Error(), "--id") {
		t.Errorf("error should tell the caller to pass --id: %v", err)
	}
}

func TestResolveRunID_ExistingSemanticsUnchanged(t *testing.T) {
	baseDir := t.TempDir()
	saveTestRun(t, baseDir, "aaa001", "/x", tui.StatusRunning)
	saveTestRun(t, baseDir, "bbb002", "/x", tui.StatusDone)

	// Running run resolves.
	meta, err := ResolveRunID(baseDir, "aaa001")
	if err != nil || meta.ID != "aaa001" {
		t.Fatalf("ResolveRunID running: meta=%+v err=%v", meta, err)
	}
	// Finished run must NOT resolve via exact match...
	if _, err := ResolveRunID(baseDir, "bbb002"); err == nil {
		t.Error("ResolveRunID should reject done runs (exact match)")
	}
	// ...nor via prefix match.
	if _, err := ResolveRunID(baseDir, "bbb"); err == nil {
		t.Error("ResolveRunID should reject done runs (prefix match)")
	}
}
