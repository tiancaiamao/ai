package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

			"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/conv"
	"github.com/genius/ag/internal/run"
	"github.com/genius/ag/internal/storage"
)

// BridgeResponse mirrors the response format used by CLI commands.
type BridgeResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

// BridgeCommand sends a command to the target agent.
func BridgeCommand(agentID, cmdType, message string) (*BridgeResponse, error) {
	if err := agent.EnsureExists(agentID); err != nil {
		return nil, err
	}

	if useAIAdapterForCommand(agentID) {
		return bridgeCommandWithAI(agentID, cmdType, message)
	}
	return bridgeCommandWithSocket(agentID, cmdType, message)
}

// bridgeCommandWithAI sends command through ai send.
func bridgeCommandWithAI(agentID, cmdType, message string) (*BridgeResponse, error) {
	var aiMessage string
	switch cmdType {
	case "steer", "prompt":
		aiMessage = message
	case "abort", "shutdown":
		aiMessage = "/abort"
	default:
		return nil, fmt.Errorf("unsupported command type: %s", cmdType)
	}

	if err := aiAdapter.SendCommand(agentID, cmdType, aiMessage); err != nil {
		return &BridgeResponse{OK: false, Error: err.Error()}, err
	}

	return &BridgeResponse{OK: true}, nil
}

