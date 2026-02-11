package winai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	// 	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"log/slog"

	"github.com/sminez/ad/win/pkg/ad"
	"github.com/sminez/ad/win/pkg/repl"
	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/rpc"
)

const sectionLine = "═════════════════════════════════════"

// AiInterpreter bridges win and the ai RPC process.
type AiInterpreter struct {
	*repl.BaseInterpreter
	cmdPath  string
	cmdArgs  []string
	debug    bool
	startCtx context.Context

	// adClient is the client for communicating with ad (used for minibuffer, etc.)
	adClient *ad.Client

	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	cancel  context.CancelFunc
	inProc  bool
	started bool

	mu      sync.Mutex
	stateMu sync.Mutex
	writeMu sync.Mutex

	showAssistant    bool
	showThinking     bool
	showTools        bool
	showToolsVerbose bool
	showPrefixes     bool

	currentMessageRole     string
	currentMessageStreamed bool

	availableModels       []rpc.ModelInfo
	availableSessions     []SessionMeta
	availableForkMessages []rpc.ForkMessage
	availableTreeEntries  []rpc.TreeEntry
	currentModelID        string
	currentModelProvider  string
	currentThinkingLevel  string
	busyMode              string
	autoCompactionEnabled bool
	compactionState       *rpc.CompactionState
	aiPID                 int
	aiLogPath             string
	aiWorkingDir          string
	pipelineMu            sync.Mutex
	pipeline              pipelineMetrics
	pendingSessionList    bool
	pendingSessionSwitch  string
	pendingForkList       bool
	pendingForkSelect     string
	pendingTreeList       bool
	pendingTreeSelect     string
	lastAiActivity        time.Time
	isStreaming           bool
	deferStatus           bool
	pendingStatus         []string
	pendingStateRequests  map[string]stateRequestInfo
	rpcSequence           int64
	workingDir            string
}

type stateRequestInfo struct {
	started time.Time
	show    bool
	kind    string
	quiet   bool
}

type rpcEnvelope struct {
	Type string `json:"type"`
}

type rpcResponse struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Error   string          `json:"error"`
	Data    json.RawMessage `json:"data"`
}

type assistantMessageEvent struct {
	Type         string `json:"type"`
	ContentIndex int    `json:"contentIndex"`
	Delta        string `json:"delta"`
	Content      string `json:"content"`
}

