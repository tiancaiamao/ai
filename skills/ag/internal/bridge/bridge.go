package bridge

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/backend"
	"github.com/genius/ag/internal/storage"
)

const (
	socketName = "bridge.sock"
	stderrTail = "stderr.tail"
	stderrMax  = 4096
)

// Run starts the bridge process for an agent. This is the main entry point
// called by 'ag bridge <id>' inside a tmux session.
func Run(id string) error {
	// 0. Validate agent ID (defense-in-depth)
	if err := agent.ValidateID(id); err != nil {
		return fmt.Errorf("invalid agent id: %w", err)
	}

	agentDir := storage.AgentDir(id)

	// 1. Read spawn config from meta.json
	metaPath := filepath.Join(agentDir, "meta.json")
	var cfg SpawnConfig
	if err := storage.ReadJSON(metaPath, &cfg); err != nil {
		return fmt.Errorf("read meta.json: %w", err)
	}

	// 1b. Load backend configuration
	backendName := cfg.Backend
	if backendName == "" {
		backendName = "ai" // default
	}
	backendsPath := backend.FindBackendsFile()
	backendsCfg, err := backend.LoadOrDefault(backendsPath)
	if err != nil {
		return fmt.Errorf("load backends: %w", err)
	}
	be, err := backendsCfg.Find(backendName)
	if err != nil {
		return fmt.Errorf("unknown backend %q: %w", backendName, err)
	}

	// 2. Ensure agent directory exists
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

	// 3. Redirect bridge's own stderr to bridge-stderr (FR-008)
	bridgeStderrPath := filepath.Join(agentDir, "bridge-stderr")
	bridgeStderr, err := os.OpenFile(bridgeStderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("create bridge-stderr: %w", err)
	}
	defer bridgeStderr.Close()
	log.SetOutput(bridgeStderr)
	// Also redirect os.Stderr for any unhandled panics
	os.Stderr = bridgeStderr

	// 4. Create ActivityWriter and initialize with status "running"
	activity := NewActivityWriter(agentDir)
	activity.UpdateActivity(func(a *AgentActivity) {
		a.Status = StatusRunning
		a.StartedAt = time.Now().Unix()
		a.Backend = backendName
	})

	// 4b. Create StreamWriter for real-time stream.log output
	streamWriter, err := NewStreamWriter(agentDir)
	if err != nil {
		return fmt.Errorf("create stream writer: %w", err)
	}
	defer streamWriter.Close()

	// 5. Remove stale bridge.sock and create listener (FR-005: BEFORE starting agent)
	sockPath := filepath.Join(agentDir, socketName)
	_ = os.Remove(sockPath)

	// Create SocketServer — listener is bound now, but accept loop starts later
	socketServer := NewSocketServer(sockPath, nil) // handler set after stdinPipe is ready

	// 6. Build the command from backend config
	cmdArgs := make([]string, len(be.Args))
	copy(cmdArgs, be.Args)
	cmd := exec.Command(be.Command, cmdArgs...)
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}

	// For json-rpc backend, pass system prompt via CLI flag
	if be.Protocol == backend.ProtocolJSONRPC && cfg.System != "" {
		cmd.Args = append(cmd.Args, "--system-prompt", cfg.System)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderrPath := filepath.Join(agentDir, "stderr")
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return fmt.Errorf("create stderr file: %w", err)
	}
	cmd.Stderr = stderrFile

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		stderrFile.Close()
		return fmt.Errorf("start %s: %w", be.Command, err)
	}

	// 7. Write agent PID to activity writer
	pgid := cmd.Process.Pid
	activity.UpdateActivity(func(a *AgentActivity) {
		a.Pid = pgid
	})

	// 8. Set command handler now that stdinPipe is ready, and start accept loop
	// For raw protocol backends that don't support steer/abort/prompt,
	// commands will return an error.
	socketServer.SetHandler(func(bc BridgeCommand) BridgeResponse {
		return handleCommand(bc, stdinPipe, be)
	})
	if err := socketServer.Start(); err != nil {
		cmd.Process.Kill()
		stderrFile.Close()
		return fmt.Errorf("start socket server: %w", err)
	}

	// 9. Send initial input
	if cfg.Input != "" {
		if be.Protocol == backend.ProtocolJSONRPC {
			msg := map[string]string{"type": "prompt", "message": cfg.Input}
			data, _ := json.Marshal(msg)
			if _, err := stdinPipe.Write(append(data, '\n')); err != nil {
				log.Printf("bridge: failed to send initial prompt: %v", err)
			}
				} else {
			// Raw protocol: write input as plain text followed by newline
			if _, err := fmt.Fprintln(stdinPipe, cfg.Input); err != nil {
				log.Printf("bridge: failed to send initial input: %v", err)
			}
			// Raw backends (e.g., codex exec) read stdin until EOF before processing.
			// Since raw protocol has no follow-up commands, close stdin to signal EOF.
			stdinPipe.Close()
		}
	}

	// 10. Start protocol-appropriate reader
	errCh := make(chan error, 1)
	go func() {
		if be.Protocol == backend.ProtocolJSONRPC {
			eventReader := NewEventReader(stdoutPipe, activity, streamWriter, agentDir)
			errCh <- eventReader.Run()
		} else {
			errCh <- runRawReader(stdoutPipe, activity, streamWriter)
		}
	}()

	// 13. Signal handling: catch SIGTERM for clean shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// 11. Wait for agent process to exit
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitCh:
		// agent process exited on its own
	case sig := <-sigCh:
		log.Printf("bridge: received signal %v, shutting down %s", sig, be.Command)
		// Send abort if supported
		if be.Protocol == backend.ProtocolJSONRPC {
			abortMsg, _ := json.Marshal(map[string]string{"type": "abort"})
			stdinPipe.Write(append(abortMsg, '\n'))
		}
		// Kill regardless after grace period
		select {
		case waitErr = <-waitCh:
			log.Printf("bridge: agent exited cleanly after signal")
		case <-time.After(10 * time.Second):
			log.Printf("bridge: killing process group %d", pgid)
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			waitErr = <-waitCh
		}
	}

	// Stop reading events
	_ = stdoutPipe.Close()

	// Wait for event reader to finish (drain remaining)
	select {
	case readerErr := <-errCh:
		if readerErr != nil {
			log.Printf("bridge: event reader error: %v", readerErr)
		}
	case <-time.After(2 * time.Second):
		log.Printf("bridge: event reader did not finish in time, proceeding with cleanup")
	}

	// 12. Cleanup on agent exit
	_ = socketServer.Stop()

	// Close stderr file and write stderr.tail
	stderrFile.Close()
	writeStderrTail(stderrPath, filepath.Join(agentDir, stderrTail))

	finalizeActivityOnProcessExit(activity, exitCodeFromErr(waitErr), waitErr)

	// Write output file as a copy of stream.log for backward compatibility
	streamLogPath := streamWriter.Path()
	if content, err := os.ReadFile(streamLogPath); err == nil && len(content) > 0 {
		outputPath := filepath.Join(agentDir, "output")
		_ = os.WriteFile(outputPath, content, 0644)
	}

	activity.Close()

	return nil
}

