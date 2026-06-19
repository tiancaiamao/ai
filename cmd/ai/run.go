package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/run"
)

// serveConfig holds the common configuration for launching an RPC subprocess.
type serveConfig struct {
	session      string
	systemPrompt string
	maxTurns     int
	timeout      time.Duration
	http         string
	name         string
	role         string
	model        string
	daemon       bool // true for serve (new process group), false for run
}

// serveProcess holds the runtime state of a managed RPC subprocess.
type serveProcess struct {
	cmd          *exec.Cmd
	stdinWriter  *os.File
	broadcaster  *run.EventBroadcaster
	meta         *run.RunMeta
	metaPath     string
	sockPath     string
	baseDir      string
	socketServer *run.SocketServer
	bridgeDone   chan struct{}
	logFile      *os.File
}

// Close stops the socket server and releases resources.
func (sp *serveProcess) Close() {
	sp.socketServer.Stop()
	os.Remove(sp.sockPath)
	sp.broadcaster.Close()
	sp.logFile.Close()
}

// startServeProcess launches an RPC subprocess with shared infrastructure:
// run ID, log file, stdin/stdout pipes, event broadcaster, and socket server.
func startServeProcess(binPath string, cfg serveConfig) *serveProcess {
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

	// Resolve system prompt: --system-prompt overrides --role.
	// Parse @file syntax before role fallback.
	sysPrompt := parseSystemPrompt(cfg.systemPrompt)
	if sysPrompt == "" && cfg.role != "coder" {
		tmpl, err := prompt.TemplateForRole(cfg.role)
		if err != nil {
			slog.Error("invalid role", "error", err)
			os.Exit(1)
		}
		sysPrompt = tmpl
	}

	// Build RPC flags to forward.
	rpcFlags := buildRPCFlags(cfg.session, sysPrompt, cfg.maxTurns, cfg.timeout, cfg.http, cfg.model, id)

	if runtime.GOOS == "linux" {
		binPath = "/proc/self/exe"
	}

	cmd := exec.Command(binPath, append([]string{"rpc"}, rpcFlags...)...)
	cwd, _ := os.Getwd()
	cmd.Dir = cwd

	// Daemon mode: detach from terminal with new process group.
	if cfg.daemon {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	// Redirect subprocess stderr to log file.
	logPath := filepath.Join(runDir, "error.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		slog.Error("failed to create log file", "path", logPath, "error", err)
		os.Exit(1)
	}
	cmd.Stderr = logFile

	// Stdin pipe for sending commands.
	// Use os.Pipe instead of io.Pipe: io.Pipe is a synchronous in-memory
	// pipe that requires Go's os/exec to spawn internal goroutines for copying
	// between the pipe and the child's file descriptors. This is unreliable —
	// data written to the io.PipeWriter may never reach the subprocess.
	// os.Pipe provides kernel-buffered OS-level pipes that the child reads
	// directly, with no intermediate goroutines.
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		slog.Error("failed to create stdin pipe", "error", err)
		os.Exit(1)
	}
	cmd.Stdin = stdinReader

	// Stdout goes to event broadcaster instead of events.jsonl.
	// The broadcaster fans out to N watch clients via ring buffer + channels.
	broadcaster := run.NewEventBroadcaster()

	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		slog.Error("failed to create stdout pipe", "error", err)
		os.Exit(1)
	}
	cmd.Stdout = pipeWriter

	// Start the subprocess.
	if err := cmd.Start(); err != nil {
		slog.Error("failed to start rpc subprocess", "error", err)
		os.Exit(1)
	}

	// Close parent's copies of child-side file descriptors.
	// After cmd.Start(), the child has inherited these FDs via fork/exec.
	// Keeping them open in the parent would prevent EOF detection.
	stdinReader.Close()
	pipeWriter.Close()

	// Bridge goroutine: read stdout lines from pipe → push to broadcaster.
	bridgeDone := make(chan struct{})
	go func() {
		defer close(bridgeDone)
		defer pipeReader.Close()
		scanner := bufio.NewScanner(pipeReader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			lineCopy := make([]byte, len(line))
			copy(lineCopy, line)
			broadcaster.Push(lineCopy)
		}
		if err := scanner.Err(); err != nil {
			slog.Error("stdout bridge scanner error", "error", err)
		}
	}()

	// Write initial run.json.
	meta := &run.RunMeta{
		ID:           id,
		PID:          cmd.Process.Pid,
		CWD:          cwd,
		Status:       run.StatusRunning,
		StartedAt:    time.Now().Unix(),
		Name:         cfg.name,
		PidStartTime: run.GetProcessStartTime(cmd.Process.Pid),
	}
	metaPath := run.RunMetaPath(baseDir, id)
	if err := run.SaveRunMeta(meta, metaPath); err != nil {
		slog.Error("failed to save run meta", "error", err)
	}

	// Start socket server for external commands + event streaming.
	sockPath := run.SocketPath(baseDir, id)
	socketServer := run.NewSocketServer(sockPath, runSocketHandler(meta, metaPath, cmd.Process, stdinWriter))
	socketServer.SetBroadcaster(broadcaster)
	if err := socketServer.Start(); err != nil {
		slog.Error("failed to start socket server", "error", err)
		cmd.Process.Kill()
		meta.Status = run.StatusFailed
		meta.FinishedAt = time.Now().Unix()
		run.SaveRunMeta(meta, metaPath)
		os.Exit(1)
	}

	return &serveProcess{
		cmd:          cmd,
		stdinWriter:  stdinWriter,
		broadcaster:  broadcaster,
		meta:         meta,
		metaPath:     metaPath,
		sockPath:     sockPath,
		baseDir:      baseDir,
		socketServer: socketServer,
		bridgeDone:   bridgeDone,
		logFile:      logFile,
	}
}

