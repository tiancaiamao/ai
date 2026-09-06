package run

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tiancaiamao/ai/pkg/app"
	"github.com/tiancaiamao/ai/pkg/protocol"
	"github.com/tiancaiamao/ai/pkg/transport"
	"github.com/tiancaiamao/ai/subcommand/helpers"
	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

// ServeConfig holds the shared configuration for `ai serve`, `ai run` and
// `ai acp`.
type ServeConfig struct {
	Session      string
	SystemPrompt string
	MaxTurns     int
	Timeout      time.Duration
	HTTP         string
	Name         string
	Role         string
	Model        string
	AgentConfig  string // path to agent.yaml; takes precedence over Role
	IDFile       string // write run ID here once the ACP server is live
}

// ServeSubcommand starts the agent in this process. The ACP server runs over
// a transport.Hub served on a unix socket, so local clients (`ai watch`,
// `ai send`, the `ai run` TUI) attach via ACP while the control client keeps
// one long-lived connection for the initial prompt and the events.jsonl
// mirror.
//
// Use "ai serve &" or "nohup ai serve &" for background operation.
func ServeSubcommand(binPath string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	sessionFlag := fs.String("session", "", "Session file path")
	systemPromptFlag := fs.String("system-prompt", "", "Custom system prompt (forwarded to agent)")
	maxTurnsFlag := fs.Int("max-turns", 0, "Maximum conversation turns (0 = unlimited)")
	timeoutFlag := fs.Duration("timeout", 0, "Total execution timeout (0 = unlimited)")
	httpFlag := fs.String("http", "", "HTTP debug server address (e.g. ':6060')")
	inputFlag := fs.String("input", "", "Initial prompt to send after startup")
	inputFileFlag := fs.String("input-file", "", "Read initial prompt from file (avoids OS ARG_MAX limits)")
	nameFlag := fs.String("name", "", "Human-readable name for the run")
	roleFlag := fs.String("role", "", "Agent role name (e.g. coder, orchestrator, validator). Loads ~/.ai/roles/<name>/agent.yaml")
	idFileFlag := fs.String("id-file", "", "Write run ID to this file after startup (useful for background mode)")
	modelFlag := fs.String("model", "", "Override LLM model ID (e.g. claude-sonnet-4-20250514)")
	fs.Parse(os.Args[1:])

	// Daemon mode: detach from the terminal with a new process group.
	if err := syscall.Setpgid(0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "warn: failed to set process group: %v\n", err)
	}

	sp := startServeApp(ServeConfig{
		Session:      *sessionFlag,
		SystemPrompt: *systemPromptFlag,
		MaxTurns:     *maxTurnsFlag,
		Timeout:      *timeoutFlag,
		HTTP:         *httpFlag,
		Name:         *nameFlag,
		Role:         *roleFlag,
		Model:        *modelFlag,
		IDFile:       *idFileFlag,
	})
	defer sp.Close()

	// Send the initial prompt, if any. This must happen after
	// startServeApp returns: the signal handler installed there may call
	// control.Cancel concurrently with this PromptAsync, and the client's
	// internal lock serializes them safely, but the handler must exist first.
	inputText := *inputFlag
	if *inputFileFlag != "" {
		data, err := os.ReadFile(*inputFileFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to read input file: %v\n", err)
			os.Exit(1)
		}
		inputText = string(data)
	}
	if inputText != "" {
		if err := sp.control.PromptAsync(sp.sessionID, inputText); err != nil {
			fmt.Fprintf(os.Stderr, "warn: failed to send initial input: %v\n", err)
		}
	}

	// Wait for the agent to exit.
	<-sp.done

	status := sp.status
	sp.meta.Status = status
	sp.meta.FinishedAt = time.Now().Unix()
	tui.SaveRunMeta(sp.meta, sp.metaPath)

	// Exit non-zero on failure so callers (e.g. `ai run`) can detect it.
	if status == tui.StatusFailed {
		os.Exit(1)
	}
}