type SessionMeta struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Title        string    `json:"title"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	MessageCount int       `json:"messageCount"`
}

type agentEvent struct {
	Type                  string                 `json:"type"`
	EventAt               int64                  `json:"eventAt,omitempty"`
	Message               *agent.AgentMessage    `json:"message,omitempty"`
	Messages              []agent.AgentMessage   `json:"messages,omitempty"`
	ToolResults           []agent.AgentMessage   `json:"toolResults,omitempty"`
	ToolCallID            string                 `json:"toolCallId,omitempty"`
	ToolName              string                 `json:"toolName,omitempty"`
	Args                  map[string]interface{} `json:"args,omitempty"`
	Result                *agent.AgentMessage    `json:"result,omitempty"`
	IsError               bool                   `json:"isError,omitempty"`
	AssistantMessageEvent *assistantMessageEvent `json:"assistantMessageEvent,omitempty"`
	Compaction            *agent.CompactionInfo  `json:"compaction,omitempty"`
}

type serverStartEvent struct {
	Type  string   `json:"type"`
	Model string   `json:"model"`
	Tools []string `json:"tools"`
}

type pipelineMetrics struct {
	EventCount    int64
	EventLastType string
	EventLastAt   time.Time
	EventLastRecv time.Time
	EventLagTotal time.Duration
	EventLagMax   time.Duration
	EventLagLast  time.Duration

	WriteCount  int64
	WriteTotal  time.Duration
	WriteLast   time.Duration
	WriteMax    time.Duration
	WriteLastAt time.Time

	StdoutLastAt time.Time
	StderrLastAt time.Time
}

// NewAiInterpreter creates a new ai interpreter.
func NewAiInterpreter(cmdPath string, cmdArgs []string, debug bool) *AiInterpreter {
	interp := newBaseInterpreter(debug)
	interp.cmdPath = cmdPath
	interp.cmdArgs = cmdArgs
	return interp
}

// NewAiInterpreterWithIO creates an interpreter backed by in-process IO pipes.
func NewAiInterpreterWithIO(stdin io.WriteCloser, stdout io.ReadCloser, stderr io.ReadCloser, debug bool) *AiInterpreter {
	interp := newBaseInterpreter(debug)
	interp.inProc = true
	interp.stdin = stdin
	interp.stdout = stdout
	interp.stderr = stderr
	return interp
}

func newBaseInterpreter(debug bool) *AiInterpreter {
	return &AiInterpreter{
		BaseInterpreter:      repl.NewBaseInterpreter(true),
		debug:                debug,
		showAssistant:        true,
		showThinking:         true,
		showTools:            true,
		showToolsVerbose:     false,
		showPrefixes:         true,
		busyMode:             "steer",
		pendingStateRequests: make(map[string]stateRequestInfo),
	}
}

// SetAdClient sets the ad client for minibuffer interactions.
func (p *AiInterpreter) SetAdClient(client *ad.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.adClient = client
}

// Start starts the ai subprocess and begins streaming output.
func (p *AiInterpreter) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.started {
		return fmt.Errorf("ai already started")
	}

	p.startCtx = ctx

	childCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	if p.inProc {
		if p.stdin == nil || p.stdout == nil {
			return fmt.Errorf("ai pipes not configured")
		}
		p.started = true
		if wd, err := os.Getwd(); err == nil {
			p.stateMu.Lock()
			p.workingDir = wd
			p.stateMu.Unlock()
			if p.debug {
				slog.Info("[AI-START] Working directory", "dir", wd)
			}
		}
		if p.debug {
			slog.Info("[AI-START] ai started in-process")
		}
		go p.readStdout(childCtx)
		if p.stderr != nil {
			go p.readStderr(childCtx)
		}
		return nil
	}

	args := append([]string{}, p.cmdArgs...)
	if !hasFlag(args, "-http") && !hasFlag(args, "--http") {
		args = append(args, "-http", ":6060")
	}
	cmd := exec.Command(p.cmdPath, args...)

	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	p.cmd = cmd
	p.stdin = stdin
	p.stdout = stdout
	p.stderr = stderr
	p.started = true

	slog.Info("[AI-CMD]", "cmd", p.cmd.String())
	if err := p.cmd.Start(); err != nil {
		p.started = false
		return fmt.Errorf("start ai: %w", err)
	}

	if wd, err := os.Getwd(); err == nil {
		p.stateMu.Lock()
		p.workingDir = wd
		p.stateMu.Unlock()
		if p.debug {
			slog.Info("[AI-START] Working directory", "dir", wd)
		}
	}

	if p.debug {
		slog.Info("[AI-START] ai started with PID", "pid", p.cmd.Process.Pid)
	}

	go p.readStdout(childCtx)
	go p.readStderr(childCtx)
	go p.waitForExit(cmd)

	return nil
}

func hasFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

// Stop terminates the ai subprocess.
func (p *AiInterpreter) Stop() error {
	p.mu.Lock()
	cmd := p.cmd
	stdin := p.stdin
	stdout := p.stdout
	stderr := p.stderr
	cancel := p.cancel
	inProc := p.inProc
	p.cmd = nil
	p.stdin = nil
	p.stdout = nil
	p.stderr = nil
	p.cancel = nil
	p.started = false
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	if stdin != nil {
		_ = stdin.Close()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	if stderr != nil {
		_ = stderr.Close()
	}

	if !inProc && cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil {
			return fmt.Errorf("kill ai: %w", err)
		}
	}

	p.resetAiState()
	return nil
}

func (p *AiInterpreter) waitForExit(cmd *exec.Cmd) {
	err := cmd.Wait()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	if !p.resetProcessForCmd(cmd, "process exited") {
		return
	}

	if p.debug {
		if err != nil {
			slog.Info("[AI-EXIT]", "code", exitCode, "error", err)
		} else {
			slog.Info("[AI-EXIT]", "code", exitCode)
		}
	}

	msg := "ai: process exited"
	if exitCode >= 0 {
		msg = fmt.Sprintf("ai: process exited (code=%d)", exitCode)
	}
	p.writeStatus(msg)
}

func (p *AiInterpreter) resetProcess(reason string) {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil {
		return
	}
	_ = p.resetProcessForCmd(cmd, reason)
}

func (p *AiInterpreter) resetProcessForCmd(cmd *exec.Cmd, reason string) bool {
	p.mu.Lock()
	if p.cmd != cmd {
		p.mu.Unlock()
		return false
	}
	stdin := p.stdin
	stdout := p.stdout
	stderr := p.stderr
	cancel := p.cancel
	p.started = false
	p.cmd = nil
	p.stdin = nil
	p.stdout = nil
	p.stderr = nil
	p.cancel = nil
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	if stderr != nil {
		_ = stderr.Close()
	}

	p.resetAiState()

	if p.debug && reason != "" {
		slog.Info("[AI-RESET]", "reason", reason)
	}
	return true
}

func (p *AiInterpreter) resetAiState() {
	p.stateMu.Lock()
	p.aiPID = 0
	p.aiLogPath = ""
	p.aiWorkingDir = ""
	p.pendingStateRequests = make(map[string]stateRequestInfo)
	p.stateMu.Unlock()

	p.setStreaming(false)
}

func (p *AiInterpreter) restartAI(reason string) error {
	p.mu.Lock()
	inProc := p.inProc
	p.mu.Unlock()
	if inProc {
		return fmt.Errorf("ai in-process session cannot be restarted")
	}

	if reason != "" {
		p.writeStatus(fmt.Sprintf("ai: %s; restarting...", reason))
	}

	p.mu.Lock()
	ctx := p.startCtx
	p.mu.Unlock()
	if ctx == nil || ctx.Err() != nil {
		return fmt.Errorf("ai not running")
	}

	if err := p.Start(ctx); err != nil {
		if strings.Contains(err.Error(), "ai already started") {
			return nil
		}
		return err
	}
	return nil
}

// Process sends input to ai or handles control commands.
func (p *AiInterpreter) Process(ctx context.Context, input string) error {
	raw := input
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	handled, err := p.handleInput(trimmed, false)
	if err != nil {
		p.writeStatus(fmt.Sprintf("ai: %v", err))
		return nil
	}
	if handled {
		return nil
	}

	p.stateMu.Lock()
	streaming := p.isStreaming
	busyMode := p.busyMode
	p.stateMu.Unlock()

	if streaming {
		switch busyMode {
		case "follow-up":
			if err := p.sendMessageCommand("follow_up", raw); err != nil {
				p.writeStatus(fmt.Sprintf("ai: %v", err))
			}
			return nil
		case "reject":
			p.writeStatus("ai: agent is busy")
			return nil
		default: // "steer"
			if err := p.sendMessageCommand("steer", raw); err != nil {
				p.writeStatus(fmt.Sprintf("ai: %v", err))
			}
			return nil
		}
	}

	if err := p.sendPrompt(raw); err != nil {
		p.writeStatus(fmt.Sprintf("ai: %v", err))
	}
	return nil
}

// SendInput sends input directly to the ai process (async interpreter).
func (p *AiInterpreter) SendInput(input string) error {
	return p.sendPrompt(input)
}

// HandleControl intercepts control commands from send-to-win.
func (p *AiInterpreter) HandleControl(input string) (bool, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return false, nil
	}
	return p.handleInput(trimmed, true)
}

// SetOutputWriter sets the output writer for streaming output.
func (p *AiInterpreter) SetOutputWriter(writer repl.OutputWriter) {
	p.BaseInterpreter.SetOutputWriter(writer)
}

func (p *AiInterpreter) handleInput(input string, fromControl bool) (bool, error) {
	if strings.HasPrefix(input, "::rpc") {
		return true, p.handleRawRPC(input)
	}

	if strings.HasPrefix(input, "/") {
		cmdLine := strings.TrimSpace(strings.TrimPrefix(input, "/"))
		p.stateMu.Lock()
		if fromControl && p.isStreaming {
			p.deferStatus = true
		}
		p.stateMu.Unlock()
		handled, err := p.handleCommand(cmdLine, fromControl)
		if fromControl {
			p.stateMu.Lock()
			if !p.isStreaming {
				p.deferStatus = false
			}
			p.stateMu.Unlock()
		}
		return handled, err
	}

	return false, nil
}

func (p *AiInterpreter) handleModelSelect() (bool, error) {
	// Send request to get available models
	if err := p.sendCommand("get_available_models", nil, ""); err != nil {
		return false, err
	}

	// Wait for the models to arrive
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			p.writeStatus("ai: timeout waiting for model list")
			return false, nil
		case <-ticker.C:
			p.stateMu.Lock()
			models := append([]rpc.ModelInfo(nil), p.availableModels...)
			p.stateMu.Unlock()

			if len(models) > 0 {
				// Convert models to selection format
				options := make([]string, len(models))
				for i, m := range models {
					ref := fmt.Sprintf("%s/%s", m.Provider, m.ID)
					name := m.Name
					if name == "" {
						name = m.ID
					}
					options[i] = fmt.Sprintf("%s - %s", ref, name)
				}

				// Use ad's minibuffer for interactive selection
				if p.adClient != nil {
					selection, err := p.adClient.MinibufferSelect("Select model:", options)
					if err != nil {
						p.writeStatus(fmt.Sprintf("ai: model selection error: %v", err))
						return true, nil
					}
					if selection != "" {
						// Parse the selection - it should be one of the model references
						// The selection format is "provider/id - name"
						parts := strings.SplitN(selection, " - ", 2)
						if len(parts) > 0 {
							modelRef := strings.TrimSpace(parts[0])
							if err := p.setModelFromInput(modelRef); err != nil {
								p.writeStatus(fmt.Sprintf("ai: %v", err))
							}
						}
					}
					return true, nil
				}

				// Fall back to showing the list
				p.showModelList(models, true)
				return true, nil
			}
		}
	}
}

func (p *AiInterpreter) handleCommand(cmdLine string, fromControl bool) (bool, error) {
	if cmdLine == "" {
		return false, nil
	}

	fields := strings.Fields(cmdLine)
	if len(fields) == 0 {
		return false, nil
	}

	cmd := fields[0]
	args := strings.TrimSpace(strings.TrimPrefix(cmdLine, cmd))

	switch cmd {
	case "help":
		p.showHelp(fromControl)
		return true, nil
	case "quit":
		p.writeStatus("ai: quitting")
		os.Exit(0)
		return true, nil
	case "abort":
		return true, p.sendCommand("abort", nil, "")
	case "session":
		return true, p.sendStateRequest(true, "session", false)
	case "ping":
		return true, p.sendStateRequest(false, "ping", false)
	case "messages":
		return true, p.sendCommand("get_messages", nil, "")
	case "tree":
		if strings.TrimSpace(args) == "" {
			p.pendingTreeList = true
			return true, p.sendCommand("get_tree", nil, "")
		}
		if err := p.resumeOnBranchFromInput(strings.TrimSpace(args)); err != nil {
			if errors.Is(err, errTreeListRequired) {
				p.pendingTreeSelect = strings.TrimSpace(args)
				return true, p.sendCommand("get_tree", nil, "")
			}
			p.writeStatusMaybeDefer(fromControl, fmt.Sprintf("ai: %v", err))
			return true, nil
		}
		return true, nil
	case "commands":
		return true, p.sendCommand("get_commands", nil, "")
	case "show":
		return true, p.handleShow(args, fromControl)
	case "thinking":
		return true, p.handleToggle("thinking", args, fromControl)
	case "tools":
		return true, p.handleTools(args, fromControl)
	case "prefix":
		return true, p.handleToggle("prefix", args, fromControl)
	case "model-select":
		return p.handleModelSelect()
	case "new":
		data := map[string]any{}
		if strings.TrimSpace(args) != "" {
			data["name"] = strings.TrimSpace(args)
			data["title"] = strings.TrimSpace(args)
		}
		return true, p.sendCommand("new_session", data, "")
	case "resume":
		if strings.TrimSpace(args) == "" {
			p.pendingSessionList = true
			return true, p.sendCommand("list_sessions", nil, "")
		}
		if err := p.switchSessionFromInput(strings.TrimSpace(args)); err != nil {
			if errors.Is(err, errSessionListRequired) {
				p.pendingSessionSwitch = strings.TrimSpace(args)
				return true, p.sendCommand("list_sessions", nil, "")
			}
			p.writeStatusMaybeDefer(fromControl, fmt.Sprintf("ai: %v", err))
			return true, nil
		}
		return true, nil
	case "compact":
		return true, p.sendCommand("compact", nil, "")
	case "copy":
		return true, p.sendCommand("get_last_assistant_text", nil, "")
	case "auto-compaction":
		return true, p.handleAutoCompaction(args, fromControl)
	case "thinking-level":
		return true, p.handleThinkingLevel(args, fromControl)
	case "cycle-thinking-level":
		return true, p.sendCommand("cycle_thinking_level", nil, "")
	case "fork":
		return true, p.handleFork(args)
	case "resume-on-branch":
		if strings.TrimSpace(args) == "" {
			p.writeStatusMaybeDefer(fromControl, "ai: usage: /resume-on-branch <index|entry-id>")
			return true, nil
		}
		if err := p.resumeOnBranchFromInput(strings.TrimSpace(args)); err != nil {
			if errors.Is(err, errTreeListRequired) {
				p.pendingTreeSelect = strings.TrimSpace(args)
				return true, p.sendCommand("get_tree", nil, "")
			}
			p.writeStatusMaybeDefer(fromControl, fmt.Sprintf("ai: %v", err))
			return true, nil
		}
		return true, nil
	case "steer":
		if strings.TrimSpace(args) == "" {
			p.writeStatusMaybeDefer(fromControl, "ai: usage: /steer <message>")
			return true, nil
		}
		return true, p.sendMessageCommand("steer", strings.TrimSpace(args))
	case "follow-up", "followup":
		if strings.TrimSpace(args) == "" {
			p.writeStatusMaybeDefer(fromControl, "ai: usage: /follow-up <message>")
			return true, nil
		}
		return true, p.sendMessageCommand("follow_up", strings.TrimSpace(args))
	case "busy-mode":
		mode := strings.TrimSpace(args)
		if mode == "" {
			p.writeStatusMaybeDefer(fromControl, "ai: usage: /busy-mode <steer|follow-up|reject>")
			return true, nil
		}
		switch mode {
		case "steer", "follow-up", "reject":
			p.stateMu.Lock()
			p.busyMode = mode
			p.stateMu.Unlock()
			p.writeStatusMaybeDefer(fromControl, fmt.Sprintf("ai: busy-mode %s", mode))
			return true, nil
		default:
			p.writeStatusMaybeDefer(fromControl, "ai: usage: /busy-mode <steer|follow-up|reject>")
			return true, nil
		}
	default:
		return false, nil
	}
}

var errModelListRequired = errors.New("model list required")
var errSessionListRequired = errors.New("session list required")
var errForkListRequired = errors.New("fork list required")
var errTreeListRequired = errors.New("tree list required")

func (p *AiInterpreter) handleRawRPC(input string) error {
	payload := strings.TrimSpace(strings.TrimPrefix(input, "::rpc"))
	if payload == "" {
		return fmt.Errorf("::rpc requires JSON payload")
	}
	if !json.Valid([]byte(payload)) {
		return fmt.Errorf("invalid JSON payload")
	}
	return p.sendRaw(payload)
}

func (p *AiInterpreter) sendPrompt(message string) error {
	return p.sendCommand("prompt", nil, message)
}

func (p *AiInterpreter) sendCommand(cmdType string, data any, message string) error {
	_, err := p.sendCommandWithID(cmdType, data, message)
	return err
}

func (p *AiInterpreter) sendCommandWithID(cmdType string, data any, message string) (string, error) {
	payload := map[string]any{
		"type": cmdType,
	}
	if message != "" {
		payload["message"] = message
	}
	if data != nil {
		payload["data"] = data
	}
	id := p.nextID()
	payload["id"] = id
	return id, p.sendJSON(payload)
}

func (p *AiInterpreter) sendMessageCommand(cmdType, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("empty message")
	}
	return p.sendCommand(cmdType, map[string]any{"message": message}, "")
}

func (p *AiInterpreter) sendStateRequest(show bool, kind string, quiet bool) error {
	id, err := p.sendCommandWithID("get_state", nil, "")
	if err != nil {
		return err
	}
	p.stateMu.Lock()
	p.pendingStateRequests[id] = stateRequestInfo{
		started: time.Now(),
		show:    show,
		kind:    kind,
		quiet:   quiet,
	}
	p.stateMu.Unlock()
	return nil
}

func (p *AiInterpreter) sendRaw(jsonLine string) error {
	for attempt := 0; attempt < 2; attempt++ {
		p.mu.Lock()
		stdin := p.stdin
		if stdin == nil {
			p.mu.Unlock()
			if attempt == 0 {
				if err := p.restartAI("stdin unavailable"); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("ai stdin not available")
		}

		if _, err := stdin.Write([]byte(jsonLine)); err != nil {
			p.mu.Unlock()
			if attempt == 0 && isClosedPipe(err) {
				p.resetProcess("stdin closed")
				if err := p.restartAI("stdin closed"); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("write stdin: %w", err)
		}
		if !strings.HasSuffix(jsonLine, "\n") {
			if _, err := stdin.Write([]byte("\n")); err != nil {
				p.mu.Unlock()
				if attempt == 0 && isClosedPipe(err) {
					p.resetProcess("stdin closed")
					if err := p.restartAI("stdin closed"); err != nil {
						return err
					}
					continue
				}
				return fmt.Errorf("write stdin newline: %w", err)
			}
		}
		p.mu.Unlock()
		return nil
	}
	return fmt.Errorf("ai stdin not available")
}

func (p *AiInterpreter) sendJSON(payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	return p.sendRaw(string(data))
}

func isClosedPipe(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "file already closed") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "closed pipe")
}

func (p *AiInterpreter) nextID() string {
	seq := atomic.AddInt64(&p.rpcSequence, 1)
	return fmt.Sprintf("%d", seq)
}

func (p *AiInterpreter) handleShow(args string, fromControl bool) error {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		p.writeStatusMaybeDefer(fromControl, "ai: usage: /show settings|usage")
		return nil
	}
	switch parts[0] {
	case "settings":
		p.showSettings(fromControl)
	case "pipeline":
		p.showPipeline(fromControl)
	case "usage":
		return p.sendCommand("get_session_stats", nil, "")
	default:
		p.writeStatusMaybeDefer(fromControl, "ai: usage: /show settings|pipeline|usage")
	}
	return nil
}

func (p *AiInterpreter) handleToggle(kind, args string, fromControl bool) error {
	mode := strings.TrimSpace(args)
	if mode == "" {
		mode = "toggle"
	}
	var value bool
	var ok bool

	p.stateMu.Lock()
	switch kind {
	case "thinking":
		value = p.showThinking
	case "prefix":
		value = p.showPrefixes
	default:
		p.stateMu.Unlock()
		return fmt.Errorf("unknown toggle: %s", kind)
	}
	p.stateMu.Unlock()

	switch mode {
	case "on":
		value = true
		ok = true
	case "off":
		value = false
		ok = true
	case "toggle":
		value = !value
		ok = true
	}

	if !ok {
		p.writeStatusMaybeDefer(fromControl, fmt.Sprintf("ai: usage: /%s [on|off|toggle]", kind))
		return nil
	}

	p.stateMu.Lock()
	switch kind {
	case "thinking":
		p.showThinking = value
	case "prefix":
		p.showPrefixes = value
	}
	p.stateMu.Unlock()

	p.writeStatusMaybeDefer(fromControl, fmt.Sprintf("ai: %s %s", kind, onOff(value)))
	return nil
}

func (p *AiInterpreter) handleTools(args string, fromControl bool) error {
	mode := strings.TrimSpace(args)
	if mode == "" {
		mode = "toggle"
	}

	p.stateMu.Lock()
	showTools := p.showTools
	showVerbose := p.showToolsVerbose
	p.stateMu.Unlock()

	switch mode {
	case "on":
		showTools = true
		showVerbose = false
	case "off":
		showTools = false
		showVerbose = false
	case "verbose":
		showTools = true
		showVerbose = true
	case "toggle":
		if showTools {
			showTools = false
			showVerbose = false
		} else {
			showTools = true
			showVerbose = false
		}
	default:
		p.writeStatusMaybeDefer(fromControl, "ai: usage: /tools <off|on|verbose|toggle>")
		return nil
	}

	p.stateMu.Lock()
	p.showTools = showTools
	p.showToolsVerbose = showVerbose
	p.stateMu.Unlock()

	p.writeStatusMaybeDefer(fromControl, fmt.Sprintf("ai: tools %s", toolsMode(showTools, showVerbose)))
	return nil
}

func (p *AiInterpreter) handleAutoCompaction(args string, fromControl bool) error {
	mode := strings.TrimSpace(args)
	if mode == "" {
		p.writeStatusMaybeDefer(fromControl, "ai: usage: /auto-compaction <on|off>")
		return nil
	}
	var enabled bool
	switch mode {
	case "on":
		enabled = true
	case "off":
		enabled = false
	default:
		p.writeStatusMaybeDefer(fromControl, "ai: usage: /auto-compaction <on|off>")
		return nil
	}
	return p.sendCommand("set_auto_compaction", map[string]any{"enabled": enabled}, "")
}

func (p *AiInterpreter) handleThinkingLevel(args string, fromControl bool) error {
	level := strings.TrimSpace(args)
	if level == "" {
		p.writeStatusMaybeDefer(fromControl, "ai: usage: /thinking-level <off|minimal|low|medium|high|xhigh>")
		return nil
	}
	return p.sendCommand("set_thinking_level", map[string]any{"level": level}, "")
}

func (p *AiInterpreter) handleFork(args string) error {
	arg := strings.TrimSpace(args)
	if arg == "" {
		p.pendingForkList = true
		return p.sendCommand("get_fork_messages", nil, "")
	}
	entryID, err := p.resolveForkInput(arg)
	if err != nil {
		if errors.Is(err, errForkListRequired) {
			p.pendingForkSelect = arg
			return p.sendCommand("get_fork_messages", nil, "")
		}
		p.writeStatus(fmt.Sprintf("ai: %v", err))
		return nil
	}
	return p.sendCommand("fork", map[string]any{"entryId": entryID}, "")
}

func (p *AiInterpreter) resolveForkInput(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("missing fork entry id")
	}
	if idx, err := strconv.Atoi(input); err == nil {
		p.stateMu.Lock()
		defer p.stateMu.Unlock()
		if len(p.availableForkMessages) == 0 {
			return "", errForkListRequired
		}
		if idx < 0 || idx >= len(p.availableForkMessages) {
			return "", fmt.Errorf("fork index out of range")
		}
		return p.availableForkMessages[idx].EntryID, nil
	}
	return input, nil
}

func (p *AiInterpreter) resolveTreeInput(input string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("missing tree entry id")
	}
	if strings.EqualFold(input, "root") {
		return "root", nil
	}
	if idx, err := strconv.Atoi(input); err == nil {
		p.stateMu.Lock()
		defer p.stateMu.Unlock()
		if len(p.availableTreeEntries) == 0 {
			return "", errTreeListRequired
		}
		if idx < 0 || idx >= len(p.availableTreeEntries) {
			return "", fmt.Errorf("tree index out of range")
		}
		return p.availableTreeEntries[idx].EntryID, nil
	}
	return input, nil
}

func (p *AiInterpreter) resumeOnBranchFromInput(input string) error {
	entryID, err := p.resolveTreeInput(input)
	if err != nil {
		return err
	}
	return p.sendCommand("resume_on_branch", map[string]any{"entryId": entryID}, "")
}

func (p *AiInterpreter) setModelFromInput(input string) error {
	model, err := p.resolveModelInput(strings.TrimSpace(input))
	if err != nil {
		return err
	}
	return p.sendCommand("set_model", map[string]any{"provider": model.Provider, "modelId": model.ID}, "")
}

func (p *AiInterpreter) resolveModelInput(input string) (*rpc.ModelInfo, error) {
	if input == "" {
		return nil, fmt.Errorf("missing model id")
	}

	p.stateMu.Lock()
	models := append([]rpc.ModelInfo(nil), p.availableModels...)
	currentProvider := p.currentModelProvider
	p.stateMu.Unlock()

	if idx, err := strconv.Atoi(input); err == nil {
		if len(models) == 0 {
			return nil, errModelListRequired
		}
		if idx < 0 || idx >= len(models) {
			return nil, fmt.Errorf("model index out of range")
		}
		return &models[idx], nil
	}

	if strings.Contains(input, "/") {
		parts := strings.SplitN(input, "/", 2)
		if parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid model: %s", input)
		}
		return &rpc.ModelInfo{Provider: parts[0], ID: parts[1]}, nil
	}

	if len(models) == 0 {
		if currentProvider != "" {
			return &rpc.ModelInfo{Provider: currentProvider, ID: input}, nil
		}
		return nil, errModelListRequired
	}

	for _, m := range models {
		if m.ID == input || m.Name == input {
			return &m, nil
		}
	}

	if currentProvider != "" {
		return &rpc.ModelInfo{Provider: currentProvider, ID: input}, nil
	}
	return nil, fmt.Errorf("unknown model: %s", input)
}

func (p *AiInterpreter) switchSessionFromInput(input string) error {
	if input == "" {
		return fmt.Errorf("missing session id")
	}

	p.stateMu.Lock()
	sessions := append([]SessionMeta(nil), p.availableSessions...)
	p.stateMu.Unlock()

	if idx, err := strconv.Atoi(input); err == nil {
		if len(sessions) == 0 {
			return errSessionListRequired
		}
		if idx < 0 || idx >= len(sessions) {
			return fmt.Errorf("session index out of range")
		}
		return p.sendCommand("switch_session", map[string]any{"id": sessions[idx].ID}, "")
	}

	return p.sendCommand("switch_session", map[string]any{"id": input}, "")
}

func (p *AiInterpreter) readStdout(ctx context.Context) {
	slog.Debug("[AI-STDOUT] reader started")
	scanner := bufio.NewScanner(p.stdout)
	buf := make([]byte, 0, 1024*1024)
	scanner.Buffer(buf, 64*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		p.noteStdout()
		p.handleStdoutLine([]byte(line))

		select {
		case <-ctx.Done():
			return
		default:
		}
	}

	if err := scanner.Err(); err != nil {
		slog.Error("[AI-STDOUT] scanner error", "error", err)
	}
}

func (p *AiInterpreter) readStderr(ctx context.Context) {
	scanner := bufio.NewScanner(p.stderr)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		p.noteStderr()
		if p.debug {
			slog.Info("[AI-STDERR]", "line", line)
		}

		select {
		case <-ctx.Done():
			return
		default:
		}
	}

	if err := scanner.Err(); err != nil && p.debug {
		slog.Error("[AI-STDERR] scanner error", "error", err)
	}
}

func (p *AiInterpreter) handleStdoutLine(line []byte) {
	var env rpcEnvelope
	if err := json.Unmarshal(line, &env); err != nil {
		slog.Warn("[AI-STDOUT] invalid JSON", "json", string(line))
		p.writeStatus(fmt.Sprintf("ai: %s", string(line)))
		return
	}

	if env.Type == "response" {
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			p.writeStatus(fmt.Sprintf("ai: invalid response: %v", err))
			return
		}
		p.handleResponse(resp)
		return
	}

	p.handleEvent(line)
}

func (p *AiInterpreter) handleResponse(resp rpcResponse) {
	if !resp.Success {
		if resp.Command == "get_state" {
			info, ok := p.takeStateRequestInfo(resp.ID)
			if ok && !info.quiet {
				p.writeStatus(fmt.Sprintf("ai: ping failed: %s", resp.Error))
			}
		}
		if resp.Error == "" {
			p.writeStatus(fmt.Sprintf("ai: %s failed", resp.Command))
			return
		}
		p.writeStatus(fmt.Sprintf("ai: %s failed: %s", resp.Command, resp.Error))
		return
	}

	switch resp.Command {
	case "get_available_models":
		p.handleAvailableModels(resp.Data)
	case "set_model":
		p.handleSetModel(resp.Data)
	case "get_state":
		p.handleStateResponse(resp)
	case "get_session_stats":
		p.handleSessionStats(resp.Data)
	case "get_commands":
		p.handleCommands(resp.Data)
	case "get_messages":
		p.handleMessages(resp.Data)
	case "new_session":
		p.handleNewSession(resp.Data)
	case "list_sessions":
		p.handleListSessions(resp.Data)
	case "switch_session":
		p.writeStatus("ai: session switched")
	case "set_auto_compaction":
		p.writeStatus("ai: auto-compaction updated")
	case "set_thinking_level":
		p.handleThinkingLevelResponse(resp.Data)
	case "cycle_thinking_level":
		p.handleThinkingLevelResponse(resp.Data)
	case "get_last_assistant_text":
		p.handleLastAssistantText(resp.Data)
	case "get_fork_messages":
		p.handleForkMessages(resp.Data)
	case "fork":
		p.handleForkResult(resp.Data)
	case "get_tree":
		p.handleTreeEntries(resp.Data)
	case "resume_on_branch":
		p.writeStatus("ai: branch updated")
	case "compact":
		p.handleCompactResult(resp.Data)
	case "prompt":
		// Silently ignore prompt success - we'll see the results via events
	case "abort":
		p.writeStatus("ai: aborted")
	}
}

func (p *AiInterpreter) handleEvent(line []byte) {
	var evt agentEvent
	if err := json.Unmarshal(line, &evt); err != nil {
		if p.debug {
			slog.Warn("[AI-EVENT] invalid event", "error", err)
		}
		return
	}
	p.recordEventMetrics(evt)

	switch evt.Type {
	case "server_start":
		var start serverStartEvent
		if err := json.Unmarshal(line, &start); err == nil {
			p.stateMu.Lock()
			if start.Model != "" {
				p.currentModelID = start.Model
			}
			p.stateMu.Unlock()
		}
	case "agent_start":
		p.setStreaming(true)
		p.noteAiActivity()
	case "agent_end":
		p.setStreaming(false)
		p.noteAiActivity()
	case "turn_start":
		p.noteAiActivity()
	case "turn_end":
		p.noteAiActivity()
	case "message_start":
		p.handleMessageStart(evt)
	case "message_update":
		p.handleMessageUpdate(evt)
	case "message_end":
		p.handleMessageEnd(evt)
	case "tool_execution_start":
		p.handleToolStart(evt)
	case "tool_execution_end":
		p.handleToolEnd(evt)
	case "text_delta":
		if evt.Message != nil {
			p.writeStream("assistant", evt.Message.ExtractText(), p.showAssistant)
		}
	case "thinking_delta":
		if evt.Message != nil {
			p.writeStream("thinking", evt.Message.ExtractThinking(), p.showThinking)
		}
	case "compaction_start":
		p.handleCompactionEvent(true, evt.Compaction)
	case "compaction_end":
		p.handleCompactionEvent(false, evt.Compaction)
	case "tool_call_delta":
		// ignore
	default:
		slog.Debug("[AI-EVENT]", "line", string(line))
	}
}

func (p *AiInterpreter) handleMessageStart(evt agentEvent) {
	p.stateMu.Lock()
	p.currentMessageRole = ""
	p.currentMessageStreamed = false
	p.stateMu.Unlock()
	p.noteAiActivity()
}

func (p *AiInterpreter) handleMessageUpdate(evt agentEvent) {
	if evt.AssistantMessageEvent == nil {
		return
	}
	ame := evt.AssistantMessageEvent

	p.stateMu.Lock()
	showAssistant := p.showAssistant
	showThinking := p.showThinking
	p.stateMu.Unlock()

	switch ame.Type {
	case "text_delta":
		p.writeStream("assistant", ame.Delta, showAssistant)
	case "thinking_delta":
		p.writeStream("thinking", ame.Delta, showThinking)
	case "text_end":
		// no-op
	case "toolcall_delta":
		// no-op
	}
	p.noteAiActivity()
}

func (p *AiInterpreter) handleMessageEnd(evt agentEvent) {
	if evt.Message != nil {
		p.writeMessageIfEmpty(evt.Message)
	}
	p.endStream(true)
	p.noteAiActivity()
}

func (p *AiInterpreter) handleCompactionEvent(start bool, info *agent.CompactionInfo) {
	label := "compaction"
	if info != nil && info.Auto {
		label = "auto-compaction"
	}
	if start {
		if info != nil && info.Before > 0 {
			p.writeStatus(fmt.Sprintf("ai: %s started (%d messages)", label, info.Before))
		} else {
			p.writeStatus(fmt.Sprintf("ai: %s started", label))
		}
		return
	}
	if info != nil && info.Error != "" {
		p.writeStatus(fmt.Sprintf("ai: %s failed: %s", label, info.Error))
		return
	}
	if info != nil && info.Before > 0 && info.After > 0 {
		p.writeStatus(fmt.Sprintf("ai: %s done (%d -> %d messages)", label, info.Before, info.After))
		return
	}
	p.writeStatus(fmt.Sprintf("ai: %s done", label))
}

func (p *AiInterpreter) handleToolStart(evt agentEvent) {
	p.stateMu.Lock()
	showTools := p.showTools
	showVerbose := p.showToolsVerbose
	p.stateMu.Unlock()
	if !showTools {
		return
	}
	p.endStream(false)
	label := "tool"
	if evt.ToolName != "" {
		label = fmt.Sprintf("tool %s", evt.ToolName)
	}
	if showVerbose {
		args := ""
		if len(evt.Args) > 0 {
			encoded, _ := json.Marshal(evt.Args)
			args = string(encoded)
		}
		if args != "" {
			p.writePrefixedLine("tool", fmt.Sprintf("%s args: %s", label, args))
		} else {
			p.writePrefixedLine("tool", fmt.Sprintf("%s start", label))
		}
		return
	}

	summary := summarizeToolArgs(evt.ToolName, evt.Args)
	if summary != "" {
		p.writePrefixedLine("tool", fmt.Sprintf("%s start (%s)", label, summary))
	} else {
		p.writePrefixedLine("tool", fmt.Sprintf("%s start", label))
	}
}

func (p *AiInterpreter) handleToolEnd(evt agentEvent) {
	p.stateMu.Lock()
	showTools := p.showTools
	showVerbose := p.showToolsVerbose
	p.stateMu.Unlock()
	if !showTools {
		return
	}
	p.endStream(false)
	label := "tool"
	if evt.ToolName != "" {
		label = fmt.Sprintf("tool %s", evt.ToolName)
	}
	if showVerbose {
		text := ""
		if evt.Result != nil {
			text = renderMessageText(evt.Result)
		}
		if text == "" {
			text = "(no output)"
		}
		if evt.IsError {
			p.writePrefixedLine("tool", fmt.Sprintf("%s error: %s", label, text))
		} else {
			p.writePrefixedLine("tool", fmt.Sprintf("%s result: %s", label, text))
		}
		p.scrollToBottom()
		return
	}

	if evt.IsError {
		msg := "error"
		if evt.Result != nil {
			errText := renderMessageText(evt.Result)
			if errText != "" {
				msg = fmt.Sprintf("error: %s", truncate(errText, 200))
			}
		}
		p.writePrefixedLine("tool", fmt.Sprintf("%s %s", label, msg))
	} else {
		p.writePrefixedLine("tool", fmt.Sprintf("%s done", label))
	}
	p.scrollToBottom()
}

func (p *AiInterpreter) writeMessageIfEmpty(msg *agent.AgentMessage) {
	p.stateMu.Lock()
	streamed := p.currentMessageStreamed
	showThinking := p.showThinking
	showTools := p.showTools
	showAssistant := p.showAssistant
	p.stateMu.Unlock()

	if streamed {
		return
	}

	content := renderMessageContent(msg, showThinking, showTools)
	if content == "" {
		return
	}
	role := strings.TrimSpace(msg.Role)
	if role == "" {
		role = "assistant"
	}
	enabled := true
	switch role {
	case "assistant":
		enabled = showAssistant
	case "thinking":
		enabled = showThinking
	case "tool":
		enabled = showTools
	}
	p.writeStream(role, content, enabled)
}

func renderMessageContent(msg *agent.AgentMessage, showThinking bool, showTools bool) string {
	if msg == nil {
		return ""
	}
	var b strings.Builder
	for _, block := range msg.Content {
		switch v := block.(type) {
		case agent.TextContent:
			b.WriteString(v.Text)
		case agent.ThinkingContent:
			if showThinking {
				b.WriteString(v.Thinking)
			}
		case agent.ToolCallContent:
			if showTools {
				b.WriteString(fmt.Sprintf("[toolcall %s]", v.Name))
			}
		case agent.ImageContent:
			// ignore images in text output
		}
	}
	return b.String()
}

func renderMessageText(msg *agent.AgentMessage) string {
	if msg == nil {
		return ""
	}
	text := msg.ExtractText()
	if text != "" {
		return text
	}
	return renderMessageContent(msg, true, true)
}

func (p *AiInterpreter) writeStream(role, text string, enabled bool) {
	if !enabled || text == "" {
		return
	}

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	p.stateMu.Lock()
	currentRole := p.currentMessageRole
	streamed := p.currentMessageStreamed
	showPrefixes := p.showPrefixes
	p.stateMu.Unlock()

	if currentRole != role {
		if streamed {
			p.writeRaw("\n")
		}
		if showPrefixes {
			p.writeRaw(fmt.Sprintf("%s: ", role))
		}
		p.stateMu.Lock()
		p.currentMessageRole = role
		p.currentMessageStreamed = true
		p.stateMu.Unlock()
	} else if !streamed && showPrefixes {
		p.writeRaw(fmt.Sprintf("%s: ", role))
		p.stateMu.Lock()
		p.currentMessageStreamed = true
		p.stateMu.Unlock()
	}

	p.writeRaw(text)
}

func (p *AiInterpreter) endStream(scroll bool) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	p.stateMu.Lock()
	streamed := p.currentMessageStreamed
	p.currentMessageRole = ""
	p.currentMessageStreamed = false
	p.stateMu.Unlock()

	if streamed {
		p.writeRaw("\n")
	}
	if scroll {
		p.scrollToBottom()
	}
}

func (p *AiInterpreter) writePrefixedLine(role, text string) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	p.stateMu.Lock()
	showPrefixes := p.showPrefixes
	p.stateMu.Unlock()

	if showPrefixes {
		p.writeRaw(fmt.Sprintf("%s: %s\n", role, text))
		return
	}
	p.writeRaw(text + "\n")
}

func (p *AiInterpreter) writeStatus(text string) {
	p.stateMu.Lock()
	deferStatus := p.deferStatus
	streaming := p.isStreaming
	if deferStatus && streaming {
		slog.Debug("[AI-STATUS] deferred", "bytes", len(text))
		p.pendingStatus = append(p.pendingStatus, text)
		p.stateMu.Unlock()
		return
	}
	p.stateMu.Unlock()

	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	line := text
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	p.writeRaw(line)
	p.scrollToBottom()
}

func (p *AiInterpreter) writeStatusMaybeDefer(fromControl bool, text string) {
	if !fromControl {
		p.writeStatus(text)
		return
	}
	p.writeStatus(text)
}

func (p *AiInterpreter) setStreaming(streaming bool) {
	var pending []string
	p.stateMu.Lock()
	p.isStreaming = streaming
	if !streaming && len(p.pendingStatus) > 0 {
		pending = append([]string(nil), p.pendingStatus...)
		p.pendingStatus = nil
	}
	if !streaming {
		p.deferStatus = false
	}
	p.stateMu.Unlock()

	for _, msg := range pending {
		p.writeStatus(msg)
	}
}

func (p *AiInterpreter) writeRaw(text string) {
	writer := p.GetOutputWriter()
	if writer == nil {
		slog.Warn("[AI-OUTPUT] writer is nil", "bytes", len(text))
		return
	}
	if text == "" {
		return
	}
	if !utf8.ValidString(text) {
		text = strings.ToValidUTF8(text, "\uFFFD")
	}
	const maxChunkSize = 4096
	data := []byte(text)
	for len(data) > 0 {
		chunkSize := len(data)
		if chunkSize > maxChunkSize {
			chunkSize = maxChunkSize
		}
		if chunkSize < len(data) {
			for chunkSize > 0 && !utf8.Valid(data[:chunkSize]) {
				chunkSize--
			}
			if chunkSize == 0 {
				_, size := utf8.DecodeRune(data)
				if size <= 0 {
					size = 1
				}
				chunkSize = size
			}
		}
		chunk := string(data[:chunkSize])
		start := time.Now()
		if err := writer.Write(chunk); err != nil && p.debug {
			slog.Error("[AI-OUTPUT] write failed", "error", err, "bytes", len(chunk))
		}
		p.recordWriteMetrics(time.Since(start))
		data = data[chunkSize:]
	}
}

func (p *AiInterpreter) scrollToBottom() {
	writer := p.GetOutputWriter()
	if writer == nil {
		return
	}
	_ = writer.ScrollToBottom()
}

func (p *AiInterpreter) recordEventMetrics(evt agentEvent) {
	recvAt := time.Now()
	eventAt := recvAt
	if evt.EventAt > 0 {
		eventAt = time.Unix(0, evt.EventAt)
	}
	lag := recvAt.Sub(eventAt)

	p.pipelineMu.Lock()
	p.pipeline.EventCount++
	p.pipeline.EventLastType = evt.Type
	p.pipeline.EventLastAt = eventAt
	p.pipeline.EventLastRecv = recvAt
	p.pipeline.EventLagLast = lag
	p.pipeline.EventLagTotal += lag
	if lag > p.pipeline.EventLagMax {
		p.pipeline.EventLagMax = lag
	}
	p.pipelineMu.Unlock()
}

func (p *AiInterpreter) recordWriteMetrics(d time.Duration) {
	now := time.Now()
	p.pipelineMu.Lock()
	p.pipeline.WriteCount++
	p.pipeline.WriteTotal += d
	p.pipeline.WriteLast = d
	if d > p.pipeline.WriteMax {
		p.pipeline.WriteMax = d
	}
	p.pipeline.WriteLastAt = now
	p.pipelineMu.Unlock()
}

func (p *AiInterpreter) noteStdout() {
	now := time.Now()
	p.pipelineMu.Lock()
	p.pipeline.StdoutLastAt = now
	p.pipelineMu.Unlock()
}

func (p *AiInterpreter) noteStderr() {
	now := time.Now()
	p.pipelineMu.Lock()
	p.pipeline.StderrLastAt = now
	p.pipelineMu.Unlock()
}

func (p *AiInterpreter) showHelp(fromControl bool) {
	p.writeStatusMaybeDefer(fromControl, `Commands:
  /help
  /session
  /messages
  /tree
  /resume-on-branch <index|entry-id>
  /commands
  /show settings
  /show pipeline
  /show usage
  /thinking [on|off|toggle]
  /tools [off|on|verbose|toggle]
  /prefix [on|off|toggle]
  /model-select
  /new [name]
  /resume [id|path|index]
  /compact
  /copy
  /auto-compaction <on|off>
  /thinking-level <off|minimal|low|medium|high|xhigh>
  /fork [entry-id|index]
  /steer <message>
  /follow-up <message>
  /busy-mode <steer|follow-up|reject>
  /abort
  /quit`)
}

func (p *AiInterpreter) showSettings(fromControl bool) {
	p.stateMu.Lock()
	showThinking := p.showThinking
	showTools := p.showTools
	showToolsVerbose := p.showToolsVerbose
	showPrefixes := p.showPrefixes
	modelID := p.currentModelID
	modelProvider := p.currentModelProvider
	thinkingLevel := p.currentThinkingLevel
	autoCompact := p.autoCompactionEnabled
	busyMode := p.busyMode
	compaction := p.compactionState
	p.stateMu.Unlock()

	model := modelID
	if modelProvider != "" {
		model = fmt.Sprintf("%s/%s", modelProvider, modelID)
	}

	compactionContext := "unknown"
	compactionReserve := "unknown"
	compactionLimit := "unknown"
	compactionMaxMessages := "disabled"
	compactionMaxTokens := "disabled"
	compactionKeepRecent := "unknown"
	compactionKeepRecentTokens := "unknown"
	if compaction != nil {
		compactionContext = formatIntOrUnknown(compaction.ContextWindow)
		compactionReserve = formatIntOrUnknown(compaction.ReserveTokens)
		compactionLimit = formatTokenLimit(compaction)
		compactionMaxMessages = formatLimit(compaction.MaxMessages)
		compactionMaxTokens = formatLimit(compaction.MaxTokens)
		compactionKeepRecent = formatIntOrUnknown(compaction.KeepRecent)
		compactionKeepRecentTokens = formatIntOrUnknown(compaction.KeepRecentTokens)
	}

	p.writeStatusMaybeDefer(fromControl, fmt.Sprintf(`Display Settings:
  model: %s
  thinking: %s
  tools: %s
  prefix: %s
  thinking-level: %s
  busy-mode: %s
  auto-compaction: %s
  compaction-context-window: %s
  compaction-reserve-tokens: %s
  compaction-token-limit: %s
  compaction-max-messages: %s
  compaction-max-tokens: %s
  compaction-keep-recent: %s
  compaction-keep-recent-tokens: %s`,
		model,
		onOff(showThinking),
		toolsMode(showTools, showToolsVerbose),
		onOff(showPrefixes),
		orUnknown(thinkingLevel),
		orUnknown(busyMode),
		onOff(autoCompact),
		compactionContext,
		compactionReserve,
		compactionLimit,
		compactionMaxMessages,
		compactionMaxTokens,
		compactionKeepRecent,
		compactionKeepRecentTokens,
	))
}

func (p *AiInterpreter) showPipeline(fromControl bool) {
	p.pipelineMu.Lock()
	pm := p.pipeline
	p.pipelineMu.Unlock()

	avgLag := time.Duration(0)
	if pm.EventCount > 0 {
		avgLag = pm.EventLagTotal / time.Duration(pm.EventCount)
	}
	avgWrite := time.Duration(0)
	if pm.WriteCount > 0 {
		avgWrite = pm.WriteTotal / time.Duration(pm.WriteCount)
	}
	stdoutIdle := time.Duration(0)
	if !pm.StdoutLastAt.IsZero() {
		stdoutIdle = time.Since(pm.StdoutLastAt)
	}
	stderrIdle := time.Duration(0)
	if !pm.StderrLastAt.IsZero() {
		stderrIdle = time.Since(pm.StderrLastAt)
	}
	eventIdle := time.Duration(0)
	if !pm.EventLastRecv.IsZero() {
		eventIdle = time.Since(pm.EventLastRecv)
	}
	writeIdle := time.Duration(0)
	if !pm.WriteLastAt.IsZero() {
		writeIdle = time.Since(pm.WriteLastAt)
	}

	p.writeStatusMaybeDefer(fromControl, fmt.Sprintf(`Pipeline Metrics:
  events: count=%d lastType=%s lastLag=%s avgLag=%s maxLag=%s
  events: lastEventAt=%s lastRecvAt=%s idle=%s
  writes: count=%d last=%s avg=%s max=%s lastAt=%s idle=%s
  io: lastStdout=%s idle=%s lastStderr=%s idle=%s`,
		pm.EventCount,
		orUnknown(pm.EventLastType),
		formatDuration(pm.EventLagLast),
		formatDuration(avgLag),
		formatDuration(pm.EventLagMax),
		formatTime(pm.EventLastAt),
		formatTime(pm.EventLastRecv),
		formatDuration(eventIdle),
		pm.WriteCount,
		formatDuration(pm.WriteLast),
		formatDuration(avgWrite),
		formatDuration(pm.WriteMax),
		formatTime(pm.WriteLastAt),
		formatDuration(writeIdle),
		formatTime(pm.StdoutLastAt),
		formatDuration(stdoutIdle),
		formatTime(pm.StderrLastAt),
		formatDuration(stderrIdle),
	))
}

func (p *AiInterpreter) handleAvailableModels(data json.RawMessage) {
	var payload struct {
		Models []rpc.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		p.writeStatus(fmt.Sprintf("ai: invalid models response: %v", err))
		return
	}

	p.stateMu.Lock()
	p.availableModels = payload.Models
	if p.currentModelProvider == "" && p.currentModelID != "" {
		for _, model := range payload.Models {
			if model.ID == p.currentModelID {
				p.currentModelProvider = model.Provider
				break
			}
		}
	}
	p.stateMu.Unlock()
}

func (p *AiInterpreter) showModelList(models []rpc.ModelInfo, showUsage bool) {
	if len(models) == 0 {
		p.writeStatus("ai: no models available")
		return
	}

	maxID := 0
	for _, m := range models {
		id := fmt.Sprintf("%s/%s", m.Provider, m.ID)
		if len(id) > maxID {
			maxID = len(id)
		}
	}

	p.writeRaw(sectionLine + "\n")
	p.writeRaw("Available Models\n")
	p.writeRaw(sectionLine + "\n\n")

	p.stateMu.Lock()
	currentID := p.currentModelID
	currentProvider := p.currentModelProvider
	p.stateMu.Unlock()

	for i, m := range models {
		id := fmt.Sprintf("%s/%s", m.Provider, m.ID)
		name := m.Name
		if name == "" {
			name = m.ID
		}
		current := ""
		if m.ID == currentID && m.Provider == currentProvider {
			current = " [current]"
		}
		line := fmt.Sprintf("%d: %-*s - %s%s\n", i, maxID, id, name, current)
		p.writeRaw(line)
	}

	p.writeRaw("\n" + sectionLine + "\n")
	if showUsage {
		p.writeRaw(`