// runRawReader reads lines from stdout and writes them to the stream log.
// For raw protocol backends, we simply capture all output as text.
func runRawReader(stdout io.Reader, activity *ActivityWriter, sw *StreamWriter) error {
	var stopFlusher func()
	if sw != nil {
		stopFlusher = sw.RunFlusher()
		defer stopFlusher()
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	turns := 0

	for scanner.Scan() {
		line := scanner.Text()
		turns++

		// Write to stream.log
		if sw != nil {
			sw.AppendText(line)
		}

		// Update activity with incremental counts
		activity.UpdateActivity(func(a *AgentActivity) {
			a.Turns = turns
			a.LastText = truncateStr(line, 200)
			// No token counting for raw protocol
		})
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdout: %w", err)
	}
	return nil
}

// finalizeActivityOnProcessExit updates activity.json based on the final process
// exit result without overriding an already terminal status.
func finalizeActivityOnProcessExit(activity *ActivityWriter, exitCode int, waitErr error) {
	now := time.Now().Unix()
	activity.UpdateActivity(func(a *AgentActivity) {
		if exitCode != 0 {
			if a.Status == StatusRunning {
				a.Status = StatusFailed
				if waitErr != nil && a.Error == "" {
					a.Error = waitErr.Error()
				}
			}
		} else if a.Status == StatusRunning {
			// Raw backends may never emit an explicit "agent_end" event.
			a.Status = StatusDone
		}

		if a.FinishedAt == 0 {
			a.FinishedAt = now
		}
	})
}

// truncateStr returns s truncated to maxLen runes with "..." suffix.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen > 3 {
		return s[:maxLen-3] + "..."
	}
	return s[:maxLen]
}

