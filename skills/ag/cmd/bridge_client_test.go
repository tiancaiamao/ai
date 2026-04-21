package cmd

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestShutdown_FallbackToKillWhenBridgeRejectsShutdown(t *testing.T) {
	origDir, _ := os.Getwd()
	os.Chdir(t.TempDir())
	defer os.Chdir(origDir)
	storage.SetBaseDir(".ag")
	if err := storage.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	id := "test-shutdown-fallback"
	agentDir := storage.AgentDir(id)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	act := map[string]any{
		"status":    "running",
		"startedAt": time.Now().Unix(),
		"pid":       0, // avoid sending signals in test
	}
	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), act); err != nil {
		t.Fatalf("write activity: %v", err)
	}

	sockPath := filepath.Join(agentDir, "bridge.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		_, _ = conn.Read(buf) // request payload is not validated in this test

		resp, _ := json.Marshal(map[string]any{
			"ok":    false,
			"error": "backend does not support shutdown",
		})
		_, _ = conn.Write(append(resp, '\n'))
	}()

	start := time.Now()
	if err := Shutdown(id); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("shutdown fallback was too slow: %v", elapsed)
	}

	<-done

	var got agent.Activity
	if err := storage.ReadJSON(filepath.Join(agentDir, "activity.json"), &got); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if got.Status != "killed" {
		t.Fatalf("status=%q, want killed", got.Status)
	}
	if got.FinishedAt == 0 {
		t.Fatal("expected finishedAt to be set")
	}
}