Usage:
  - Visual select a model line above
  - Press: <space> p m to set selected model
  - Or type: /model <number|provider/model-id>
`)
	}
	p.scrollToBottom()
}

func (p *AiInterpreter) handleSetModel(data json.RawMessage) {
	var model rpc.ModelInfo
	if err := json.Unmarshal(data, &model); err != nil {
		p.writeStatus(fmt.Sprintf("ai: invalid set_model response: %v", err))
		return
	}

	p.stateMu.Lock()
	p.currentModelID = model.ID
	p.currentModelProvider = model.Provider
	p.stateMu.Unlock()

	label := model.ID
	if model.Provider != "" {
		label = fmt.Sprintf("%s/%s", model.Provider, model.ID)
	}
	p.writeStatus(fmt.Sprintf("ai: model set to %s", label))
}

func (p *AiInterpreter) handleStateResponse(resp rpcResponse) {
	info, ok := p.takeStateRequestInfo(resp.ID)
	state, err := p.decodeState(resp.Data)
	if err != nil {
		p.writeStatus(fmt.Sprintf("ai: invalid state response: %v", err))
		return
	}
	p.applyState(state)

	if ok {
		if info.show {
			p.showState(state)
			return
		}
		p.reportStatePing(info, state)
		return
	}

	p.showState(state)
}

func (p *AiInterpreter) decodeState(data json.RawMessage) (*rpc.SessionState, error) {
	var state rpc.SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (p *AiInterpreter) applyState(state *rpc.SessionState) {
	if state == nil {
		return
	}
	p.stateMu.Lock()
	if state.Model != nil {
		p.currentModelID = state.Model.ID
		p.currentModelProvider = state.Model.Provider
	}
	p.currentThinkingLevel = state.ThinkingLevel
	p.autoCompactionEnabled = state.AutoCompactionEnabled
	p.isStreaming = state.IsStreaming
	p.compactionState = state.Compaction
	p.aiPID = state.AIPid
	p.aiLogPath = state.AILogPath
	p.aiWorkingDir = state.AIWorkingDir
	p.stateMu.Unlock()
}

func (p *AiInterpreter) showState(state *rpc.SessionState) {
	if state == nil {
		return
	}
	model := "unknown"
	if state.Model != nil {
		model = state.Model.ID
		if state.Model.Provider != "" {
			model = fmt.Sprintf("%s/%s", state.Model.Provider, state.Model.ID)
		}
	}

	compactionContext := "unknown"
	compactionLimit := "unknown"
	compactionReserve := "unknown"
	compactionKeepRecent := "unknown"
	compactionKeepRecentTokens := "unknown"
	aiPID := "unknown"
	aiLogPath := "unknown"
	aiWorkingDir := "unknown"
	if state.Compaction != nil {
		compactionContext = formatIntOrUnknown(state.Compaction.ContextWindow)
		compactionLimit = formatTokenLimit(state.Compaction)
		compactionReserve = formatIntOrUnknown(state.Compaction.ReserveTokens)
		compactionKeepRecent = formatIntOrUnknown(state.Compaction.KeepRecent)
		compactionKeepRecentTokens = formatIntOrUnknown(state.Compaction.KeepRecentTokens)
	}
	if state.AIPid > 0 {
		aiPID = formatIntOrUnknown(state.AIPid)
	}
	if strings.TrimSpace(state.AILogPath) != "" {
		aiLogPath = state.AILogPath
	}
	if strings.TrimSpace(state.AIWorkingDir) != "" {
		aiWorkingDir = state.AIWorkingDir
	}

	p.writeStatus(fmt.Sprintf(`Session:
  id: %s
  name: %s
  file: %s
  ai-pid: %s
  ai-log: %s
  ai-cwd: %s
  model: %s
  context-window: %s
  compaction-limit: %s
  compaction-reserve: %s
  compaction-keep-recent: %s
  compaction-keep-recent-tokens: %s
  thinking-level: %s
  auto-compaction: %s
  messages: %d
  pending: %d
  streaming: %s
  compacting: %s`,
		orUnknown(state.SessionID),
		orUnknown(state.SessionName),
		orUnknown(state.SessionFile),
		aiPID,
		orUnknown(aiLogPath),
		orUnknown(aiWorkingDir),
		model,
		compactionContext,
		compactionLimit,
		compactionReserve,
		compactionKeepRecent,
		compactionKeepRecentTokens,
		orUnknown(state.ThinkingLevel),
		onOff(state.AutoCompactionEnabled),
		state.MessageCount,
		state.PendingMessageCount,
		onOff(state.IsStreaming),
		onOff(state.IsCompacting),
	))
}

func (p *AiInterpreter) takeStateRequestInfo(id string) (stateRequestInfo, bool) {
	if strings.TrimSpace(id) == "" {
		return stateRequestInfo{}, false
	}
	p.stateMu.Lock()
	info, ok := p.pendingStateRequests[id]
	if ok {
		delete(p.pendingStateRequests, id)
	}
	p.stateMu.Unlock()
	return info, ok
}

func (p *AiInterpreter) reportStatePing(info stateRequestInfo, state *rpc.SessionState) {
	latency := time.Since(info.started)

	if info.quiet {
		return
	}

	label := "pong"
	streaming := "unknown"
	compacting := "unknown"
	pending := 0
	if state != nil {
		streaming = onOff(state.IsStreaming)
		compacting = onOff(state.IsCompacting)
		pending = state.PendingMessageCount
	}
	p.writeStatus(fmt.Sprintf("ai: %s %s (streaming=%s compacting=%s pending=%d)",
		label, latency.Round(time.Millisecond), streaming, compacting, pending))
}

func (p *AiInterpreter) handleSessionStats(data json.RawMessage) {
	var stats rpc.SessionStats
	if err := json.Unmarshal(data, &stats); err != nil {
		p.writeStatus(fmt.Sprintf("ai: invalid usage response: %v", err))
		return
	}

	p.writeStatus(fmt.Sprintf(`Usage:
  session: %s
  messages: %d (user %d, assistant %d)
  tools: %d calls, %d results
  tokens: in %d, out %d, cache read %d, cache write %d, total %d
  cost: %.4f`,
		orUnknown(stats.SessionID),
		stats.TotalMessages,
		stats.UserMessages,
		stats.AssistantMessages,
		stats.ToolCalls,
		stats.ToolResults,
		stats.Tokens.Input,
		stats.Tokens.Output,
		stats.Tokens.CacheRead,
		stats.Tokens.CacheWrite,
		stats.Tokens.Total,
		stats.Cost,
	))
}

func (p *AiInterpreter) handleCommands(data json.RawMessage) {
	var payload struct {
		Commands []rpc.SlashCommand `json:"commands"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		p.writeStatus(fmt.Sprintf("ai: invalid commands response: %v", err))
		return
	}

	commands := payload.Commands
	if len(commands) == 0 {
		p.writeStatus("ai: no commands available")
		return
	}

	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Source == commands[j].Source {
			return commands[i].Name < commands[j].Name
		}
		return commands[i].Source < commands[j].Source
	})

	var b strings.Builder
	b.WriteString("Commands:\n")
	for _, cmd := range commands {
		desc := strings.TrimSpace(cmd.Description)
		if desc != "" {
			b.WriteString(fmt.Sprintf("  [%s] %s - %s\n", cmd.Source, cmd.Name, desc))
		} else {
			b.WriteString(fmt.Sprintf("  [%s] %s\n", cmd.Source, cmd.Name))
		}
	}
	p.writeStatus(strings.TrimRight(b.String(), "\n"))
}