// StdioServe runs the same infrastructure as `ai serve` (run meta,
// events.jsonl mirror, control socket, hub) but exposes the ACP server on
// stdin/stdout as the primary peer. Used by `ai acp`: the controlling ACP
// client (Zed, agent-shell) drives the agent over stdio while the run stays
// visible to `ai ls` and attachable via `ai send` / `ai watch` /
// `ai history --id <run>` through the control socket.
//
// The process exits when the stdio peer disconnects, a termination signal
// arrives, or the agent fails; the final status is recorded in run.json.
func StdioServe(cfg ServeConfig) error {
	sp := startServeApp(cfg)
	defer sp.Close()

	// Attach the stdio peer to the hub. The hub's readLoop swallows
	// per-conn read errors, so eofConn is how we notice the client going
	// away and shut the agent down.
	sp.hub.AddConn(newEOFConn(transport.NewStdio(os.Stdin, os.Stdout), sp.cancel))

	<-sp.done

	sp.meta.Status = sp.status
	sp.meta.FinishedAt = time.Now().Unix()
	if err := tui.SaveRunMeta(sp.meta, sp.metaPath); err != nil {
		return fmt.Errorf("save run meta: %w", err)
	}

	if sp.status == tui.StatusFailed {
		return fmt.Errorf("agent failed (see %s)", filepath.Join(filepath.Dir(sp.metaPath), "error.log"))
	}
	return nil
}

// eofConn wraps a Conn and invokes onEOF (once) when the peer's read side
// returns — EOF or any read error.
type eofConn struct {
	transport.Conn
	onEOF func()
	once  sync.Once
}

func newEOFConn(c transport.Conn, onEOF func()) *eofConn {
	return &eofConn{Conn: c, onEOF: onEOF}
}

func (c *eofConn) ReadMessage() ([]byte, error) {
	msg, err := c.Conn.ReadMessage()
	if err != nil {
		c.once.Do(c.onEOF)
	}
	return msg, err
}

// serveApp holds the runtime state of the in-process ACP server.
type serveApp struct {
	meta       *tui.RunMeta
	metaPath   string
	eventsPath string
	sockPath   string
	socket     *transport.UnixSocket
	hub        *transport.Hub
	control    *protocol.ACPClient
	sessionID  string
	logFile    *os.File

	mu       sync.Mutex
	finished bool
	status   string // final status (done/killed/failed)
	done     chan struct{}
	cancel   context.CancelFunc
}

// finish records the final status and closes done exactly once.
func (sp *serveApp) finish(status string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.finished {
		return
	}
	sp.finished = true
	sp.status = status
	close(sp.done)
}

// Close releases the control connection, hub, socket and log file.
func (sp *serveApp) Close() {
	if sp.control != nil {
		sp.control.Close()
	}
	if sp.hub != nil {
		sp.hub.Close()
	}
	if sp.socket != nil {
		sp.socket.Close()
	}
	if sp.logFile != nil {
		sp.logFile.Close()
	}
}

func failServe(msg string) {
	fmt.Fprintf(os.Stderr, "error: %s\n", msg)
	os.Exit(1)
}

