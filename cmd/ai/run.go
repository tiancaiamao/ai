package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/tiancaiamao/ai/pkg/run"
)

func serveSubcommand(binPath string) {
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
	sessionFlag := fs.String("session", "", "Session file path (forwarded to ai rpc)")
	systemPromptFlag := fs.String("system-prompt", "", "Custom system prompt (forwarded to ai rpc)")
	maxTurnsFlag := fs.Int("max-turns", 0, "Maximum conversation turns (forwarded to ai rpc)")
	timeoutFlag := fs.Duration("timeout", 0, "Total execution timeout (forwarded to ai rpc)")
	httpFlag := fs.String("http", "", "HTTP debug server address (forwarded to ai rpc)")
	inputFlag := fs.String("input", "", "Initial prompt to send after startup")
	nameFlag := fs.String("name", "", "Human-readable name for the run")
	fs.Parse(os.Args[1:])

	// Generate run ID and create directory.
	id := run.GenerateID()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Error("failed to get home directory", "error", err)
		os.Exit(1)
	}
	baseDir := filepath.Join(homeDir, ".ai")
	runDir := run.RunDir(baseDir, id)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		slog.Error("failed to create run directory", "path", runDir, "error", err)
		os.Exit(1)
	}

	// Print run ID to stderr so callers can capture it.
	fmt.Fprintln(os.Stderr, id)

	// Build RPC flags to forward.
	rpcFlags := buildRPCFlags(*sessionFlag, *systemPromptFlag, *maxTurnsFlag, *timeoutFlag, *httpFlag)

		// On Linux, prefer /proc/self/exe for reliable re-exec.
	if runtime.GOOS == "linux" {
		binPath = "/proc/self/exe"
	}

	cmd := exec.Command(binPath, append([]string{"rpc"}, rpcFlags...)...)
	cwd, _ := os.Getwd()
	cmd.Dir = cwd
	cmd.Stderr = os.Stderr

	// Set up stdin pipe so we can inject steer commands from the socket.
	stdinReader, stdinWriter := io.Pipe()
	cmd.Stdin = stdinReader

	// Set up stdout tee: write to both os.Stdout and events.jsonl.
	eventsPath := run.EventsPath(baseDir, id)
	eventsFile, err := os.Create(eventsPath)
	if err != nil {
		slog.Error("failed to create events file", "path", eventsPath, "error", err)
		os.Exit(1)
	}
	defer eventsFile.Close()

	multiWriter := io.MultiWriter(os.Stdout, eventsFile)
	cmd.Stdout = multiWriter

	// Start the subprocess.
	if err := cmd.Start(); err != nil {
		slog.Error("failed to start rpc subprocess", "error", err)
		os.Exit(1)
	}

	// Write initial run.json.
	meta := &run.RunMeta{
		ID:        id,
		PID:       cmd.Process.Pid,
		CWD:       cwd,
		Status:    run.StatusRunning,
		StartedAt: time.Now().Unix(),
		Name:      *nameFlag,
	}
	metaPath := run.RunMetaPath(baseDir, id)
	if err := run.SaveRunMeta(meta, metaPath); err != nil {
		slog.Error("failed to save run meta", "error", err)
	}

	// Start socket server for external commands (steer/abort/get_state).
	sockPath := run.SocketPath(baseDir, id)
	socketServer := run.NewSocketServer(sockPath, runSocketHandler(meta, metaPath, cmd.Process, stdinWriter))
	if err := socketServer.Start(); err != nil {
		slog.Error("failed to start socket server", "error", err)
	}
	defer func() {
		socketServer.Stop()
		os.Remove(sockPath)
	}()

	// Set up signal handling.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Goroutine to copy os.Stdin to stdinWriter, so interactive input works.
	go func() {
		io.Copy(stdinWriter, os.Stdin)
		stdinWriter.Close()
	}()

	// Send initial input if provided.
	if *inputFlag != "" {
		if err := sendRPCCommand(stdinWriter, "prompt", *inputFlag); err != nil {
			slog.Error("failed to send initial input", "error", err)
		}
	}

	// Wait for subprocess to exit or signal.
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitCh:
		// Subprocess exited on its own.
		case <-ctx.Done():
		// Signal received: forward SIGINT to subprocess (same as Ctrl+C).
		slog.Info("signal received, forwarding to subprocess")
		_ = cmd.Process.Signal(syscall.SIGINT)
		// Close stdin to unblock any reads in the subprocess.
		stdinWriter.Close()
		// Give it a grace period.
		select {
		case waitErr = <-waitCh:
			// Clean exit.
		case <-time.After(5 * time.Second):
			slog.Warn("subprocess did not exit in time, sending SIGTERM")
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case waitErr = <-waitCh:
			case <-time.After(3 * time.Second):
				slog.Warn("subprocess still running, killing")
				_ = cmd.Process.Kill()
				waitErr = <-waitCh
			}
		}
	}

	// Close stdinWriter if not already closed.
	stdinWriter.Close()

	// Determine final status.
	status := run.StatusFailed
	if waitErr == nil {
		status = run.StatusDone
	} else {
		exitErr := &exec.ExitError{}
		if err, ok := waitErr.(*exec.ExitError); ok {
			exitErr = err
			// Check if the process was killed by a signal.
			// On Go, ExitError.ProcessState can be checked via Sys().
			if state, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok {
				if state.Signaled() {
					status = run.StatusKilled
				}
			}
		}
	}

	// Update run.json with final status.
	meta.Status = status
	meta.FinishedAt = time.Now().Unix()
	if err := run.SaveRunMeta(meta, metaPath); err != nil {
		slog.Error("failed to update run meta on exit", "error", err)
	}

	slog.Info("run finished", "id", id, "status", status)

	if status == run.StatusDone {
		os.Exit(0)
	} else {
		os.Exit(1)
	}
}

