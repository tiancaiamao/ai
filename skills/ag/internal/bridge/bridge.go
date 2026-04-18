package bridge

import (
	"encoding/json"
	"fmt"
	"errors"
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
	})

	// 5. Remove stale bridge.sock and create listener (FR-005: BEFORE starting ai)
	sockPath := filepath.Join(agentDir, socketName)
	_ = os.Remove(sockPath)

	// Create SocketServer — listener is bound now, but accept loop starts later
	socketServer := NewSocketServer(sockPath, nil) // handler set after stdinPipe is ready

	// 6. Start the ai process
	cmd := exec.Command("ai", "--mode", "rpc")
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
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
		return fmt.Errorf("start ai: %w", err)
	}

	// 7. Write ai PID to activity writer
	pgid := cmd.Process.Pid
	activity.UpdateActivity(func(a *AgentActivity) {
		a.Pid = pgid
	})

	// 8. Set command handler now that stdinPipe is ready, and start accept loop
	socketServer.SetHandler(func(bc BridgeCommand) BridgeResponse {
		return handleCommand(bc, stdinPipe)
	})
	if err := socketServer.Start(); err != nil {
		cmd.Process.Kill()
		stderrFile.Close()
		return fmt.Errorf("start socket server: %w", err)
	}

	// 9. Send system prompt and initial prompt to ai stdin
	if cfg.System != "" {
		sysMsg := map[string]string{"type": "prompt"}
		if strings.HasPrefix(cfg.System, "@") {
			sysMsg["message"] = "Follow the instructions in " + cfg.System
		} else {
			sysMsg["message"] = "System: " + cfg.System
		}
		data, _ := json.Marshal(sysMsg)
		if _, err := stdinPipe.Write(append(data, '\n')); err != nil {
			log.Printf("bridge: failed to send system prompt: %v", err)
		}
	}

	if cfg.Input != "" {
		msg := map[string]string{"type": "prompt", "message": cfg.Input}
		data, _ := json.Marshal(msg)
		if _, err := stdinPipe.Write(append(data, '\n')); err != nil {
			log.Printf("bridge: failed to send initial prompt: %v", err)
		}
	}

	// 10. Start EventReader goroutine reading from ai stdout
	eventReader := NewEventReader(stdoutPipe, activity, agentDir)
	errCh := make(chan error, 1)
	go func() {
		errCh <- eventReader.Run()
	}()

	// 13. Signal handling: catch SIGTERM for clean shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	// 11. Wait for ai process to exit
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitCh:
		// ai process exited on its own
	case sig := <-sigCh:
		log.Printf("bridge: received signal %v, shutting down ai", sig)
		// Send abort to ai via stdin
		abortMsg, _ := json.Marshal(map[string]string{"type": "abort"})
		stdinPipe.Write(append(abortMsg, '\n'))

		// Wait up to 10s for clean exit
		select {
		case waitErr = <-waitCh:
			log.Printf("bridge: ai exited cleanly after abort")
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
	case <-errCh:
	case <-time.After(2 * time.Second):
	}

	// 12. Cleanup on ai exit
	_ = socketServer.Stop()

	// Close stderr file and write stderr.tail
	stderrFile.Close()
	writeStderrTail(stderrPath, filepath.Join(agentDir, stderrTail))

	// If ai exit code != 0 and status still "running", set to "failed"
	exitCode := exitCodeFromErr(waitErr)
	if exitCode != 0 {
		activity.UpdateActivity(func(a *AgentActivity) {
			if a.Status == StatusRunning {
				a.Status = StatusFailed
				a.FinishedAt = time.Now().Unix()
				if waitErr != nil && a.Error == "" {
					a.Error = waitErr.Error()
				}
			}
		})
	}

	// Write output file from EventReader accumulated output
	outputText := eventReader.Output()
	if outputText != "" {
		outputPath := filepath.Join(agentDir, "output")
		_ = os.WriteFile(outputPath, []byte(outputText), 0644)
	}

	activity.Close()

	return nil
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

// handleCommand maps BridgeCommand types to ai stdin writes.
func handleCommand(cmd BridgeCommand, stdin io.Writer) BridgeResponse {
	var rpcMsg any

	switch cmd.Type {
	case CmdSteer:
		rpcMsg = map[string]string{"type": "steer", "message": cmd.Message}
	case CmdAbort:
		rpcMsg = map[string]string{"type": "abort"}
	case CmdPrompt:
		rpcMsg = map[string]string{"type": "prompt", "message": cmd.Message}
	case CmdGetState:
		rpcMsg = map[string]string{"type": "get_state"}
	case CmdShutdown:
		// Send abort then signal clean exit handled elsewhere
		rpcMsg = map[string]string{"type": "abort"}
	default:
		return BridgeResponse{OK: false, Error: fmt.Sprintf("unknown command type: %s", cmd.Type)}
	}

	data, err := json.Marshal(rpcMsg)
	if err != nil {
		return BridgeResponse{OK: false, Error: fmt.Sprintf("marshal rpc: %v", err)}
	}

	if _, err := stdin.Write(append(data, '\n')); err != nil {
		return BridgeResponse{OK: false, Error: fmt.Sprintf("write to ai stdin: %v", err)}
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