// bridgeCommandWithSocket sends command through legacy bridge socket.
func bridgeCommandWithSocket(agentID, cmdType, message string) (*BridgeResponse, error) {
	agentDir := agent.AgentDir(agentID)
	sockPath := filepath.Join(agentDir, "bridge.sock")
	if !storage.Exists(sockPath) {
		return nil, fmt.Errorf("bridge socket not found (agent may not be running)")
	}

	req := map[string]string{
		"type":    cmdType,
		"message": message,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

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

	var buf []byte
	recvBuf := make([]byte, 4096)
	for {
		n, err := conn.Read(recvBuf)
		if n > 0 {
			buf = append(buf, recvBuf[:n]...)
			for i := len(buf) - n; i < len(buf); i++ {
				if buf[i] == '\n' {
					buf = buf[:i]
					var resp BridgeResponse
					if err := json.Unmarshal(buf, &resp); err != nil {
						return nil, fmt.Errorf("parse response: %w", err)
					}
					return &resp, nil
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}
	}
}

// Kill terminates an agent.
func Kill(id string) error {
	if useAIAdapterForCommand(id) {
		return killAIAgent(id)
	}
	return killLegacyAgent(id)
}

func killAIAgent(id string) error {
	meta, err := aiAdapter.GetStatus(id)
	if err == nil && meta.PID > 0 {
		// Kill the entire process group so child processes (e.g. ai serve,
		// codex exec) are also terminated, not just the direct child.
		_ = syscall.Kill(-meta.PID, syscall.SIGTERM)
	}
	return writeAIKilledActivity(id)
}

func writeAIKilledActivity(id string) error {
	agentDir := agent.AgentDir(id)
	actPath := filepath.Join(agentDir, "activity.json")
	var act agent.Activity
	if err := storage.ReadJSON(actPath, &act); err != nil {
		act = agent.Activity{Backend: "ai", StartedAt: time.Now().Unix()}
	}
	act.Status = "killed"
	if act.FinishedAt == 0 {
		act.FinishedAt = time.Now().Unix()
	}
	return storage.AtomicWriteJSON(actPath, act)
}

func killLegacyAgent(id string) error {
	agentDir := agent.AgentDir(id)
	actPath := filepath.Join(agentDir, "activity.json")
	sockPath := filepath.Join(agentDir, "bridge.sock")

	if storage.Exists(actPath) {
		var act agent.Activity
		if err := storage.ReadJSON(actPath, &act); err == nil {
			if agent.IsTerminal(act.Status) {
				if act.Status != "killed" {
					act.Status = "killed"
					if act.FinishedAt == 0 {
						act.FinishedAt = time.Now().Unix()
					}
					_ = storage.AtomicWriteJSON(actPath, act)
				}
				return nil
			}

			if act.Pid > 0 {
				proc, _ := os.FindProcess(act.Pid)
				if sigErr := proc.Signal(syscall.Signal(0)); sigErr == nil && storage.Exists(sockPath) {
					_ = syscall.Kill(-act.Pid, syscall.SIGTERM)
				}
			}
		}
	}

	if storage.Exists(actPath) {
		var act agent.Activity
		if err := storage.ReadJSON(actPath, &act); err == nil {
			act.Status = "killed"
			if act.FinishedAt == 0 {
				act.FinishedAt = time.Now().Unix()
			}
			_ = storage.AtomicWriteJSON(actPath, act)
		}
	}
	return nil
}

// Shutdown requests a graceful shutdown first, then falls back to Kill.
func Shutdown(id string) error {
	resp, err := BridgeCommand(id, "shutdown", "")
	if err != nil || !resp.OK {
		return Kill(id)
	}

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		act, readErr := GetAgentStatus(id)
		if readErr != nil || agent.IsTerminal(act.Status) {
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	return Kill(id)
}

// Rm removes agent files. Agent must be terminal.
func Rm(id string) error {
	agentDir := agent.AgentDir(id)
	actPath := filepath.Join(agentDir, "activity.json")
	if storage.Exists(actPath) {
		var act agent.Activity
		if err := storage.ReadJSON(actPath, &act); err == nil {
			if !agent.IsTerminal(act.Status) {
				return fmt.Errorf("agent %s is %s (must be done/failed/killed to remove, use --force)", id, act.Status)
			}
		}
	}

	// Clean up claim lock on the task with the same ID, so another agent
	// can re-claim it. Only remove the lock file, not the task directory.
	lockPath := filepath.Join(storage.BaseDir, "tasks", id, ".claim-lock")
	_ = os.Remove(lockPath)

	return os.RemoveAll(agentDir)
}

// Output returns the textual output of an agent.
func Output(id string, tailN int) (string, error) {
	if err := agent.EnsureExists(id); err != nil {
		return "", err
	}

	if useAIAdapterForCommand(id) {
		return outputFromAI(id, tailN)
	}
	return outputFromLegacy(id, tailN)
}

func outputFromLegacy(id string, tailN int) (string, error) {
	agentDir := agent.AgentDir(id)
	act, err := agent.ReadActivity(id)
	if err != nil {
		return "", fmt.Errorf("no activity for agent %s", id)
	}

	if agent.IsTerminal(act.Status) {
		outputPath := filepath.Join(agentDir, "output")
		data, err := os.ReadFile(outputPath)
		if err == nil {
			return tailBytes(data, tailN), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("read output: %w", err)
		}
	}

	streamPath := filepath.Join(agentDir, "stream.log")
	data, err := os.ReadFile(streamPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read stream.log: %w", err)
	}
	return tailBytes(data, tailN), nil
}

func outputFromAI(id string, tailN int) (string, error) {
	runID, err := aiAdapter.getRunIDForAgent(id)
	if err != nil {
		return "", fmt.Errorf("get run ID for agent %s: %w", id, err)
	}

		eventsPath, err := run.EventsPath(runID)
	if err != nil {
		return "", fmt.Errorf("get events path: %w", err)
	}
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read events.jsonl: %w", err)
	}

		messages := conv.BuildAssistantTexts(data)
	result := strings.Join(messages, "\n\n")

	if tailN > 0 {
		lines := strings.Split(strings.TrimSpace(result), "\n")
		if len(lines) > tailN {
			lines = lines[len(lines)-tailN:]
		}
		result = strings.Join(lines, "\n")
	}
	return result, nil
}




// Wait blocks until all specified agents reach a terminal state.
func Wait(ctx context.Context, ids []string, timeoutSec int) error {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	if timeoutSec == 0 {
		deadline = time.Now().Add(365 * 24 * time.Hour)
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		allDone := true
		for _, id := range ids {
			act, err := GetAgentStatus(id)
			if err != nil {
				return fmt.Errorf("agent %s: no activity", id)
			}
			if act.Backend != "ai" {
				DetectStale(id, act)
			}
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

// tailBytes returns the last tailN lines of data as a string.
func tailBytes(data []byte, tailN int) string {
	text := string(data)
	if tailN <= 0 || len(text) == 0 {
		return text
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > tailN {
		lines = lines[len(lines)-tailN:]
	}
	return strings.Join(lines, "\n")
}