// buildRPCFlags constructs the flag arguments to forward to 'ai rpc'.
func buildRPCFlags(session, systemPrompt string, maxTurns int, timeout time.Duration, http string) []string {
	var flags []string
	if session != "" {
		flags = append(flags, "--session", session)
	}
	if systemPrompt != "" {
		flags = append(flags, "--system-prompt", systemPrompt)
	}
	if maxTurns > 0 {
		flags = append(flags, "--max-turns", fmt.Sprintf("%d", maxTurns))
	}
	if timeout > 0 {
		flags = append(flags, "--timeout", timeout.String())
	}
	if http != "" {
		flags = append(flags, "--http", http)
	}
	return flags
}

// sendRPCCommand writes a JSON-RPC command to the subprocess stdin.
func sendRPCCommand(w io.Writer, cmdType, message string) error {
	rpcCmd := map[string]string{
		"type":    cmdType,
		"message": message,
	}
	data, err := json.Marshal(rpcCmd)
	if err != nil {
		return fmt.Errorf("marshal rpc command: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

// runSocketHandler returns a CommandHandler that processes socket commands
// by translating them to actions on the running subprocess.
func runSocketHandler(meta *run.RunMeta, metaPath string, proc *os.Process, stdinWriter *io.PipeWriter) run.CommandHandler {
	var mu sync.Mutex

	return func(cmd run.Command) run.Response {
		mu.Lock()
		defer mu.Unlock()

	switch cmd.Type {
	case "steer", "prompt":
		if cmd.Message == "" {
			return run.Response{OK: false, Error: "command requires a message"}
		}
		// Forward as "prompt" so RPC handles slash commands correctly.
		if err := sendRPCCommand(stdinWriter, "prompt", cmd.Message); err != nil {
			return run.Response{OK: false, Error: fmt.Sprintf("command failed: %v", err)}
		}
		return run.Response{OK: true}

		case "abort":
			if err := proc.Signal(syscall.SIGTERM); err != nil {
				return run.Response{OK: false, Error: fmt.Sprintf("abort failed: %v", err)}
			}
			return run.Response{OK: true}

		case "get_state":
			loaded, err := run.LoadRunMeta(metaPath)
			if err != nil {
				return run.Response{OK: false, Error: fmt.Sprintf("load run meta: %v", err)}
			}
			return run.Response{OK: true, Data: loaded}

		default:
			return run.Response{OK: false, Error: fmt.Sprintf("unknown command type: %s", cmd.Type)}
		}
	}
}