// startServeApp sets up the run directory, meta file, unix socket, hub and
// control client, then starts the in-process ACP server. The process exits
// when a turn completes (_turn_end) or a termination signal arrives.
func startServeApp(cfg ServeConfig) *serveApp {
	homeDir, err := os.UserHomeDir()

	if err != nil {
		failServe(fmt.Sprintf("failed to get home directory: %v", err))
	}
	baseDir := filepath.Join(homeDir, ".ai")

	// Resolve system prompt.
	sysPrompt, err := helpers.ParseSystemPrompt(cfg.SystemPrompt)
	if err != nil {
		failServe(fmt.Sprintf("invalid system prompt: %v", err))
	}
	if cfg.Role != "" {
		roleConfigPath := filepath.Join(homeDir, ".ai", "roles", cfg.Role, "agent.yaml")
		if _, err := os.Stat(roleConfigPath); err != nil {
			failServe(fmt.Sprintf("role not found: %s (path %s)", cfg.Role, roleConfigPath))
		}
	}

	// Create the run meta first so its ID is fixed before anything else
	// (run dir, socket, id file) is derived from it.
	cwd, _ := os.Getwd()
	meta, err := tui.CreateRun(baseDir, cwd, os.Getpid())
	if err != nil {
		failServe(fmt.Sprintf("failed to create run meta: %v", err))
	}
	// Let pkg/app record the active session in run.json on session
	// creation and /resume-style switches (session/load).
	app.SetRunSessionUpdater(tui.SetRunSession)
	id := meta.ID
	runDir := tui.RunDir(baseDir, id)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		failServe(fmt.Sprintf("failed to create run directory: %v", err))
	}

	// Log file for agent errors.
	logFile, err := os.Create(filepath.Join(runDir, "error.log"))
	if err != nil {
		failServe(fmt.Sprintf("failed to create log file: %v", err))
	}

	if cfg.Name != "" {
		meta.Name = cfg.Name
		if err := tui.SaveRunMeta(meta, tui.RunMetaPath(baseDir, id)); err != nil {
			failServe(fmt.Sprintf("failed to save run meta: %v", err))
		}
	}

	// Listen for ACP clients.
	sockPath := tui.SocketPath(baseDir, id)
	socket, err := transport.NewUnixSocket(sockPath)
	if err != nil {
		failServe(fmt.Sprintf("failed to create ACP socket: %v", err))
	}
	hub := transport.NewHub()
	go func() {
		for {
			conn, err := socket.Accept()
			if err != nil {
				return
			}
			hub.AddConn(conn)
		}
	}()

	sp := &serveApp{
		meta:       meta,
		metaPath:   tui.RunMetaPath(baseDir, id),
		eventsPath: tui.EventsPath(baseDir, id),
		sockPath:   sockPath,
		socket:     socket,
		hub:        hub,
		logFile:    logFile,
		done:       make(chan struct{}),
	}

	// In-process ACP server. Runs until the hub is closed (see Close) or it
	// errors out.
	ctx, cancel := context.WithCancel(context.Background())
	sp.cancel = cancel
	go func() {
		var err error
		if cfg.AgentConfig != "" {
			err = app.RunACPWithAgentConfigContext(ctx, hub, cfg.Session, cfg.HTTP, sysPrompt, cfg.MaxTurns, cfg.Timeout, cfg.AgentConfig, cfg.Model, id)
		} else {
			err = app.RunACPWithContext(ctx, hub, cfg.Session, cfg.HTTP, sysPrompt, cfg.MaxTurns, cfg.Timeout, cfg.Role, cfg.Model, id)
		}
		if err != nil {
			fmt.Fprintf(logFile, "[serve] agent error: %v\n", err)
		}
		// RunACP may fail before it starts consuming the hub (for example,
		// during agent setup). Close the listener and all accepted clients so
		// callers cannot observe a live socket with no ACP server behind it.
		_ = hub.Close()
		_ = socket.Close()

		sp.mu.Lock()
		status := sp.status
		sp.mu.Unlock()
		if status == "" {
			if err != nil {
				status = tui.StatusFailed
			} else {
				status = tui.StatusDone
			}
		}
		sp.finish(status)
	}()

	// Control client: initial prompt, signal abort and events.jsonl mirror.
	control, sessionID, err := connectACP(sockPath)
	if err != nil {
		failServe(fmt.Sprintf("failed to start agent: %v (see %s)", err, filepath.Join(runDir, "error.log")))
	}
	sp.control = control
	sp.sessionID = sessionID

	// Publish the run ID only once the ACP server is live, so callers that
	// poll this file can immediately dial the socket.
	if cfg.IDFile != "" {
		if err := os.WriteFile(cfg.IDFile, []byte(id+"\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "warn: failed to write id file: %v\n", err)
		}
	}

	// Mirror ACP updates into events.jsonl (agent_end lines, consumed by
	// `ai ls`) and finalize status when a turn ends.
	go sp.mirror()

	// Graceful shutdown: cancel the ACP context. RunACPWithContext closes
	// the transport, aborts the in-flight turn, and waits for persistence
	// cleanup before the serve goroutine marks the process done.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "[serve] received signal: %v, aborting agent\n", sig)
		sp.mu.Lock()
		if !sp.finished {
			sp.status = tui.StatusKilled
		}
		sp.mu.Unlock()
		if sp.cancel != nil {
			sp.cancel()
		}
	}()

	return sp
}