func (p *AiInterpreter) handleMessages(data json.RawMessage) {
	if p.debug {
		writerOK := p.GetOutputWriter() != nil
		p.stateMu.Lock()
		streaming := p.isStreaming
		deferStatus := p.deferStatus
		p.stateMu.Unlock()
		slog.Info("[AI] handleMessages",
			"dataBytes", len(data),
			"writer", writerOK,
			"streaming", streaming,
			"deferStatus", deferStatus,
		)
	}
	var payload struct {
		Messages []agent.AgentMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		// get_messages may return a raw array in some versions
		var messages []agent.AgentMessage
		if err2 := json.Unmarshal(data, &messages); err2 != nil {
			p.writeStatus(fmt.Sprintf("ai: invalid messages response: %v", err))
			return
		}
		payload.Messages = messages
	}

	if len(payload.Messages) == 0 {
		p.writeStatus("ai: no messages")
		return
	}

	const maxMessages = 10
	total := len(payload.Messages)
	display := payload.Messages
	baseIndex := 0
	if total > maxMessages {
		baseIndex = total - maxMessages
		display = payload.Messages[baseIndex:]
	}

	var b strings.Builder
	if total > maxMessages {
		b.WriteString(fmt.Sprintf("Messages (last %d of %d):\n", maxMessages, total))
	} else {
		b.WriteString("Messages:\n")
	}
	for i, msg := range display {
		text := strings.TrimSpace(renderMessageText(&msg))
		if text == "" {
			text = "(no text)"
		}
		text = truncate(text, 120)
		b.WriteString(fmt.Sprintf("  [%d] %s: %s\n", baseIndex+i, msg.Role, text))
	}
	p.writeStatus(strings.TrimRight(b.String(), "\n"))
}

