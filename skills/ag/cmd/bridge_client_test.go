package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/storage"
)

func TestOutput_TailTrailingNewline(t *testing.T) {
	// Setup: chdir to temp dir so .ag/ goes there
	origDir, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(origDir)
	storage.SetBaseDir(".ag")
	storage.Init()

	// Create a fake agent with "done" status
	id := "test-tail"
	agent.EnsureExists(id)
	agentDir := storage.AgentDir(id)

	// Write activity.json with done status
	act := map[string]any{
		"status":     "done",
		"finishedAt": 1234567890,
	}
	storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), act)

	// Write output file with trailing newline
	// "line1\nline2\n" should yield ["line1", "line2", ""]
	// With --tail 1, should return "line2" not ""
	os.WriteFile(filepath.Join(agentDir, "output"), []byte("line1\nline2\n"), 0644)

	// Test --tail 1
	result, err := Output(id, 1)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if result != "line2" {
		t.Fatalf("expected 'line2', got %q", result)
	}

	// Test --tail 2
	result, err = Output(id, 2)
	if err != nil {
		t.Fatalf("Output tail=2: %v", err)
	}
	if result != "line1\nline2" {
		t.Fatalf("expected 'line1\\nline2', got %q", result)
	}

	// Test --tail 0 (no truncation)
	result, err = Output(id, 0)
	if err != nil {
		t.Fatalf("Output tail=0: %v", err)
	}
	if result != "line1\nline2\n" {
		t.Fatalf("expected full output, got %q", result)
	}
}