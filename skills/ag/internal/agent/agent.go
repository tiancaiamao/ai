package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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

	switch cfg.Mode {
	case "rpc":
		return spawnRPC(cfg, agentDir, meta)
	default:
		return spawnHeadless(cfg, agentDir, meta)
	}
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

// --- RPC Mode ---

// rpcConn holds the pipes for communicating with an RPC-mode agent.
type rpcConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

// spawnRPC starts ai --mode rpc as a detached process with named pipes for IPC.
// The agent reads RPC commands from <agentDir>/rpc_stdin and writes events to <agentDir>/rpc_stdout.
// A watcher process manages the lifecycle: sends the initial prompt, collects output, and updates status.
func spawnRPC(cfg SpawnConfig, agentDir string, meta *Meta) (*Meta, error) {
	aiBin, err := exec.LookPath("ai")
	if err != nil {
		return nil, fmt.Errorf("ai binary not found in PATH: %w", err)
	}

	cwd := cfg.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	// Resolve input text
	inputText := cfg.Input
	if strings.HasPrefix(cfg.Input, "@") {
		path := strings.TrimPrefix(cfg.Input, "@")
		if data, err := os.ReadFile(path); err == nil {
			inputText = string(data)
		}
	}

	// Build watcher script that:
	// 1. Starts ai --mode rpc (reads from fifo, writes to fifo)
	// 2. Feeds the initial prompt via RPC
	// 3. Streams events to output file
	// 4. Updates status on completion
	outputPath := filepath.Join(agentDir, "output")
	statusPath := filepath.Join(agentDir, "status")
	metaPath := filepath.Join(agentDir, "meta.json")
	eventsPath := filepath.Join(agentDir, "events.jsonl")

	aiArgs := []string{aiBin, "--mode", "rpc", "--timeout", cfg.Timeout}
	if cfg.System != "" {
		systemPromptPath := resolveSystemPrompt(cfg.System, agentDir)
		aiArgs = append(aiArgs, "--system-prompt", systemPromptPath)
	}

	watcherContent := buildRPCWatcherScript(watcherScriptConfig{
		aiArgs:      aiArgs,
		cwd:         cwd,
		outputPath:  outputPath,
		statusPath:  statusPath,
		metaPath:    metaPath,
		eventsPath:  eventsPath,
		inputText:   inputText,
		agentDir:    agentDir,
		timeout:     cfg.Timeout,
	})

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

type watcherScriptConfig struct {
	aiArgs     []string
	cwd        string
	outputPath string
	statusPath string
	metaPath   string
	eventsPath string
	inputText  string
	agentDir   string
	timeout    string
}

// buildRPCWatcherScript creates a bash script that:
// 1. Creates named pipes (FIFOs) for RPC communication
// 2. Starts ai --mode rpc with stdin/stdout connected to the FIFOs
// 3. Feeds the initial prompt via RPC JSON command
// 4. Reads events from the agent and writes to events.jsonl + output
// 5. Updates status when agent finishes
func buildRPCWatcherScript(cfg watcherScriptConfig) string {
	// Build the ai command with shell quoting
	aiCmdShell := ""
	for _, a := range cfg.aiArgs {
		aiCmdShell += " " + shellQuote(a)
	}

	// Escape input text for JSON embedding
	escapedInput := strings.ReplaceAll(cfg.inputText, `\`, `\\`)
	escapedInput = strings.ReplaceAll(escapedInput, `"`, `\"`)
	escapedInput = strings.ReplaceAll(escapedInput, "\n", `\n`)
	escapedInput = strings.ReplaceAll(escapedInput, "\t", `\t`)
	escapedInput = strings.ReplaceAll(escapedInput, "$", `\$`)

	// We use a Python3 helper to do the RPC communication because bash alone
	// can't reliably handle JSON over FIFOs with the ai process.
	// The python script:
	// 1. Starts ai --mode rpc as a subprocess
	// 2. Sends the prompt command
	// 3. Reads events until agent_end
	// 4. Collects assistant text as output
	// 5. Writes events to events.jsonl
	pythonScript := filepath.Join(cfg.agentDir, "rpc_bridge.py")

	pyContent := `#!/usr/bin/env python3
"""RPC bridge: manages ai --mode rpc subprocess lifecycle."""
import json, subprocess, sys, os, signal, time

def main():
    ai_args = json.loads(os.environ.get('AI_ARGS', '[]'))
    input_text = os.environ.get('AI_INPUT', '')
    output_path = os.environ.get('AI_OUTPUT', '/dev/null')
    events_path = os.environ.get('AI_EVENTS', '/dev/null')
    status_path = os.environ.get('AI_STATUS', '')
    meta_path = os.environ.get('AI_META', '')
    cwd = os.environ.get('AI_CWD', '.')
    timeout_str = os.environ.get('AI_TIMEOUT', '10m')

    # Parse timeout
    timeout_secs = 600
    if timeout_str.endswith('m'):
        timeout_secs = int(timeout_str[:-1]) * 60
    elif timeout_str.endswith('s'):
        timeout_secs = int(timeout_str[:-1])
    elif timeout_str.endswith('h'):
        timeout_secs = int(timeout_str[:-1]) * 3600

    # Start ai --mode rpc
    proc = subprocess.Popen(
        ai_args,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=cwd,
        start_new_session=True,
    )

    cmd_id = 1

    def send_cmd(cmd_type, data=None):
        nonlocal cmd_id
        cmd = {"id": str(cmd_id), "type": cmd_type}
        if data:
            cmd["data"] = data
        cmd_id += 1
        line = json.dumps(cmd) + "\n"
        try:
            proc.stdin.write(line.encode())
            proc.stdin.flush()
            return True
        except (BrokenPipeError, OSError):
            return False

    def read_events(collector, stop_on_agent_end=True):
        """Read events from ai's stdout until agent_end or EOF."""
        assistant_parts = []
        try:
            for line in proc.stdout:
                line = line.decode('utf-8', errors='replace').strip()
                if not line:
                    continue
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    continue

                event_type = event.get("type", "")

                # Collect assistant text from message_update (streaming)
                if event_type == "message_update":
                    sub = event.get("assistantMessageEvent", {})
                    if isinstance(sub, dict) and sub.get("type") == "text_delta":
                        text = sub.get("delta", "")
                        if text:
                            assistant_parts.append(text)
                # Collect full message from message_end
                elif event_type == "message_end" and event.get("message"):
                    msg = event["message"]
                    if msg.get("role") == "assistant":
                        content = msg.get("content", "")
                        if isinstance(content, str) and content:
                            collector["output"] = content
                        elif isinstance(content, list):
                            parts = []
                            for block in content:
                                if isinstance(block, dict) and block.get("type") == "text":
                                    parts.append(block.get("text", ""))
                            collector["output"] = "".join(parts)

                # Write event to events.jsonl
                try:
                    with open(events_path, "a") as f:
                        f.write(json.dumps(event, ensure_ascii=False) + "\n")
                except OSError:
                    pass

                if event_type == "agent_end" and stop_on_agent_end:
                    break
        except Exception:
            pass

        # If no full message collected, use delta parts
        if not collector.get("output") and assistant_parts:
            collector["output"] = "".join(assistant_parts)

    # Send initial prompt
    prompt_data = {"message": input_text}
    send_cmd("prompt", prompt_data)

    # Read events with timeout
    collector = {"output": ""}
    start = time.time()

    # Use a simple timeout loop
    import threading
    done = threading.Event()

    def run_read():
        read_events(collector, stop_on_agent_end=True)
        done.set()

    reader = threading.Thread(target=run_read, daemon=True)
    reader.start()

    if not done.wait(timeout=timeout_secs):
        # Timeout: send abort, wait briefly, then kill
        send_cmd("abort")
        time.sleep(2)
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
            time.sleep(0.5)
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except (ProcessLookupError, OSError):
            pass

    # Wait for process to finish
    # Close stdin to signal ai process to exit
    try:
        proc.stdin.close()
    except (BrokenPipeError, OSError):
        pass
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()

    exit_code = proc.returncode if proc.returncode is not None else -1

    # Write output
    output_text = collector.get("output", "")
    try:
        with open(output_path, "w") as f:
            f.write(output_text)
    except OSError:
        pass

    # Update status and meta
    status = "done" if exit_code == 0 and output_text else "failed"
    if not output_text and exit_code != 0:
        status = "failed"
    elif not output_text and exit_code == 0:
        status = "done"
    elif output_text:
        # We got output, consider it done even if exit code is non-zero
        # (ai process may have been killed during shutdown)
        status = "done"

    try:
        with open(status_path, "w") as f:
            f.write(status + "\n")
    except OSError:
        pass

    if meta_path:
        try:
            with open(meta_path, "r") as f:
                meta = json.load(f)
            meta["finishedAt"] = int(time.time())
            meta["exitCode"] = exit_code
            with open(meta_path, "w") as f:
                json.dump(meta, f, indent=2)
        except (OSError, json.JSONDecodeError):
            pass

    sys.exit(0 if status == "done" else 1)

if __name__ == "__main__":
    main()
`

	// Write the Python bridge script
	// (We'll write it in spawnRPC, not in the bash script)

	// Build the bash wrapper
	script := "#!/bin/bash\n"
	script += "set -o pipefail\n"
	script += fmt.Sprintf("cd %s 2>/dev/null || true\n", shellQuote(cfg.cwd))
	script += fmt.Sprintf("export AI_ARGS=%s\n", shellQuote(mustJSON(cfg.aiArgs)))
	script += fmt.Sprintf("export AI_INPUT=%s\n", shellQuote(escapedInput))
	script += fmt.Sprintf("export AI_OUTPUT=%s\n", shellQuote(cfg.outputPath))
	script += fmt.Sprintf("export AI_EVENTS=%s\n", shellQuote(cfg.eventsPath))
	script += fmt.Sprintf("export AI_STATUS=%s\n", shellQuote(cfg.statusPath))
	script += fmt.Sprintf("export AI_META=%s\n", shellQuote(cfg.metaPath))
	script += fmt.Sprintf("export AI_CWD=%s\n", shellQuote(cfg.cwd))
	script += fmt.Sprintf("export AI_TIMEOUT=%s\n", shellQuote(cfg.timeout))
	script += fmt.Sprintf("exec python3 %s\n", shellQuote(pythonScript))

	// Write the Python script
	os.WriteFile(pythonScript, []byte(pyContent), 0755)

	return script
}

func mustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// ReadEvents reads new events from an RPC-mode agent's events.jsonl file.
// Returns events from the given offset.
func ReadEvents(id string, offset int) ([]json.RawMessage, error) {
	agentDir := storage.AgentDir(id)
	eventsPath := filepath.Join(agentDir, "events.jsonl")

	f, err := os.Open(eventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var events []json.RawMessage
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= offset {
			continue
		}
		line := scanner.Bytes()
		if len(line) > 0 {
			// Must copy: scanner.Bytes() reuses its internal buffer across Scan() calls.
			// Without copying, all json.RawMessage entries would point to the same buffer
			// and contain only the last line's content.
			cp := make(json.RawMessage, len(line))
			copy(cp, line)
			events = append(events, cp)
		}
	}
	return events, scanner.Err()
}

// SendRPC sends an RPC command to a running RPC-mode agent via its inbox.
// The agent picks it up on the next event loop iteration.
func SendRPC(id string, cmdType string, data map[string]interface{}) error {
	agentDir := storage.AgentDir(id)
	status := storage.ReadStatus(agentDir)
	if status != StatusRunning {
		return fmt.Errorf("agent %s is %s (not running)", id, status)
	}

	// For RPC mode, we send commands via the agent's inbox as a special .rpc file
	// The watcher's python bridge will read and forward them
	cmd := map[string]interface{}{
		"id":   fmt.Sprintf("cmd-%d", time.Now().UnixNano()),
		"type": cmdType,
	}
	if data != nil {
		cmd["data"] = data
	}

	// Write to inbox with sequence number
	inboxDir := filepath.Join(agentDir, "inbox")
	entries, _ := os.ReadDir(inboxDir)
	nextSeq := len(entries) + 1
	cmdPath := filepath.Join(inboxDir, fmt.Sprintf("%03d.rpc", nextSeq))

	cmdJSON, _ := json.Marshal(cmd)
	return storage.WriteFile(cmdPath, cmdJSON)
}

// --- Headless Mode (original) ---

// spawnHeadless starts ai --mode headless as a detached watcher process.
func spawnHeadless(cfg SpawnConfig, agentDir string, meta *Meta) (*Meta, error) {
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

// --- Wait ---

func Wait(id string, timeoutSec int) error {
	agentDir := storage.AgentDir(id)
	if !storage.Exists(agentDir) {
		return fmt.Errorf("agent not found: %s", id)
	}

	meta := &Meta{}
	storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)

	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	checkInterval := 500 * time.Millisecond

	// Accelerate polling after 10s
	if timeoutSec > 20 {
		checkInterval = 2 * time.Second
	}

	for time.Now().Before(deadline) {
		storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)

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

// --- Helpers ---

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

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}