func (p *AiInterpreter) handleNewSession(data json.RawMessage) {
	var payload struct {
		SessionID string `json:"sessionId"`
		Cancelled bool   `json:"cancelled"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		p.writeStatus(fmt.Sprintf("ai: invalid new_session response: %v", err))
		return
	}
	if payload.Cancelled {
		p.writeStatus("ai: new session cancelled")
		return
	}
	p.writeStatus(fmt.Sprintf("ai: new session %s", payload.SessionID))
}

func (p *AiInterpreter) handleListSessions(data json.RawMessage) {
	var payload struct {
		Sessions []SessionMeta `json:"sessions"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		// list_sessions returns a raw array in some versions
		var sessions []SessionMeta
		if err2 := json.Unmarshal(data, &sessions); err2 != nil {
			p.writeStatus(fmt.Sprintf("ai: invalid sessions response: %v", err))
			return
		}
		payload.Sessions = sessions
	}

	p.stateMu.Lock()
	p.availableSessions = payload.Sessions
	pendingList := p.pendingSessionList
	pendingSwitch := p.pendingSessionSwitch
	p.pendingSessionList = false
	p.pendingSessionSwitch = ""
	p.stateMu.Unlock()

	if pendingSwitch != "" {
		if err := p.switchSessionFromInput(pendingSwitch); err != nil {
			p.writeStatus(fmt.Sprintf("ai: %v", err))
		}
		return
	}

	if !pendingList {
		return
	}

	if len(payload.Sessions) == 0 {
		p.writeStatus("ai: no sessions found")
		return
	}

	p.writeRaw(sectionLine + "\n")
	p.writeRaw("Available Sessions\n")
	p.writeRaw(sectionLine + "\n\n")

	sort.Slice(payload.Sessions, func(i, j int) bool {
		return payload.Sessions[i].UpdatedAt.After(payload.Sessions[j].UpdatedAt)
	})

	for i, sess := range payload.Sessions {
		name := sess.Name
		if name == "" {
			name = sess.ID
		}
		updated := sess.UpdatedAt.Format("2006-01-02 15:04")
		p.writeRaw(fmt.Sprintf("%d: %s (id: %s)\n", i, name, sess.ID))
		p.writeRaw(fmt.Sprintf("    updated: %s  messages: %d\n", updated, sess.MessageCount))
	}
	p.writeRaw("\n" + sectionLine + "\n")
	p.writeRaw("Usage:\n  - /resume <index|id|path>\n")
	p.scrollToBottom()
}