func runSubcommand(binPath string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	sessionFlag := fs.String("session", "", "Session file path (forwarded to ai rpc)")
	systemPromptFlag := fs.String("system-prompt", "", "Custom system prompt (forwarded to ai rpc)")
	maxTurnsFlag := fs.Int("max-turns", 0, "Maximum conversation turns (forwarded to ai rpc)")
	timeoutFlag := fs.Duration("timeout", 0, "Total execution timeout (forwarded to ai rpc)")
	httpFlag := fs.String("http", "", "HTTP debug server address (forwarded to ai rpc)")
	inputFlag := fs.String("input", "", "Initial prompt to send after startup")
	nameFlag := fs.String("name", "", "Human-readable name for the run")
	roleFlag := fs.String("role", "coder", "Agent role: coder (default), orchestrator, validator")
	modelFlag := fs.String("model", "", "Override LLM model ID (e.g. claude-sonnet-4-20250514)")
	fs.Parse(os.Args[1:])

	sp := startServeProcess(binPath, serveConfig{
		session:      *sessionFlag,
		systemPrompt: *systemPromptFlag,
		maxTurns:     *maxTurnsFlag,
		timeout:      *timeoutFlag,
		http:         *httpFlag,
		name:         *nameFlag,
		role:         *roleFlag,
		model:        *modelFlag,
	})
	defer sp.Close()

	// Send initial input if provided.
	if *inputFlag != "" {
		if err := sendRPCCommand(sp.stdinWriter, "prompt", *inputFlag); err != nil {
			slog.Error("failed to send initial input", "error", err)
		}
	}

	// Launch watch TUI in foreground.
	// The TUI reads events from broadcaster via ring buffer and renders to the terminal.
	// User input is forwarded to the subprocess via the socket.
	m := newRunModel(sp.broadcaster, sp.meta.ID, sp.sockPath, sp.cmd.Process, sp.stdinWriter, sp.meta, sp.metaPath)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		slog.Error("TUI error", "error", err)
	}

	// TUI exited — clean up subprocess.
	// Close stdin pipe so the child sees EOF on stdin.
	sp.stdinWriter.Close()

	sp.cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- sp.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		sp.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			sp.cmd.Process.Kill()
			<-done
		}
	}

	// Update final status.
	sp.meta.Status = run.StatusDone
	sp.meta.FinishedAt = time.Now().Unix()
	run.SaveRunMeta(sp.meta, sp.metaPath)
}