// mirror consumes the control client's ACP update stream and appends
// agent_end lines to events.jsonl — the contract `ai ls` uses for idle
// detection and result display.
//
// Serve stays alive across turns (agent_end only means the current prompt
// finished, not process exit — callers clean up with `ai kill`). Final
// status is set by the signal handler (killed) or a fatal agent error
// (failed).
func (sp *serveApp) mirror() {
	f, err := os.OpenFile(sp.eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	for u := range sp.control.Updates() {
		if u.SessionUpdate != "_turn_end" {
			continue
		}
		success := true
		errMsg := ""
		if meta, ok := u.Meta.(map[string]any); ok {
			if s, ok := meta["success"].(bool); ok {
				success = s
			}
			errMsg, _ = meta["error"].(string)
		}
		line, err := json.Marshal(map[string]any{
			"type":      "agent_end",
			"success":   success,
			"error":     errMsg,
			"timestamp": time.Now().Unix(),
		})
		if err == nil {
			f.Write(append(line, '\n'))
		}
	}
}

// --- ai run: spawn a detached serve process and attach the TUI over ACP ---

// RunSubcommand runs the agent as a detached `ai serve` subprocess and
// attaches an interactive TUI over ACP.
func RunSubcommand(binPath string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	sessionFlag := fs.String("session", "", "Session file path")
	systemPromptFlag := fs.String("system-prompt", "", "Custom system prompt (forwarded to agent)")
	maxTurnsFlag := fs.Int("max-turns", 0, "Maximum conversation turns (0 = unlimited)")
	timeoutFlag := fs.Duration("timeout", 0, "Total execution timeout (0 = unlimited)")
	httpFlag := fs.String("http", "", "HTTP debug server address (e.g. ':6060')")
	inputFlag := fs.String("input", "", "Initial prompt to send after startup")
	nameFlag := fs.String("name", "", "Human-readable name for the run")
	roleFlag := fs.String("role", "", "Agent role name (e.g. coder, orchestrator, validator). Loads ~/.ai/roles/<name>/agent.yaml")
	modelFlag := fs.String("model", "", "Override LLM model ID (e.g. claude-sonnet-4-20250514)")
	fs.Parse(os.Args[1:])

	cfg := ServeConfig{
		Session:      *sessionFlag,
		SystemPrompt: *systemPromptFlag,
		MaxTurns:     *maxTurnsFlag,
		Timeout:      *timeoutFlag,
		HTTP:         *httpFlag,
		Name:         *nameFlag,
		Role:         *roleFlag,
		Model:        *modelFlag,
	}

	// Serve writes its run ID here once the ACP server is live.
	idFile, err := os.CreateTemp("", "ai-run-*.id")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create temp file: %v\n", err)
		os.Exit(1)
	}
	idFile.Close()
	defer os.Remove(idFile.Name())

	sp, err := startServeProcess(binPath, cfg, idFile.Name())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to start serve process: %v\n", err)
		os.Exit(1)
	}
	sp.meta, err = waitForRunMeta(idFile.Name(), sp, 60*time.Second)
	if err != nil {
		sp.stop()
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Attach the TUI to the agent over ACP.
	client, sid, err := connectACP(tui.SocketPath("", sp.meta.ID))
	if err != nil {
		sp.stop()
		sp.wait()
		fmt.Fprintf(os.Stderr, "error: cannot attach to agent: %v\n", err)
		os.Exit(1)
	}

	// Send the initial prompt, if any.
	if *inputFlag != "" {
		if err := client.PromptAsync(sid, *inputFlag); err != nil {
			fmt.Fprintf(os.Stderr, "warn: failed to send initial input: %v\n", err)
		}
	}

	// Launch the TUI. It owns the client connection (closes it on exit).
	model := newRunModel(sp.meta, client, sid)
	p := tea.NewProgram(model, tea.WithAltScreen())
	go model.consumeACP(client.Updates(), p)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}

	// TUI exited — stop the serve process and wait for it to exit.
	sp.stop()
	sp.wait()

	// Report the final status (written by the serve process on exit).
	if m, err := tui.LoadRunMeta(tui.RunMetaPath("", sp.meta.ID)); err == nil && m.Status == tui.StatusFailed {
		os.Exit(1)
	}
}