func (p *AiInterpreter) handleThinkingLevelResponse(data json.RawMessage) {
	var payload struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		// Some responses return the level as a string
		var level string
		if err2 := json.Unmarshal(data, &level); err2 != nil {
			p.writeStatus(fmt.Sprintf("ai: invalid thinking level response: %v", err))
			return
		}
		payload.Level = level
	}

	p.stateMu.Lock()
	p.currentThinkingLevel = payload.Level
	p.stateMu.Unlock()

	if payload.Level == "" {
		p.writeStatus("ai: thinking level updated")
		return
	}
	p.writeStatus(fmt.Sprintf("ai: thinking level set to %s", payload.Level))
}

func (p *AiInterpreter) handleLastAssistantText(data json.RawMessage) {
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		p.writeStatus(fmt.Sprintf("ai: invalid assistant text response: %v", err))
		return
	}
	if payload.Text == "" {
		p.writeStatus("ai: no assistant text to copy")
		return
	}
	if err := copyToClipboard(payload.Text); err != nil {
		p.writeStatus(fmt.Sprintf("ai: copy failed: %v", err))
		return
	}
	p.writeStatus("ai: copied last assistant message")
}

func (p *AiInterpreter) handleForkMessages(data json.RawMessage) {
	var payload struct {
		Messages []rpc.ForkMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		p.writeStatus(fmt.Sprintf("ai: invalid fork messages response: %v", err))
		return
	}

	p.stateMu.Lock()
	p.availableForkMessages = payload.Messages
	pendingList := p.pendingForkList
	pendingSelect := p.pendingForkSelect
	p.pendingForkList = false
	p.pendingForkSelect = ""
	p.stateMu.Unlock()

	if pendingSelect != "" {
		if entryID, err := p.resolveForkInput(pendingSelect); err == nil {
			if err := p.sendCommand("fork", map[string]any{"entryId": entryID}, ""); err != nil {
				p.writeStatus(fmt.Sprintf("ai: %v", err))
			}
		} else {
			p.writeStatus(fmt.Sprintf("ai: %v", err))
		}
		return
	}

	if !pendingList {
		return
	}

	if len(payload.Messages) == 0 {
		p.writeStatus("ai: no fork messages available")
		return
	}

	p.writeRaw(sectionLine + "\n")
	p.writeRaw("Available Messages for Forking\n")
	p.writeRaw(sectionLine + "\n\n")

	for i, msg := range payload.Messages {
		text := truncate(strings.TrimSpace(msg.Text), 120)
		p.writeRaw(fmt.Sprintf("[%d] %s\n", i, text))
		p.writeRaw(fmt.Sprintf("    Entry ID: %s\n\n", msg.EntryID))
	}
	p.writeRaw(sectionLine + "\n")
	p.writeRaw("Usage:\n  - /fork <index|entry-id>\n")
	p.scrollToBottom()
}