// serveSubcommand starts the agent as a daemon process.
// It runs in the foreground but keeps I/O silent (redirected to files).
// The socket server runs in-process, enabling ai send/watch control.
// Use "ai serve &" or "nohup ai serve &" for background operation.
func serveSubcommand(binPath string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	sessionFlag := fs.String("session", "", "Session file path (forwarded to ai rpc)")
	systemPromptFlag := fs.String("system-prompt", "", "Custom system prompt (forwarded to ai rpc)")
	maxTurnsFlag := fs.Int("max-turns", 0, "Maximum conversation turns (forwarded to ai rpc)")
	timeoutFlag := fs.Duration("timeout", 0, "Total execution timeout (forwarded to ai rpc)")
	httpFlag := fs.String("http", "", "HTTP debug server address (forwarded to ai rpc)")
	inputFlag := fs.String("input", "", "Initial prompt to send after startup")
	inputFileFlag := fs.String("input-file", "", "Read initial prompt from file (avoids OS ARG_MAX limits)")
	nameFlag := fs.String("name", "", "Human-readable name for the run")
	roleFlag := fs.String("role", "coder", "Agent role: coder (default), orchestrator, validator")
	idFileFlag := fs.String("id-file", "", "Write run ID to this file after startup (useful for background mode)")
	modelFlag := fs.String("model", "", "Override LLM model ID (e.g. claude-sonnet-4-20250514)")
	fs.Parse(os.Args[1:])

	sp := startServeProcess(binPath, serveConfig{
		session:      *sessionFlag,
		systemPrompt: *systemPromptFlag,
		maxTurns:     *maxTurnsFlag,
		timeout:      *timeoutFlag,
		http:         *httpFlag,
		name:         *nameFlag,
		role:         *roleFlag,
		model:        *modelFlag,
		daemon:       true,
	})
	defer sp.Close()

	// Send initial input if provided.
	inputText := *inputFlag
	if *inputFileFlag != "" {
		data, err := os.ReadFile(*inputFileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to read input file: %v\n", err)
			sp.cmd.Process.Kill()
			os.Exit(1)
		}
		inputText = string(data)
	}
	if inputText != "" {
		if err := sendRPCCommand(sp.stdinWriter, "prompt", inputText); err != nil {
			fmt.Fprintf(os.Stderr, "warn: failed to send initial input: %v\n", err)
		}
	}

	// Capture process exit state to avoid the double-wait race with cmd.Wait().
	// Close stdinWriter when the subprocess exits so the child sees EOF on stdin
	// and the pipe is properly cleaned up.
	processStateCh := make(chan *os.ProcessState, 1)
	go func() {
		state, _ := sp.cmd.Process.Wait()
		processStateCh <- state
		sp.stdinWriter.Close()
	}()

	// Write run ID to file if requested (caller can poll this file instead of
	// capturing stdout — useful when running in background via "&").
	if *idFileFlag != "" {
		if err := os.WriteFile(*idFileFlag, []byte(sp.meta.ID+"\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "warn: failed to write id-file: %v\n", err)
		}
	}

	// Wait for subprocess to exit.
	_ = sp.cmd.Wait()

	// Wait for the bridge goroutine to finish reading remaining stdout.
	// pipeWriter was already closed after cmd.Start(), so the bridge goroutine
	// will see EOF once the child exits and its stdout is closed.
	<-sp.bridgeDone

	// Determine final status using the captured process state (not cmd.Wait()
	// error, which is unreliable due to the double-wait).
	processState := <-processStateCh
	status := run.StatusFailed
	if processState == nil {
		status = run.StatusKilled
	} else if processState.Success() {
		status = run.StatusDone
	} else {
		if ws, ok := processState.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				status = run.StatusKilled
			}
		}
	}

	sp.meta.Status = status
	sp.meta.FinishedAt = time.Now().Unix()
	run.SaveRunMeta(sp.meta, sp.metaPath)
}

