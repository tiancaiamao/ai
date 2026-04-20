package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/storage"
)

// Spawn creates an agent directory, writes config, and launches the bridge
// as a detached background process (no tmux dependency).
func Spawn(id, system, input, cwd string) error {
	agentDir := agent.AgentDir(id)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

	// Validate prerequisites
	if _, err := exec.LookPath("ai"); err != nil {
		return fmt.Errorf("ai binary is required but not found in PATH")
	}

	// Find ag binary path for spawning bridge
	agBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve ag binary: %w", err)
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

	// Write initial activity.json so status/ls work immediately
	activity := map[string]any{
		"status":    "starting",
		"startedAt": cfg["startedAt"],
	}
	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), activity); err != nil {
		return fmt.Errorf("write activity.json: %w", err)
	}

	// Launch bridge as detached background process
	cmd := exec.Command(agBin, "bridge", id)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start bridge process: %w", err)
	}

	// Detach immediately — bridge becomes orphan, reparented to init/launchd
	// We do NOT wait for it.

	// Poll for bridge.sock ready (max 10s)
	sockPath := filepath.Join(agentDir, "bridge.sock")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if storage.Exists(sockPath) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Socket not ready — check if process is still alive
	if cmd.Process != nil {
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			// Process died — check stderr
			stderrData, _ := os.ReadFile(filepath.Join(agentDir, "bridge-stderr"))
			if len(stderrData) > 0 {
				return fmt.Errorf("bridge exited prematurely:\n%s", string(stderrData))
			}
			return fmt.Errorf("bridge exited prematurely (no stderr)")
		}
	}

	return fmt.Errorf("bridge.sock not created within 10s (process is running)")
}