func (p *AiInterpreter) handleTreeEntries(data json.RawMessage) {
	var payload struct {
		Entries []rpc.TreeEntry `json:"entries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		var entries []rpc.TreeEntry
		if err2 := json.Unmarshal(data, &entries); err2 != nil {
			p.writeStatus(fmt.Sprintf("ai: invalid tree response: %v", err))
			return
		}
		payload.Entries = entries
	}

	p.stateMu.Lock()
	p.availableTreeEntries = payload.Entries
	pendingList := p.pendingTreeList
	pendingSelect := p.pendingTreeSelect
	p.pendingTreeList = false
	p.pendingTreeSelect = ""
	p.stateMu.Unlock()

	if pendingSelect != "" {
		if entryID, err := p.resolveTreeInput(pendingSelect); err == nil {
			if err := p.sendCommand("resume_on_branch", map[string]any{"entryId": entryID}, ""); err != nil {
				p.writeStatus(fmt.Sprintf("ai: %v", err))
			}
		} else {
			p.writeStatus(fmt.Sprintf("ai: %v", err))
		}
		return
	}

	if !pendingList {
		return
	}

	if len(payload.Entries) == 0 {
		p.writeStatus("ai: no tree entries available")
		return
	}

	p.writeRaw(sectionLine + "\n")
	p.writeRaw("Session Tree\n")
	p.writeRaw(sectionLine + "\n\n")

	for i, entry := range payload.Entries {
		indent := strings.Repeat("  ", entry.Depth)
		label := formatTreeLabel(entry)
		if entry.Leaf {
			label = "[current] " + label
		}
		p.writeRaw(fmt.Sprintf("[%d] %s%s\n", i, indent, label))
		p.writeRaw(fmt.Sprintf("    Entry ID: %s\n\n", entry.EntryID))
	}
	p.writeRaw(sectionLine + "\n")
	p.writeRaw("Usage:\n  - /resume-on-branch <index|entry-id>\n")
	p.scrollToBottom()
}

func (p *AiInterpreter) handleForkResult(data json.RawMessage) {
	var result rpc.ForkResult
	if err := json.Unmarshal(data, &result); err != nil {
		p.writeStatus(fmt.Sprintf("ai: invalid fork response: %v", err))
		return
	}
	if result.Cancelled {
		p.writeStatus("ai: fork cancelled")
		return
	}
	if result.Text != "" {
		p.writeStatus(fmt.Sprintf("ai: forked: %s", result.Text))
		return
	}
	p.writeStatus("ai: forked")
}

func (p *AiInterpreter) handleCompactResult(data json.RawMessage) {
	var result rpc.CompactResult
	if err := json.Unmarshal(data, &result); err != nil {
		p.writeStatus("ai: compacted")
		return
	}

	if result.Summary != "" {
		p.writeStatus(fmt.Sprintf("ai: compacted\nSummary:\n%s", result.Summary))
		return
	}
	p.writeStatus("ai: compacted")
}

func (p *AiInterpreter) noteAiActivity() {
	p.stateMu.Lock()
	p.lastAiActivity = time.Now()
	p.stateMu.Unlock()
}

func onOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func toolsMode(showTools, showVerbose bool) string {
	if !showTools {
		return "off"
	}
	if showVerbose {
		return "verbose"
	}
	return "on"
}

func orUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	return d.String()
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format(time.RFC3339Nano)
}

func formatIntOrUnknown(value int) string {
	if value <= 0 {
		return "unknown"
	}
	return strconv.Itoa(value)
}

func formatLimit(value int) string {
	if value <= 0 {
		return "disabled"
	}
	return strconv.Itoa(value)
}

func formatTokenLimit(state *rpc.CompactionState) string {
	if state == nil || state.TokenLimit <= 0 {
		return "unknown"
	}
	source := formatTokenLimitSource(state.TokenLimitSource)
	if source == "" {
		return strconv.Itoa(state.TokenLimit)
	}
	return fmt.Sprintf("%d (%s)", state.TokenLimit, source)
}

func formatTokenLimitSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "context_window":
		return "context-window"
	case "max_tokens":
		return "max-tokens"
	case "none":
		return ""
	default:
		return strings.TrimSpace(value)
	}
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit-3] + "..."
}

func formatTreeLabel(entry rpc.TreeEntry) string {
	label := entry.Type
	if strings.TrimSpace(entry.Role) != "" {
		label = entry.Role
	}
	text := strings.TrimSpace(entry.Text)
	if text == "" {
		return label
	}
	return fmt.Sprintf("%s: %s", label, truncate(text, 120))
}

func summarizeToolArgs(toolName string, args map[string]interface{}) string {
	if len(args) == 0 {
		return ""
	}
	type candidate struct {
		key   string
		label string
	}
	candidates := []candidate{
		{key: "command", label: "command"},
		{key: "cmd", label: "command"},
		{key: "path", label: "path"},
		{key: "file", label: "file"},
		{key: "pattern", label: "pattern"},
		{key: "query", label: "query"},
	}
	for _, c := range candidates {
		if value, ok := args[c.key]; ok {
			if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
				return fmt.Sprintf("%s=%s", c.label, truncate(s, 120))
			}
		}
	}

	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("args=%s", strings.Join(keys, ","))
}

func copyToClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return err
	}
	if _, err := io.WriteString(stdin, text); err != nil {
		stdin.Close()
		return err
	}
	if err := stdin.Close(); err != nil {
		return err
	}
	return cmd.Wait()
}

// func (p *AiInterpreter) resolveWorkingDir(path string) string {
// 	if path == "" {
// 		return ""
// 	}
// 	if strings.HasPrefix(path, "~") {
// 		if home, err := os.UserHomeDir(); err == nil {
// 			path = filepath.Join(home, strings.TrimPrefix(path, "~"))
// 		}
// 	}
// 	abs, err := filepath.Abs(path)
// 	if err != nil {
// 		return path
// 	}
// 	return abs
// }

var _ repl.Interpreter = (*AiInterpreter)(nil)
var _ repl.AsyncInterpreter = (*AiInterpreter)(nil)
var _ repl.ControlInterpreter = (*AiInterpreter)(nil)
