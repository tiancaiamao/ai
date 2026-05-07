//go:build integration

package adapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// skipIfNoAIBinary skips the test if the `ai` binary is not in PATH.
func skipIfNoAIBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ai"); err != nil {
		t.Skip("ai binary not found in PATH; skipping integration test")
	}
}

func TestIntegrationConnManagerPrompt(t *testing.T) {
	skipIfNoAIBinary(t)

	// Create temp sessions directory.
	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cm := NewConnManager(sessionsDir, "You are a helpful test assistant. Reply with exactly: PONG")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sessionKey := "test-integration"

	// Send a prompt — this will start the ai subprocess.
	response, err := cm.Prompt(ctx, sessionKey, "Reply with exactly: PONG")
	if err != nil {
		t.Fatalf("ConnManager.Prompt failed: %v", err)
	}

	t.Logf("Response: %s", response)

	// Response should contain PONG.
	if response == "" {
		t.Fatal("Expected non-empty response from ai subprocess")
	}

	// Verify the connection is alive.
	conns := cm.ListConnections()
	if len(conns) != 1 {
		t.Fatalf("Expected 1 connection, got %d", len(conns))
	}
	if conns[0] != sessionKey {
		t.Fatalf("Expected connection key %q, got %q", sessionKey, conns[0])
	}

	// Clean up.
	if err := cm.CloseAll(); err != nil {
		t.Logf("Warning: CloseAll error: %v", err)
	}
}

func TestIntegrationConnManagerMultipleSessions(t *testing.T) {
	skipIfNoAIBinary(t)

	tmpDir := t.TempDir()
	sessionsDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cm := NewConnManager(sessionsDir, "You are a helpful test assistant. Reply briefly.")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Send prompts to two different sessions.
	_, err := cm.Prompt(ctx, "session-alpha", "Say hello")
	if err != nil {
		t.Fatalf("Prompt session-alpha failed: %v", err)
	}

	_, err = cm.Prompt(ctx, "session-beta", "Say goodbye")
	if err != nil {
		t.Fatalf("Prompt session-beta failed: %v", err)
	}

	conns := cm.ListConnections()
	if len(conns) != 2 {
		t.Fatalf("Expected 2 connections, got %d: %v", len(conns), conns)
	}

	if err := cm.CloseAll(); err != nil {
		t.Logf("Warning: CloseAll error: %v", err)
	}
}