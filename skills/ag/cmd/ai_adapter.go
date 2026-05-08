package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/genius/ag/internal/run"
	"github.com/genius/ag/internal/storage"
)

// AIAdapter wraps calls to the ai serve infrastructure.
type AIAdapter struct {
	// agentID to runID mapping cache
	mappings map[string]string
	mu       sync.RWMutex
}

// NewAIAdapter creates a new AIAdapter instance.
func NewAIAdapter() *AIAdapter {
	return &AIAdapter{
		mappings: make(map[string]string),
	}
}

// SpawnWithAIServe starts a new agent using the ai serve command.
func (a *AIAdapter) SpawnWithAIServe(id, system, input, cwd string) error {
	// Create ag agent directory (for backward compat and storage mappings)
	agentDir := storage.AgentDir(id)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

	// Write initial activity.json
	activity := map[string]interface{}{
		"status":    "running",
		"backend":   "ai",
		"startedAt": time.Now().Unix(),
	}
	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), activity); err != nil {
		return fmt.Errorf("write activity.json: %w", err)
	}

	// Build ai serve command to run in background
	cmd := exec.Command("ai", "serve")

	// Add arguments
	if system != "" {
		cmd.Args = append(cmd.Args, "--system-prompt", system)
	}
	if input != "" {
		cmd.Args = append(cmd.Args, "--input", input)
	}
	if cwd != "" {
		cmd.Dir = cwd
	}

	// Set agent name for identification
	cmd.Args = append(cmd.Args, "--name", "ag-agent-"+id)

	// Use stdout pipe to read run ID, discard stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	cmd.Stderr = nil // discard stderr

	// Create process group so child processes can be killed together.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ai serve failed: %w", err)
	}

	// Read run ID (ai serve outputs run ID then keeps running).
	// Use ReadString to read only the first line, don't let subsequent output block.
	reader := bufio.NewReader(stdout)
	firstLine, _ := reader.ReadString('\n')
	stdout.Close() // Close pipe so ai serve won't block if it writes >64KB to stdout
	var runID string
	if firstLine != "" {
		runID = strings.TrimSpace(firstLine)
	}
	if runID == "" {
		// ai serve returned no run ID, try ai ls
		fmt.Println("ai serve returned no run ID, trying ai ls...")
		runID, err = a.getLatestRunID()
		if err != nil {
			return fmt.Errorf("ai serve returned no run ID and ai ls failed: %w", err)
		}
		fmt.Printf("Found run ID from ai ls: %s\n", runID)
	}

	// Save PID (Release zeroes the Pid field)
	pid := cmd.Process.Pid

	// Detach ai serve process from parent so spawn doesn't block
	if err := cmd.Process.Release(); err != nil {
		// Non-fatal, process is already started
		fmt.Fprintf(os.Stderr, "warning: could not release ai serve process: %v\n", err)
	}

	// Save PID to activity
	activity["pid"] = pid
	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), activity); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update activity.json with PID: %v\n", err)
	}

	// Save mapping
	if err := a.saveAgentRunMapping(id, runID); err != nil {
		return fmt.Errorf("save mapping: %w", err)
	}

	fmt.Printf("Agent %s started (run ID: %s, PID: %d)\n", id, runID, pid)
	return nil
}

// getLatestRunID uses ai ls to find the latest run ID.
func (a *AIAdapter) getLatestRunID() (string, error) {
	cmd := exec.Command("ai", "ls", "--json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ai ls failed: %w", err)
	}

	// Parse JSON output
	var runs []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(output, &runs); err != nil {
		return "", fmt.Errorf("parse ai ls output: %w", err)
	}

	// Find the latest running ag-agent
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Status == "running" && strings.HasPrefix(runs[i].Name, "ag-agent-") {
			return runs[i].ID, nil
		}
	}

	return "", fmt.Errorf("no running ag-agent found")
}

// SendCommand sends a command to the specified agent.
func (a *AIAdapter) SendCommand(agentID, cmdType, message string) error {
	// Get corresponding run ID
	runID, err := a.getRunIDForAgent(agentID)
	if err != nil {
		return err
	}

	// Build ai send command
	args := []string{"send", "--id", runID}
	if message != "" {
		args = append(args, message)
	}

	cmd := exec.Command("ai", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ai send failed: %s (output: %s)", err, string(output))
	}

	return nil
}

// GetStatus returns the agent's run status.
func (a *AIAdapter) GetStatus(agentID string) (*run.RunMeta, error) {
	// Get corresponding run ID
	runID, err := a.getRunIDForAgent(agentID)
	if err != nil {
		return nil, err
	}

	meta, err := run.ReadMeta(runID)
	if err != nil {
		return nil, fmt.Errorf("read run meta: %w", err)
	}

	return meta, nil
}

// Wait blocks until the agent completes.
func (a *AIAdapter) Wait(agentID string) error {
	// Get corresponding run ID
	_, err := a.getRunIDForAgent(agentID)
	if err != nil {
		return err
	}

	// Poll status via ai ls
	for i := 0; i < 3600; i++ { // max 1 hour wait
		status, err := a.GetStatus(agentID)
		if err != nil {
			// If file doesn't exist, it may have already completed
			continue
		}

		if status.Status != "running" {
			return nil
		}

		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("timeout waiting for agent %s to complete", agentID)
}

// Private methods

func (a *AIAdapter) saveAgentRunMapping(agentID, runID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Save to memory
	a.mappings[agentID] = runID

	// Save to file
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	agDir := filepath.Join(homeDir, ".ag", "agents")
	if err := os.MkdirAll(agDir, 0755); err != nil {
		return fmt.Errorf("create ag dir: %w", err)
	}

	mappingsFile := filepath.Join(agDir, "run_mappings.json")

	// Read existing mappings
	var mappings map[string]string
	data, err := os.ReadFile(mappingsFile)
	if err == nil {
		json.Unmarshal(data, &mappings)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read mappings file: %w", err)
	}

	// Ensure map exists
	if mappings == nil {
		mappings = make(map[string]string)
	}

	// Update mapping
	mappings[agentID] = runID

	// Write back to file
	data, err = json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mappings: %w", err)
	}

	if err := os.WriteFile(mappingsFile, data, 0644); err != nil {
		return fmt.Errorf("write mappings file: %w", err)
	}

	fmt.Printf("Saved mapping: %s -> %s to %s\n", agentID, runID, mappingsFile)
	return nil
}

func (a *AIAdapter) getRunIDForAgent(agentID string) (string, error) {
	a.mu.RLock()
	runID, exists := a.mappings[agentID]
	a.mu.RUnlock()

	if exists {
		return runID, nil
	}

	// Read from file
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	mappingsFile := filepath.Join(homeDir, ".ag", "agents", "run_mappings.json")
	data, err := os.ReadFile(mappingsFile)
	if err != nil {
		return "", fmt.Errorf("read run mappings: %w", err)
	}

	var mappings map[string]string
	if err := json.Unmarshal(data, &mappings); err != nil {
		return "", fmt.Errorf("parse run mappings: %w", err)
	}

	runID, exists = mappings[agentID]
	if !exists {
		return "", fmt.Errorf("no run ID found for agent %s", agentID)
	}

	// Cache in memory
	a.mu.Lock()
	a.mappings[agentID] = runID
	a.mu.Unlock()

	return runID, nil
}

// Global adapter instance
var aiAdapter = NewAIAdapter()