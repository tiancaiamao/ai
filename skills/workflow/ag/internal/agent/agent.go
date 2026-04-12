package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/genius/ag/internal/storage"
)

const (
	StatusSpawning = "spawning"
	StatusRunning  = "running"
	StatusDone     = "done"
	StatusFailed   = "failed"
	StatusKilled   = "killed"
)

type Meta struct {
	ID         string `json:"id"`
	System     string `json:"system,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Timeout    string `json:"timeout,omitempty"`
	Pid        int    `json:"pid,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	TmuxName   string `json:"tmuxName,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
	ExitCode   int    `json:"exitCode,omitempty"`
	Mock       bool   `json:"mock,omitempty"`
	MockScript string `json:"mockScript,omitempty"`
}

type SpawnConfig struct {
	ID         string
	System     string
	Input      string
	Mode       string
	Cwd        string
	Timeout    string
	Mock       bool
	MockScript string
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

func subagentBin() string {
	return filepath.Join(home(), ".ai", "skills", "subagent", "bin", "start_subagent_tmux.sh")
}

func Spawn(cfg SpawnConfig) (*Meta, error) {
	if cfg.ID == "" {
		return nil, fmt.Errorf("agent id is required")
	}
	if cfg.Mode == "" {
		cfg.Mode = "headless"
	}
	if cfg.Timeout == "" {
		cfg.Timeout = "10m"
	}

	agentDir := storage.AgentDir(cfg.ID)
	if storage.Exists(agentDir) {
		return nil, fmt.Errorf("agent already exists: %s", cfg.ID)
	}

	if err := os.MkdirAll(filepath.Join(agentDir, "inbox"), 0755); err != nil {
		return nil, fmt.Errorf("create agent dir: %w", err)
	}

	meta := &Meta{
		ID:         cfg.ID,
		System:     cfg.System,
		Mode:       cfg.Mode,
		Cwd:        cfg.Cwd,
		Timeout:    cfg.Timeout,
		StartedAt:  time.Now().Unix(),
		Mock:       cfg.Mock,
		MockScript: cfg.MockScript,
	}

	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "meta.json"), meta); err != nil {
		os.RemoveAll(agentDir)
		return nil, fmt.Errorf("write meta: %w", err)
	}

	if err := storage.WriteStatus(agentDir, StatusSpawning); err != nil {
		os.RemoveAll(agentDir)
		return nil, fmt.Errorf("write status: %w", err)
	}

	// Write input to inbox if provided
	if cfg.Input != "" {
		inputPath := filepath.Join(agentDir, "inbox", "001.msg")
		if _, err := os.Stat(cfg.Input); err == nil {
			data, err := os.ReadFile(cfg.Input)
			if err != nil {
				os.RemoveAll(agentDir)
				return nil, fmt.Errorf("read input file: %w", err)
			}
			if err := storage.WriteFile(inputPath, data); err != nil {
				os.RemoveAll(agentDir)
				return nil, fmt.Errorf("write input: %w", err)
			}
		} else {
			if err := storage.WriteFile(inputPath, []byte(cfg.Input)); err != nil {
				os.RemoveAll(agentDir)
				return nil, fmt.Errorf("write input: %w", err)
			}
		}
	}

	if cfg.Mock {
		return spawnMock(cfg, agentDir, meta)
	}
	return spawnReal(cfg, agentDir, meta)
}

// spawnMock runs a mock script synchronously (it's fast, no need for async).
func spawnMock(cfg SpawnConfig, agentDir string, meta *Meta) (*Meta, error) {
	outputFile := filepath.Join(agentDir, "output")
	inputFile := filepath.Join(agentDir, "inbox", "001.msg")

	script := cfg.MockScript
	if script == "" {
		script = "cat"
	}

	cmd := exec.Command("bash", "-c",
		fmt.Sprintf("%s '%s' > '%s' && touch '%s.done'", script, inputFile, outputFile, outputFile))
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		storage.WriteStatus(agentDir, StatusFailed)
		storage.WriteFile(filepath.Join(agentDir, "spawn-log"), out)
		return nil, fmt.Errorf("mock spawn failed: %w\n%s", err, out)
	}

	meta.TmuxName = "mock-" + cfg.ID
	meta.SessionID = "mock"
	meta.FinishedAt = time.Now().Unix()
	meta.ExitCode = 0

	storage.AtomicWriteJSON(filepath.Join(agentDir, "meta.json"), meta)
	storage.WriteStatus(agentDir, StatusDone)
	storage.WriteFile(filepath.Join(agentDir, "tmux-session"), []byte(meta.TmuxName))

	return meta, nil
}

// --- Real ---

func spawnReal(cfg SpawnConfig, agentDir string, meta *Meta) (*Meta, error) {
	taskDesc := buildTaskDesc(cfg, agentDir)

	systemArg := ""
	if cfg.System != "" {
		if strings.HasPrefix(cfg.System, "@") {
			systemArg = cfg.System
		} else {
			systemArg = "@" + cfg.System
		}
	}

	outputFile := filepath.Join(agentDir, "output")
	args := []string{outputFile, cfg.Timeout}
	if systemArg != "" {
		args = append(args, systemArg)
	} else {
		args = append(args, "-")
	}
	args = append(args, taskDesc)

	cmd := exec.Command(subagentBin(), args...)
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	if err != nil {
		storage.WriteStatus(agentDir, StatusFailed)
		storage.WriteFile(filepath.Join(agentDir, "spawn-log"), out)
		return nil, fmt.Errorf("spawn failed: %w\n%s", err, out)
	}

	result := strings.TrimSpace(string(out))
	parts := strings.SplitN(result, ":", 2)
	if len(parts) != 2 {
		storage.WriteStatus(agentDir, StatusFailed)
		return nil, fmt.Errorf("unexpected spawn output: %s", result)
	}

	meta.TmuxName = parts[0]
	meta.SessionID = parts[1]

	storage.AtomicWriteJSON(filepath.Join(agentDir, "meta.json"), meta)
	storage.WriteStatus(agentDir, StatusRunning)
	storage.WriteFile(filepath.Join(agentDir, "tmux-session"), []byte(meta.TmuxName))

	return meta, nil
}

