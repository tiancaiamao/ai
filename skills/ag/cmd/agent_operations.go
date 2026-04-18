package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/storage"
)

// Spawn creates an agent directory, writes config, and launches the bridge in tmux.
func Spawn(id, system, input, cwd string) error {
	agentDir := agent.AgentDir(id)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

	// Validate prerequisites
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is required but not found in PATH")
	}
	if _, err := exec.LookPath("ai"); err != nil {
		return fmt.Errorf("ai binary is required but not found in PATH")
	}

	// Write spawn config (meta.json)
	cfg := map[string]interface{}{
		"id":        id,
		"system":    system,
		"input":     input,
		"cwd":       cwd,
		"startedAt": time.Now().Unix(),
	}
	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "meta.json"), cfg); err != nil {
		return fmt.Errorf("write meta.json: %w", err)
	}

	// Launch bridge in tmux
	sessionName := "ag-" + id
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName,
		"ag", "bridge", id)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session failed: %w\n%s", err, out)
	}

	// Poll for bridge.sock ready (max 10s)
	sockPath := filepath.Join(agentDir, "bridge.sock")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if storage.Exists(sockPath) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Socket not ready — check if tmux session still exists
	if err := exec.Command("tmux", "has-session", "-t", sessionName).Run(); err != nil {
		// Session died — check stderr
		stderrData, _ := os.ReadFile(filepath.Join(agentDir, "bridge-stderr"))
		if len(stderrData) > 0 {
			return fmt.Errorf("bridge exited prematurely:\n%s", string(stderrData))
		}
		return fmt.Errorf("bridge exited prematurely (no stderr)")
	}

	return fmt.Errorf("bridge.sock not created within 10s (session is running)")
}