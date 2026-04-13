package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	ID        string `json:"id"`
	System    string `json:"system,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Timeout   string `json:"timeout,omitempty"`
	Pid       int    `json:"pid,omitempty"`
	StartedAt int64  `json:"startedAt"`

	// Computed at finish time
	FinishedAt int64 `json:"finishedAt,omitempty"`
	ExitCode   int   `json:"exitCode,omitempty"`

	// Mock mode
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

// --- Spawn ---

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
		ID:        cfg.ID,
		System:    cfg.System,
		Mode:      cfg.Mode,
		Cwd:       cfg.Cwd,
		Timeout:   cfg.Timeout,
		StartedAt: time.Now().Unix(),
		Mock:      cfg.Mock,
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

// spawnMock runs a mock script synchronously.
func spawnMock(cfg SpawnConfig, agentDir string, meta *Meta) (*Meta, error) {
	outputFile := filepath.Join(agentDir, "output")
	inputFile := filepath.Join(agentDir, "inbox", "001.msg")

	script := cfg.MockScript
	if script == "" {
		script = "cat"
	}

	cmd := exec.Command(script, inputFile)
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}

	outFile, err := os.Create(outputFile)
	if err != nil {
		return nil, fmt.Errorf("create output file: %w", err)
	}
	cmd.Stdout = outFile

	err = cmd.Run()
	outFile.Close()
	if err != nil {
		storage.WriteStatus(agentDir, StatusFailed)
		return nil, fmt.Errorf("mock spawn failed: %w", err)
	}

	meta.FinishedAt = time.Now().Unix()
	meta.ExitCode = 0

	storage.AtomicWriteJSON(filepath.Join(agentDir, "meta.json"), meta)
	storage.WriteStatus(agentDir, StatusDone)

	return meta, nil
}

// spawnReal starts ai --mode headless as a direct child process.
// No tmux, no shell wrappers — just exec.Command with process group isolation.
// spawnReal starts ai --mode headless as a detached process.
//
// Since ag is a CLI (each command is a separate process), we can't rely on
// goroutines surviving after spawn returns. Instead, we write a watcher shell
// script that:
//  1. Runs ai --mode headless (the actual agent)
//  2. Captures output to agentDir/output
//  3. Updates status + meta.json when it finishes
//
// The watcher runs in its own process group (Setpgid) and is released from
// ag's process tree (Process.Release). It survives ag's exit.
func spawnReal(cfg SpawnConfig, agentDir string, meta *Meta) (*Meta, error) {
	// Resolve system prompt
	systemPromptArg := ""
	if cfg.System != "" {
		systemPromptArg = resolveSystemPrompt(cfg.System, agentDir)
	}

	// Resolve input
	taskArg := cfg.Input
	if strings.HasPrefix(cfg.Input, "@") {
		taskArg = strings.TrimPrefix(cfg.Input, "@")
		if !storage.Exists(taskArg) {
			taskArg = cfg.Input
		}
	}

	// Build ai command args
	aiBin, err := exec.LookPath("ai")
	if err != nil {
		return nil, fmt.Errorf("ai binary not found in PATH: %w", err)
	}

	aiArgs := []string{"--mode", "headless", "--timeout", cfg.Timeout}
	if systemPromptArg != "" {
		aiArgs = append(aiArgs, "--system-prompt", systemPromptArg)
	}
	aiArgs = append(aiArgs, taskArg)

	// Write watcher script
	outputPath := filepath.Join(agentDir, "output")
	statusPath := filepath.Join(agentDir, "status")
	metaPath := filepath.Join(agentDir, "meta.json")

	// Escape arguments for shell
	aiCmdShell := shellQuote(aiBin)
	for _, a := range aiArgs {
		aiCmdShell += " " + shellQuote(a)
	}

	cwd := cfg.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	watcherContent := "#!/bin/bash\n" +
		"set -o pipefail\n" +
		fmt.Sprintf("cd %s 2>/dev/null || true\n", shellQuote(cwd)) +
		fmt.Sprintf("%s 2>&1 | tee %s\n", aiCmdShell, shellQuote(outputPath)) +
		"EXIT_CODE=$?\n" +
		fmt.Sprintf("if [ $EXIT_CODE -eq 0 ]; then\n  echo 'done' > %s\nelse\n", shellQuote(statusPath)) +
		fmt.Sprintf("  if [ -s %s ]; then\n    echo 'done' > %s\n  else\n    echo 'failed' > %s\n  fi\nfi\n",
			shellQuote(outputPath), shellQuote(statusPath), shellQuote(statusPath)) +
		fmt.Sprintf("FINISHED=$(date +%%s)\n") +
		fmt.Sprintf("python3 -c \"import json; f=open('%s'); m=json.load(f); f.close(); ", metaPath) +
		"m['finishedAt']=int('$FINISHED'); m['exitCode']=$EXIT_CODE; " +
		fmt.Sprintf("f=open('%s','w'); json.dump(m,f,indent=2); f.close()\" 2>/dev/null || true\n", metaPath)

	watcherScript := filepath.Join(agentDir, "watcher.sh")
	if err := os.WriteFile(watcherScript, []byte(watcherContent), 0755); err != nil {
		return nil, fmt.Errorf("write watcher script: %w", err)
	}

	// Start watcher in background, detached from ag process
	cmd := exec.Command(watcherScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start watcher: %w", err)
	}

	meta.Pid = cmd.Process.Pid
	storage.AtomicWriteJSON(filepath.Join(agentDir, "meta.json"), meta)
	storage.WriteStatus(agentDir, StatusRunning)

	// Release the child — it survives ag's exit
	cmd.Process.Release()

	return meta, nil
}

// shellQuote wraps a string in single quotes, escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
func resolveSystemPrompt(system string, agentDir string) string {
	if strings.HasPrefix(system, "@") {
		filePath := strings.TrimPrefix(system, "@")
		if storage.Exists(filePath) {
			return system // already has @ prefix, file exists — pass through
		}
		// @ prefix but file doesn't exist — treat as inline content
		system = system[1:]
	}

	// Inline content — write to file, return @path
	tmpFile := filepath.Join(agentDir, "system-prompt.txt")
	if err := os.WriteFile(tmpFile, []byte(system), 0644); err != nil {
		return system // fallback: pass inline (will likely fail)
	}
	return "@" + tmpFile
}

// Wait blocks until the agent reaches a terminal state (done/failed/killed)
// or times out.
//
// Detection strategy (no tmux dependency):
//  1. Poll status file every 2s — the background goroutine in spawnReal
//     writes the status when the process exits
//  2. Cross-check: if status says "running" but the PID is gone (process
//     died without our goroutine catching it), mark as failed
//  3. Timeout safety net
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
	return waitByPolling(agentDir, meta, timeoutSec)
}

func waitByPolling(agentDir string, meta *Meta, timeoutSec int) error {
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	checkInterval := 2 * time.Second

	for time.Now().Before(deadline) {
		status := storage.ReadStatus(agentDir)

		switch status {
		case StatusDone:
			return nil
		case StatusFailed:
			return fmt.Errorf("agent %s failed (exit code %d)", meta.ID, loadExitCode(agentDir))
		case StatusKilled:
			return fmt.Errorf("agent %s was killed", meta.ID)
		}

		// Cross-check: PID alive but status still says running?
		// When the process exits, the background goroutine will update status.
		// We just accelerate polling when PID is gone.
		if meta.Pid > 0 && status == StatusRunning {
			if !pidAlive(meta.Pid) {
				// Process is gone. Switch to fast polling until goroutine catches up.
				fastDeadline := time.Now().Add(15 * time.Second)
				for time.Now().Before(fastDeadline) {
					time.Sleep(200 * time.Millisecond)
					s := storage.ReadStatus(agentDir)
					switch s {
					case StatusDone:
						return nil
					case StatusFailed:
						return fmt.Errorf("agent %s failed (exit code %d)", meta.ID, loadExitCode(agentDir))
					case StatusKilled:
						return fmt.Errorf("agent %s was killed", meta.ID)
					}
				}
				// Goroutine never caught up (shouldn't happen in practice)
				return fmt.Errorf("agent %s (pid %d): status not updated after process exit", meta.ID, meta.Pid)
			}
		}

		time.Sleep(checkInterval)
	}

	return fmt.Errorf("agent %s timed out after %ds", meta.ID, timeoutSec)
}

// pidAlive checks if a process is still running.
// Uses os.FindProcess + signal 0 (standard POSIX check).
func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 doesn't kill the process — just checks existence.
	// On Unix, this returns nil if the process exists.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// forceFail sets the agent to failed state when we detect it died
// but the goroutine hasn't updated status yet.
func forceFail(agentDir string, meta *Meta) {
	meta.FinishedAt = time.Now().Unix()
	meta.ExitCode = -1
	storage.AtomicWriteJSON(filepath.Join(agentDir, "meta.json"), meta)
	storage.WriteStatus(agentDir, StatusFailed)
}

func loadExitCode(agentDir string) int {
	meta := &Meta{}
	storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)
	return meta.ExitCode
}

// --- Kill ---

func Kill(id string) error {
	agentDir := storage.AgentDir(id)
	if !storage.Exists(agentDir) {
		return fmt.Errorf("agent not found: %s", id)
	}

	status := storage.ReadStatus(agentDir)
	if status != StatusRunning && status != StatusSpawning {
		return fmt.Errorf("agent %s is %s (not running)", id, status)
	}

	meta := &Meta{}
	storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)

	if meta.Pid > 0 {
		// Kill the entire process group (negative PID)
		syscall.Kill(-meta.Pid, syscall.SIGTERM)
		// Give it a moment, then SIGKILL if still alive
		time.Sleep(500 * time.Millisecond)
		if pidAlive(meta.Pid) {
			syscall.Kill(-meta.Pid, syscall.SIGKILL)
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

	// Live uptime calculation for running agents
	if status == StatusRunning && meta.Pid > 0 && !pidAlive(meta.Pid) {
		// Stale status — process is dead but goroutine hasn't updated
		// Return "running" but the caller can detect via status refresh
	}

	return status, meta, nil
}

// --- List ---

type AgentEntry struct {
	ID     string
	Status string
	Meta   *Meta
}

func List() ([]AgentEntry, error) {
	agentsDir, _, _ := storage.Paths()
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	result := make([]AgentEntry, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		agentDir := filepath.Join(agentsDir, id)
		status := storage.ReadStatus(agentDir)
		meta := &Meta{}
		_ = storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)
		result = append(result, AgentEntry{id, status, meta})
	}

	return result, nil
}

// ringBuffer is a fixed-size ring buffer that keeps the last N bytes written.
// Used to capture the tail of agent output for diagnostics.



// Bytes returns the content of the ring buffer in order (oldest first).


func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}