func buildTaskDesc(cfg SpawnConfig, agentDir string) string {
	wd, _ := os.Getwd()
	if cfg.Cwd != "" {
		wd = cfg.Cwd
	}
	return fmt.Sprintf(
		"Working directory: %s\nAgent state dir: %s\nRead input from: %s/inbox/001.msg\nWrite output to: %s/output\nDone marker: touch %s/output.done\nTask: %s\n",
		wd, agentDir, agentDir, agentDir, agentDir, cfg.Input,
	)
}

// --- Wait ---

func Wait(id string, timeoutSec int) error {
	agentDir := storage.AgentDir(id)
	if !storage.Exists(agentDir) {
		return fmt.Errorf("agent not found: %s", id)
	}

	meta := &Meta{}
	storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)

	if meta.Mock {
		return waitByPolling(agentDir, meta, timeoutSec)
	}
	return waitViaTmux(agentDir, meta, timeoutSec)
}

func waitByPolling(agentDir string, meta *Meta, timeoutSec int) error {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		status := storage.ReadStatus(agentDir)
		if status == StatusDone {
			return nil
		}
		if status == StatusFailed {
			return fmt.Errorf("agent %s failed", meta.ID)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("agent %s timed out after %ds", meta.ID, timeoutSec)
}

func waitViaTmux(agentDir string, meta *Meta, timeoutSec int) error {
	sessionData, err := os.ReadFile(filepath.Join(agentDir, "tmux-session"))
	if err != nil {
		return fmt.Errorf("read tmux session: %w", err)
	}
	tmuxName := strings.TrimSpace(string(sessionData))
	outputFile := filepath.Join(agentDir, "output")

	bin := filepath.Join(home(), ".ai", "skills", "tmux", "bin", "tmux_wait.sh")
	cmd := exec.Command(bin, tmuxName, outputFile, fmt.Sprintf("%d", timeoutSec), "1", "0")
	cmd.Env = os.Environ()

	out, err := cmd.CombinedOutput()
	_ = out

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			storage.WriteStatus(agentDir, StatusFailed)
			return fmt.Errorf("agent %s failed (exit %d)", meta.ID, exitErr.ExitCode())
		}
		storage.WriteStatus(agentDir, StatusFailed)
		return fmt.Errorf("wait: %w", err)
	}

	storage.WriteStatus(agentDir, StatusDone)
	meta.FinishedAt = time.Now().Unix()
	meta.ExitCode = 0
	storage.AtomicWriteJSON(filepath.Join(agentDir, "meta.json"), meta)
	return nil
}

// --- Kill ---

func Kill(id string) error {
	agentDir := storage.AgentDir(id)
	if !storage.Exists(agentDir) {
		return fmt.Errorf("agent not found: %s", id)
	}

	meta := &Meta{}
	storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)

	if meta.Mock {
		if meta.Pid > 0 {
			if p, err := os.FindProcess(meta.Pid); err == nil {
				p.Kill()
			}
		}
	} else {
		sessionData, err := os.ReadFile(filepath.Join(agentDir, "tmux-session"))
		if err == nil {
			tmuxName := strings.TrimSpace(string(sessionData))
			exec.Command("tmux", "kill-session", "-t", tmuxName).Run()
		}
	}

	storage.WriteStatus(agentDir, StatusKilled)
	meta.FinishedAt = time.Now().Unix()
	storage.AtomicWriteJSON(filepath.Join(agentDir, "meta.json"), meta)
	return nil
}

// --- Output ---

func Output(id string) ([]byte, error) {
	agentDir := storage.AgentDir(id)
	if !storage.Exists(agentDir) {
		return nil, fmt.Errorf("agent not found: %s", id)
	}
	status := storage.ReadStatus(agentDir)
	if status != StatusDone {
		return nil, fmt.Errorf("agent %s is %s (not done)", id, status)
	}
	return os.ReadFile(filepath.Join(agentDir, "output"))
}

// --- Status ---

func Status(id string) (string, *Meta, error) {
	agentDir := storage.AgentDir(id)
	if !storage.Exists(agentDir) {
		return "", nil, fmt.Errorf("agent not found: %s", id)
	}
	status := storage.ReadStatus(agentDir)
	meta := &Meta{}
	_ = storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)
	return status, meta, nil
}

// --- List ---

func List() ([]struct {
	ID     string
	Status string
	Meta   *Meta
}, error) {
	agentsDir, _, _ := storage.Paths()
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	result := make([]struct {
		ID     string
		Status string
		Meta   *Meta
	}, 0)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		agentDir := filepath.Join(agentsDir, id)
		status := storage.ReadStatus(agentDir)
		meta := &Meta{}
		_ = storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)
		result = append(result, struct {
			ID     string
			Status string
			Meta   *Meta
		}{id, status, meta})
	}

	return result, nil
}