// buildRPCFlags constructs the flag arguments to forward to 'ai rpc'.
func buildRPCFlags(session, systemPrompt string, maxTurns int, timeout time.Duration, http, model, runid string) []string {
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
	if model != "" {
		flags = append(flags, "--model", model)
	}
	if runid != "" {
		flags = append(flags, "--runid", runid)
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

// sendRPCCommandResult is like sendRPCCommand but returns the write count.
func sendRPCCommandResult(w io.Writer, cmdType, message string) (int, error) {
	rpcCmd := map[string]string{
		"type":    cmdType,
		"message": message,
	}
	data, err := json.Marshal(rpcCmd)
	if err != nil {
		return 0, fmt.Errorf("marshal rpc command: %w", err)
	}
	data = append(data, '\n')
	return w.Write(data)
}

// sendRPCCommandWithTimeout is like sendRPCCommand but aborts the write
// after the given deadline. This is a safety measure for cases where the
// subprocess is dead or unresponsive — with os.Pipe, writes return quickly
// (kernel buffer), but the process may have exited and the data is lost anyway.
func sendRPCCommandWithTimeout(w io.Writer, cmdType, message string, timeout time.Duration) error {
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)

	go func() {
		n, err := sendRPCCommandResult(w, cmdType, message)
		done <- result{n, err}
	}()

	select {
	case r := <-done:
		return r.err
	case <-time.After(timeout):
		return fmt.Errorf("write timed out after %v (subprocess likely dead)", timeout)
	}
}

// runSocketHandler creates a command handler for the socket server.
// It wraps the RPC subprocess stdin/stdout with liveness checks and timeouts.
func runSocketHandler(meta *run.RunMeta, metaPath string, proc *os.Process, stdinWriter io.Writer) run.CommandHandler {
	var mu sync.Mutex

	// isAlive checks whether the subprocess is still running.
	// Signal(0) returns an error if the process has exited but hasn't been reaped yet.
	isAlive := func() bool {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return false
		}
		return true
	}

	return func(cmd run.Command) run.Response {
		mu.Lock()
		defer mu.Unlock()

		switch cmd.Type {
		case "steer", "prompt":
			if cmd.Message == "" {
				return run.Response{OK: false, Error: "command requires a message"}
			}
			if !isAlive() {
				return run.Response{OK: false, Error: "subprocess is no longer alive"}
			}
			// Forward as "prompt" so RPC handles slash commands correctly.
			// Use a deadline so the write does not block forever when the
			// subprocess dies between the liveness check and the write.
			if err := sendRPCCommandWithTimeout(stdinWriter, "prompt", cmd.Message, 10*time.Second); err != nil {
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

// --- runModel: watchModel + user input ---

// runModel extends the watch TUI with user input support.
// It embeds watchModel for event rendering and adds a text input
// for sending messages to the running agent via socket.
type runModel struct {
	watchModel
	sockPath    string
	proc        *os.Process
	stdinPipe   io.Writer
	meta        *run.RunMeta
	metaPath    string
	inputMode   bool // true when user is typing a message
	inputBuf    *strings.Builder
	broadcaster *run.EventBroadcaster
}

func newRunModel(
	broadcaster *run.EventBroadcaster, runID, sockPath string,
	proc *os.Process,
	stdinPipe io.Writer,
	meta *run.RunMeta,
	metaPath string,
) runModel {
	w := newWatchModelFromBroadcaster(broadcaster, runID)
	return runModel{
		watchModel:  w,
		sockPath:    sockPath,
		proc:        proc,
		stdinPipe:   stdinPipe,
		meta:        meta,
		metaPath:    metaPath,
		inputBuf:    &strings.Builder{},
		broadcaster: broadcaster,
	}
}

func (m runModel) Init() tea.Cmd {
	return m.watchModel.Init()
}

func (m runModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle input mode: user is typing a message.
		if m.inputMode {
			switch msg.Type {
			case tea.KeyEnter:
				// Send the message.
				text := m.inputBuf.String()
				m.inputBuf.Reset()
				m.inputMode = false
				if text != "" {
					if err := m.sendMessage(text); err != nil {
						m.appendContent(errStyle.Render("ai: send failed: " + err.Error()))
						m.syncIfDirty()
					}
				}
				return m, nil
			case tea.KeyEsc:
				// Cancel input.
				m.inputBuf.Reset()
				m.inputMode = false
				return m, nil
			case tea.KeyBackspace:
				// Remove last rune from input buffer.
				runes := []rune(m.inputBuf.String())
				if len(runes) > 0 {
					m.inputBuf.Reset()
					m.inputBuf.WriteString(string(runes[:len(runes)-1]))
				}
				return m, nil
			default:
				// Append typed character to input buffer.
				m.inputBuf.WriteString(msg.String())
				return m, nil
			}
		}

		// Normal mode: handle navigation and commands.
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "i", ":":
			// Enter input mode.
			m.inputMode = true
			return m, nil
		case "left", "h":
			m.viewport.ScrollLeft(scrollStep)
			return m, nil
		case "right", "l":
			m.viewport.ScrollRight(scrollStep)
			return m, nil
		}
	}

	// Delegate to watchModel for event processing.
	w, cmd := m.watchModel.Update(msg)
	m.watchModel = w.(watchModel)
	return m, cmd
}

func (m runModel) View() string {
	// Build status bar.
	status := fmt.Sprintf(" ai run | run %s | %s", m.runID, m.mode)
	if m.inputMode {
		input := m.inputBuf.String()
		if input == "" {
			status += " | : " // show prompt cursor
		} else {
			status += " | " + input
		}
		status = statusBar.Render(status)
	} else {
		status += " | press i to input, q to quit"
		status = statusBar.Render(status)
	}

	if !m.ready {
		return "\n  Starting...\n"
	}

	return m.viewport.View() + "\n" + status
}

// sendMessage sends a user message to the agent via socket.
func (m *runModel) sendMessage(text string) error {
	conn, err := net.DialTimeout("unix", m.sockPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect to socket: %w", err)
	}
	defer conn.Close()

	cmd := run.Command{Type: "prompt", Message: text}
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write command: %w", err)
	}
	return nil
}
