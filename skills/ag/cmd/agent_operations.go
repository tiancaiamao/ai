package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/backend"
	"github.com/genius/ag/internal/storage"
)

// Spawn creates an agent using ai's new infrastructure.
func Spawn(id, system, input, cwd, backendName string) error {
	if backendName == "ai" {
		// Start agent through ai serve adapter.
		return aiAdapter.SpawnWithAIServe(id, system, input, cwd)
	}

		return spawnWithRawBackend(id, system, input, cwd, backendName)
}

func spawnWithRawBackend(id, system, input, cwd, backendName string) error {
	backendsPath := backend.FindBackendsFile()
	backends, err := backend.LoadOrDefault(backendsPath)
	if err != nil {
		return fmt.Errorf("load backends: %w", err)
	}
	be, err := backends.Find(backendName)
	if err != nil {
		return fmt.Errorf("unknown backend %q: %w (available: %v)", backendName, err, backends.Names())
	}
	if be.Protocol != backend.ProtocolRaw {
		return fmt.Errorf("backend %q requires protocol %q but only 'ai' or raw backends are supported", backendName, be.Protocol)
	}

		agentDir := agent.AgentDir(id)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

		cmd := exec.Command(be.Command, be.Args...)
	if cwd != "" {
		cmd.Dir = cwd
	}

		// For codex backend, propagate full environment including any proxy settings.
	// Codex needs proxy to reach chatgpt.com in restricted networks.
	// Users should set HTTP_PROXY/HTTPS_PROXY in their shell environment.
	if backendName == "codex" {
		cmd.Env = os.Environ()
	}

		// For codex backend, pass input as additional argument instead of stdin
	// to avoid "file already closed" error when codex reads from stdin.
	// Also prepend system prompt if provided (codex has no --system flag).
	if backendName == "codex" {
		prompt := input
		if system != "" {
			prompt = fmt.Sprintf("[System Instructions]\n%s\n\n[Task]\n%s", system, input)
		}
		cmd.Args = append(cmd.Args, prompt)
	}

				// Create output file for direct write by the process.
	// Use os.File directly (not io.Writer) so exec.Cmd uses OS-level dup2
	// instead of goroutines — this survives Process.Release().
	outputPath := filepath.Join(agentDir, "output")
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}

	cmd.Stdout = outputFile
	cmd.Stderr = outputFile

	// Only set stdin for backends that need it (not codex)
	if backendName != "codex" {
		cmd.Stdin = strings.NewReader(input)
	}

		if err := cmd.Start(); err != nil {
		outputFile.Close()
		return fmt.Errorf("start backend %q: %w", backendName, err)
	}

	// Record PID before releasing the process.
	pid := cmd.Process.Pid
	act := agent.Activity{
		Status:    "running",
		Backend:   backendName,
		StartedAt: time.Now().Unix(),
		Pid:       pid,
	}
	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), act); err != nil {
		return fmt.Errorf("write activity.json: %w", err)
	}

	// Release the process so it survives after ag exits.
	// Status detection relies on isProcessAlive + scanning output for terminal events.
	if err := cmd.Process.Release(); err != nil {
		fmt.Printf("Warning: failed to release process: %v\n", err)
	}

	return nil
}

