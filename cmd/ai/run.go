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

	"github.com/tiancaiamao/ai/pkg/run"
)

func runSubcommand(binPath string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
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

	// Build RPC flags to forward.
	rpcFlags := buildRPCFlags(*sessionFlag, *systemPromptFlag, *maxTurnsFlag, *timeoutFlag, *httpFlag)

	if runtime.GOOS == "linux" {
		binPath = "/proc/self/exe"
	}

	cmd := exec.Command(binPath, append([]string{"rpc"}, rpcFlags...)...)
	cwd, _ := os.Getwd()
	cmd.Dir = cwd

	// Redirect subprocess stderr to log file (not terminal — TUI owns the terminal).
	logPath := filepath.Join(runDir, "rpc.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		slog.Error("failed to create log file", "path", logPath, "error", err)
		os.Exit(1)
	}
	defer logFile.Close()
	cmd.Stderr = logFile

	// Stdin pipe for sending commands.
	stdinReader, stdinWriter := io.Pipe()
	cmd.Stdin = stdinReader

	// Stdout goes to event broadcaster instead of events.jsonl.
	// The broadcaster fans out to N watch clients via ring buffer + channels.
	broadcaster := run.NewEventBroadcaster()
	defer broadcaster.Close()

	pipeReader, pipeWriter := io.Pipe()
	cmd.Stdout = pipeWriter

	// Start the subprocess.
	if err := cmd.Start(); err != nil {
		slog.Error("failed to start rpc subprocess", "error", err)
		os.Exit(1)
	}

	// Bridge goroutine: read stdout lines from pipe → push to broadcaster.
	go func() {
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
		Name:         *nameFlag,
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
	defer func() {
		socketServer.Stop()
		os.Remove(sockPath)
	}()

	// Send initial input if provided.
	if *inputFlag != "" {
		if err := sendRPCCommand(stdinWriter, "prompt", *inputFlag); err != nil {
			slog.Error("failed to send initial input", "error", err)
		}
	}

	// Launch watch TUI in foreground.
	// The TUI reads events from broadcaster via ring buffer and renders to the terminal.
	// User input is forwarded to the subprocess via the socket.
	m := newRunModel(broadcaster, id, sockPath, cmd.Process, stdinWriter, meta, metaPath)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		slog.Error("TUI error", "error", err)
	}

	// TUI exited — clean up subprocess.
	// Close stdin pipe first so the internal goroutine in cmd.Wait() can exit.
	// Without this, cmd.Wait() blocks forever on the pipe reader goroutine.
	stdinWriter.Close()

	cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}

	// Update final status.
	meta.Status = run.StatusDone
	meta.FinishedAt = time.Now().Unix()
	run.SaveRunMeta(meta, metaPath)
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
	fs.Parse(os.Args[1:])

	// Generate run ID and create directory.
	id := run.GenerateID()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	baseDir := filepath.Join(homeDir, ".ai")
	runDir := run.RunDir(baseDir, id)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create run directory: %v\n", err)
		os.Exit(1)
	}

	// Build RPC flags to forward.
	rpcFlags := buildRPCFlags(*sessionFlag, *systemPromptFlag, *maxTurnsFlag, *timeoutFlag, *httpFlag)

	if runtime.GOOS == "linux" {
		binPath = "/proc/self/exe"
	}

	cmd := exec.Command(binPath, append([]string{"rpc"}, rpcFlags...)...)
	cwd, _ := os.Getwd()
	cmd.Dir = cwd

	// Detach from terminal: new process group so signals don't propagate.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Redirect stderr to log file.
	logPath := filepath.Join(runDir, "rpc.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()
	cmd.Stderr = logFile

	// Stdin pipe for sending commands.
	stdinReader, stdinWriter := io.Pipe()
	cmd.Stdin = stdinReader

	// Stdout goes to event broadcaster instead of events.jsonl.
	broadcaster := run.NewEventBroadcaster()
	defer broadcaster.Close()

	pipeReader, pipeWriter := io.Pipe()
	cmd.Stdout = pipeWriter

	// Start the subprocess.
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to start rpc subprocess: %v\n", err)
		os.Exit(1)
	}

	// Bridge goroutine: read stdout lines from pipe → push to broadcaster.
	agentEndCh := make(chan struct{}, 1)
	go func() {
		defer pipeReader.Close()
		scanner := bufio.NewScanner(pipeReader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			lineCopy := make([]byte, len(line))
			copy(lineCopy, line)
			broadcaster.Push(lineCopy)

			// Check for agent_end event to signal subprocess completion.
			if hasAgentEndEvent(lineCopy) {
				select {
				case agentEndCh <- struct{}{}:
				default:
				}
			}
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
		Name:         *nameFlag,
		PidStartTime: run.GetProcessStartTime(cmd.Process.Pid),
	}
	metaPath := run.RunMetaPath(baseDir, id)
	if err := run.SaveRunMeta(meta, metaPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to save run meta: %v\n", err)
	}

	// Start socket server for external commands + event streaming.
	sockPath := run.SocketPath(baseDir, id)
	socketServer := run.NewSocketServer(sockPath, runSocketHandler(meta, metaPath, cmd.Process, stdinWriter))
	socketServer.SetBroadcaster(broadcaster)
	if err := socketServer.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to start socket server: %v\n", err)
		cmd.Process.Kill()
		meta.Status = run.StatusFailed
		meta.FinishedAt = time.Now().Unix()
		run.SaveRunMeta(meta, metaPath)
		os.Exit(1)
	}
	defer func() {
		socketServer.Stop()
		os.Remove(sockPath)
	}()

	// Send initial input if provided.
	inputText := *inputFlag
	if *inputFileFlag != "" {
		data, err := os.ReadFile(*inputFileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to read input file: %v\n", err)
			cmd.Process.Kill()
			os.Exit(1)
		}
		inputText = string(data)
	}
	if inputText != "" {
		if err := sendRPCCommand(stdinWriter, "prompt", inputText); err != nil {
			fmt.Fprintf(os.Stderr, "warn: failed to send initial input: %v\n", err)
		}
	}

	// Print run ID to stdout — caller can capture this.
	fmt.Println(id)

	// Wait for agent_end via bridge goroutine signal.
	// When the agent finishes, close stdin to trigger the RPC subprocess to exit.
	// Without this, "ai serve" blocks forever because the RPC server loops on stdin.
	go func() {
		<-agentEndCh
		stdinWriter.Close()
	}()

	// Wait for subprocess to exit.
	waitErr := cmd.Wait()

	// Determine final status.
	status := run.StatusFailed
	if waitErr == nil {
		status = run.StatusDone
	} else {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			if state, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok {
				if state.Signaled() {
					status = run.StatusKilled
				}
			}
		}
	}

	meta.Status = status
	meta.FinishedAt = time.Now().Unix()
	run.SaveRunMeta(meta, metaPath)
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

// hasAgentEndEvent checks if a raw JSON line has type "agent_end".
func hasAgentEndEvent(data []byte) bool {
	// Fast path: check for the string before full JSON parse.
	// This avoids allocation for the vast majority of events.
	s := string(data)
	if !strings.Contains(s, `"agent_end"`) {
		return false
	}
	var evt struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &evt); err != nil {
		return false
	}
	return evt.Type == "agent_end"
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

// --- runModel: watchModel + user input ---

// runModel extends the watch TUI with user input support.
// It embeds watchModel for event rendering and adds a text input
// for sending messages to the running agent via socket.
type runModel struct {
	watchModel
	sockPath    string
	proc        *os.Process
	stdinPipe   *io.PipeWriter
	meta        *run.RunMeta
	metaPath    string
	inputMode   bool // true when user is typing a message
	inputBuf    *strings.Builder
	broadcaster *run.EventBroadcaster
}

func newRunModel(
	broadcaster *run.EventBroadcaster, runID, sockPath string,
	proc *os.Process,
	stdinPipe *io.PipeWriter,
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