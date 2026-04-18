package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/storage"
)

// BridgeResponse mirrors bridge.BridgeResponse for use in CLI commands.
type BridgeResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

// BridgeCommand sends a command to the agent's bridge Unix socket.
func BridgeCommand(agentID, cmdType, message string) (*BridgeResponse, error) {
	if err := agent.EnsureExists(agentID); err != nil {
		return nil, err
	}

	agentDir := agent.AgentDir(agentID)
	sockPath := filepath.Join(agentDir, "bridge.sock")

	if !storage.Exists(sockPath) {
		return nil, fmt.Errorf("bridge socket not found (agent may not be running)")
	}

	// Build request
	req := map[string]string{
		"type":    cmdType,
		"message": message,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	// Connect, send, receive, close (one-request-per-connection)
	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to bridge: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	if _, err := conn.Write(data); err != nil {
		return nil, fmt.Errorf("send command: %w", err)
	}

	// Read response until newline
	var buf []byte
	recvBuf := make([]byte, 4096)
	for {
		n, err := conn.Read(recvBuf)
		if n > 0 {
			buf = append(buf, recvBuf[:n]...)
			// Check if we got a complete JSON response
			for i := len(buf) - n; i < len(buf); i++ {
				if buf[i] == '\n' {
					buf = buf[:i] // trim newline
					goto done
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
	}
done:

	var resp BridgeResponse
	if err := json.Unmarshal(buf, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &resp, nil
}

// Kill kills the tmux session and updates activity.json to killed.
func Kill(id string) error {
	sessionName := "ag-" + id
	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	_ = cmd.Run() // ignore error if session doesn't exist

	// Update activity.json to killed
	agentDir := agent.AgentDir(id)
	actPath := filepath.Join(agentDir, "activity.json")
	if storage.Exists(actPath) {
		var act agent.Activity
		if err := storage.ReadJSON(actPath, &act); err == nil {
			act.Status = "killed"
			now := time.Now().Unix()
			if act.FinishedAt == 0 {
				act.FinishedAt = now
			}
			_ = storage.AtomicWriteJSON(actPath, act)
		}
	}
	return nil
}

// Shutdown sends a shutdown command via the bridge socket, then waits.
func Shutdown(id string) error {
	// Try graceful shutdown via socket
	_, err := BridgeCommand(id, "shutdown", "")
	if err != nil {
		// Socket not available, fall back to kill
		return Kill(id)
	}

	// Wait up to 30s for process to finish
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		act, readErr := agent.ReadActivity(id)
		if readErr != nil || agent.IsTerminal(act.Status) {
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	// Timed out, force kill
	return Kill(id)
}

// Rm removes agent files. Validates agent is in terminal state.
func Rm(id string) error {
	agentDir := agent.AgentDir(id)

	// Check status
	actPath := filepath.Join(agentDir, "activity.json")
	if storage.Exists(actPath) {
		var act agent.Activity
		if err := storage.ReadJSON(actPath, &act); err == nil {
			if !agent.IsTerminal(act.Status) {
				return fmt.Errorf("agent %s is %s (must be done/failed/killed to remove, use --force)", id, act.Status)
			}
		}
	}

	return os.RemoveAll(agentDir)
}

// Output returns the agent's accumulated output text.
// For terminal agents, returns full output. For running agents, returns error.
// tailN > 0 limits output to the last tailN bytes.
func Output(id string, tailN int) (string, error) {
	agentDir := agent.AgentDir(id)

	// Check if still running
	act, err := agent.ReadActivity(id)
	if err != nil {
		return "", fmt.Errorf("no activity for agent %s", id)
	}
	if !agent.IsTerminal(act.Status) {
		return "", fmt.Errorf("agent %s is still %s (wait for completion first)", id, act.Status)
	}

	outputPath := filepath.Join(agentDir, "output")
	data, err := os.ReadFile(outputPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // no output is valid
		}
		return "", fmt.Errorf("read output: %w", err)
	}

	text := string(data)
	if tailN > 0 && len(text) > 0 {
		// Trim trailing newline to avoid Split producing an empty last element
		text = strings.TrimRight(text, "\n")
		if text == "" {
			return "", nil
		}
		lines := strings.Split(text, "\n")
		if len(lines) > tailN {
			lines = lines[len(lines)-tailN:]
		}
		text = strings.Join(lines, "\n")
	}
	return text, nil
}

// Wait blocks until all specified agents reach terminal state.
// Respects context cancellation for clean SIGINT handling.
func Wait(ctx context.Context, ids []string, timeoutSec int) error {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	if timeoutSec == 0 {
		deadline = time.Now().Add(365 * 24 * time.Hour) // effectively infinite
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		allDone := true
		for _, id := range ids {
			act, err := agent.ReadActivity(id)
			if err != nil {
				return fmt.Errorf("agent %s: no activity", id)
			}
			DetectStale(id, act)
			if !agent.IsTerminal(act.Status) {
				allDone = false
			}
		}
		if allDone {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout after %ds", timeoutSec)
			}
		}
	}
}

// DetectStale checks if a running agent's tmux session and PID are still alive.
// Updates activity.json to "failed" if stale.
func DetectStale(id string, act *agent.Activity) {
	if act.Status != "running" {
		return
	}

	stale := false
	reason := ""

	// Check 1: tmux session exists
	sessionName := "ag-" + id
	tmuxAlive := exec.Command("tmux", "has-session", "-t", sessionName).Run() == nil
	if !tmuxAlive {
		stale = true
		reason = "tmux session not found"
	}

	// Check 2: PID is still alive (if tmux is alive but process died)
	if !stale && act.Pid > 0 {
		proc, err := os.FindProcess(act.Pid)
		if err != nil {
			stale = true
			reason = "process not found"
		} else {
			// Signal 0 checks if process exists without killing it
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				stale = true
				reason = "process no longer alive"
			}
		}
	}

	if stale {
		act.Status = "failed"
		if act.Error == "" {
			act.Error = reason
		}
		now := time.Now().Unix()
		if act.FinishedAt == 0 {
			act.FinishedAt = now
		}
		agentDir := agent.AgentDir(id)
		_ = storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), act)
	}
}