// exitCodeFromErr extracts the exit code from a cmd.Wait error, if any.
// Returns 0 for nil error.
func exitCodeFromErr(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

// handleCommand maps BridgeCommand types to agent stdin writes.
// For raw protocol backends, steer/abort/prompt return errors if unsupported.
func handleCommand(cmd BridgeCommand, stdin io.Writer, be *backend.BackendConfig) BridgeResponse {
	switch cmd.Type {
	case CmdSteer:
		if !be.Supports.Steer {
			return BridgeResponse{OK: false, Error: fmt.Sprintf("backend %q does not support steer", be.Name)}
		}
		return sendJSONRPC(cmd.Type, cmd.Message, stdin)
	case CmdAbort:
		if !be.Supports.Abort {
			return BridgeResponse{OK: false, Error: fmt.Sprintf("backend %q does not support abort", be.Name)}
		}
		return sendJSONRPC(cmd.Type, "", stdin)
	case CmdPrompt:
		if !be.Supports.Prompt {
			return BridgeResponse{OK: false, Error: fmt.Sprintf("backend %q does not support prompt", be.Name)}
		}
		return sendJSONRPC(cmd.Type, cmd.Message, stdin)
	case CmdGetState:
		return sendJSONRPC(cmd.Type, "", stdin)
	case CmdShutdown:
		if be.Supports.Abort {
			return sendJSONRPC(CmdAbort, "", stdin)
		}
		return BridgeResponse{OK: false, Error: fmt.Sprintf("backend %q does not support shutdown", be.Name)}
	default:
		return BridgeResponse{OK: false, Error: fmt.Sprintf("unknown command type: %s", cmd.Type)}
	}
}

// sendJSONRPC sends a JSON-RPC message to the agent's stdin.
func sendJSONRPC(cmdType, message string, stdin io.Writer) BridgeResponse {
	var rpcMsg any
	switch cmdType {
	case CmdSteer:
		rpcMsg = map[string]string{"type": "steer", "message": message}
	case CmdAbort:
		rpcMsg = map[string]string{"type": "abort"}
	case CmdPrompt:
		rpcMsg = map[string]string{"type": "prompt", "message": message}
	case CmdGetState:
		rpcMsg = map[string]string{"type": "get_state"}
	default:
		return BridgeResponse{OK: false, Error: fmt.Sprintf("unsupported json-rpc command: %s", cmdType)}
	}

	data, err := json.Marshal(rpcMsg)
	if err != nil {
		return BridgeResponse{OK: false, Error: fmt.Sprintf("marshal rpc: %v", err)}
	}

	if _, err := stdin.Write(append(data, '\n')); err != nil {
		return BridgeResponse{OK: false, Error: fmt.Sprintf("write to stdin: %v", err)}
	}

	return BridgeResponse{OK: true}
}

// writeStderrTail writes the last ~stderrMax bytes of the stderr file to tailPath.
func writeStderrTail(stderrPath, tailPath string) {
	f, err := os.Open(stderrPath)
	if err != nil {
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return
	}

	size := info.Size()
	offset := int64(0)
	if size > stderrMax {
		offset = size - stderrMax
	}

	buf := make([]byte, stderrMax)
	n, _ := f.ReadAt(buf, offset)
	if n > 0 {
		text := string(buf[:n])
		if offset > 0 {
			if idx := strings.IndexByte(text, '\n'); idx >= 0 {
				text = text[idx+1:]
			}
		}
		_ = os.WriteFile(tailPath, []byte(text), 0644)
	}
}