// waitForRunMeta polls the id file written by the serve process, then loads
// the run meta. It fails fast if the serve process exits during startup.
func waitForRunMeta(idFile string, sp *serveProcess, timeout time.Duration) (*tui.RunMeta, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-sp.exited:
			return nil, fmt.Errorf("serve process exited during startup (see %s)", serveErrorLogHint)
		default:
		}
		data, err := os.ReadFile(idFile)
		if err == nil {
			id := strings.TrimSpace(string(data))
			if id != "" {
				meta, err := tui.LoadRunMeta(tui.RunMetaPath("", id))
				if err == nil {
					return meta, nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("timed out waiting for serve process to start (see %s)", serveErrorLogHint)
}

// serveErrorLogHint points users at where serve startup errors are logged.
var serveErrorLogHint = "~/.ai/runs/<id>/error.log"

// startServeProcess spawns a detached `ai serve` subprocess in its own
// process group.
func startServeProcess(binPath string, cfg ServeConfig, idFile string) (*serveProcess, error) {
	args := []string{"serve", "--id-file", idFile}
	if cfg.Session != "" {
		args = append(args, "--session", cfg.Session)
	}
	if cfg.SystemPrompt != "" {
		args = append(args, "--system-prompt", cfg.SystemPrompt)
	}
	if cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(cfg.MaxTurns))
	}
	if cfg.Timeout > 0 {
		args = append(args, "--timeout", cfg.Timeout.String())
	}
	if cfg.HTTP != "" {
		args = append(args, "--http", cfg.HTTP)
	}
	if cfg.Name != "" {
		args = append(args, "--name", cfg.Name)
	}
	if cfg.Role != "" {
		args = append(args, "--role", cfg.Role)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}

	cmd := exec.Command(binPath, args...)
	cmd.Stdout = nil // detached: keep the terminal clean
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	sp := &serveProcess{cmd: cmd, exited: make(chan struct{})}
	go func() {
		cmd.Wait()
		close(sp.exited)
	}()
	return sp, nil
}

// serveProcess tracks the detached serve subprocess.
type serveProcess struct {
	cmd    *exec.Cmd
	meta   *tui.RunMeta
	exited chan struct{}
}

// stop signals the serve process to shut down (SIGTERM, then SIGKILL) and
// waits for it to exit.
func (sp *serveProcess) stop() {
	if sp.cmd.Process == nil {
		return
	}
	_ = sp.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-sp.exited:
	case <-time.After(5 * time.Second):
		_ = sp.cmd.Process.Signal(syscall.SIGKILL)
	}
	<-sp.exited
}

// wait reaps the serve subprocess.
func (sp *serveProcess) wait() {
	<-sp.exited
}

// --- runModel: watchModel + user input ---

// runModel extends the watch TUI with user input support.
// It embeds watchModel for event rendering and adds a text input
// for sending messages to the agent via ACP.
type runModel struct {
	watchModel
	meta      *tui.RunMeta
	client    *protocol.ACPClient
	sessionID string
	inputMode bool // true when user is typing a message
	inputBuf  *strings.Builder
}

func newRunModel(meta *tui.RunMeta, client *protocol.ACPClient, sessionID string) runModel {
	return runModel{
		watchModel: newWatchModelForACP("ai run", meta.ID),
		meta:       meta,
		client:     client,
		sessionID:  sessionID,
		inputBuf:   &strings.Builder{},
	}
}

func (m runModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		// q/ctrl+c always quit, even mid-input.
		switch msg.String() {
		case "q", "ctrl+c":
			m.client.Close()
			return m, tea.Quit
		}

		// Handle input mode: user is typing a message.
		if m.inputMode {
			switch msg.Type {
			case tea.KeyEnter:
				// Send the message.
				text := m.inputBuf.String()
				m.inputBuf.Reset()
				m.inputMode = false
				if text != "" {
					if err := m.client.PromptAsync(m.sessionID, text); err != nil {
						m.appendContent(errStyle.Render("ai: send failed: " + err.Error()))
						m.syncIfDirty()
					} else {
						// The user text arrives via the server's
						// user_message_chunk broadcast; no local echo needed.
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

		// Normal mode: enter input mode.
		if msg.String() == "i" || msg.String() == ":" {
			m.inputMode = true
			return m, nil
		}
	}

	// Delegate to watchModel for event processing and navigation.
	// (Also closes the client when the ACP stream ends.)
	w, cmd := m.watchModel.Update(msg)
	m.watchModel = w.(watchModel)
	if _, closed := msg.(acpStreamClosedMsg); closed {
		m.client.Close()
	}
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

func connectACP(path string) (*protocol.ACPClient, string, error) {
	conn, err := transport.DialUnix(path)
	if err != nil {
		return nil, "", err
	}
	return protocol.ConnectACP(conn)
}
