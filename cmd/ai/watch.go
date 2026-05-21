package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	ansi "github.com/charmbracelet/x/ansi"

	"github.com/tiancaiamao/ai/pkg/run"
)

// --- Styles ---

var (
	metaStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
	toolStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	sessStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Underline(true)
	thinkingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("99")).Italic(true)
	aiStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
	statusBar     = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)
)

// --- Messages ---

type eventLine struct {
	line   string
	offset int64
}

type replayDone struct {
	offset int64
}

type fileChecked struct {
	offset int64
}

type errMsg struct {
	err error
}

// broadcasterEvent is a tea.Msg delivered when a new event arrives from
// the in-memory broadcaster (used by `ai run` embedded TUI).
type broadcasterEvent struct {
	line string
}

// socketEvent is a tea.Msg delivered when a new event arrives from the
// unix socket stream (used by `ai watch` connecting to `ai serve`).
type socketEvent struct {
	line string
}

// socketConnected is a tea.Msg delivered when socket stream connection is established.
type socketConnected struct{}

// socketConnectFailed is a tea.Msg delivered when socket stream connection fails.
type socketConnectFailed struct {
	err error
}

// --- Sentence buffer for typewriter effect ---

// sentenceBuffer accumulates text deltas and flushes at sentence boundaries.
type sentenceBuffer struct {
	buf       strings.Builder
	flushFunc func(text string)
	lastFlush int // buf.Len() at last flush
}

func newSentenceBuffer(flushFunc func(text string)) *sentenceBuffer {
	return &sentenceBuffer{flushFunc: flushFunc}
}

func (sb *sentenceBuffer) write(text string) {
	sb.buf.WriteString(text)
	// Flush at sentence boundaries (ASCII: .!? + space/newline; CJK: 。，！？、)
	s := sb.buf.String()
	if hasSentenceBoundary(s) {
		sb.flush()
		return
	}
	// Flush if buffer exceeds 80 chars to avoid starving the UI.
	if sb.buf.Len()-sb.lastFlush >= 80 {
		sb.flush()
	}
}

func (sb *sentenceBuffer) flush() {
	if sb.buf.Len() > 0 {
		sb.flushFunc(sb.buf.String())
		sb.lastFlush = 0
		sb.buf.Reset()
	}
}

func hasSentenceBoundary(s string) bool {
	runes := []rune(s)
	n := len(runes)
	for i, c := range runes {
		switch c {
		case '.', '!', '?':
			if i < n-1 {
				next := runes[i+1]
				if next == ' ' || next == '\n' || next == '\t' {
					return true
				}
			}
		case '。', '！', '？', '，', '、', '；', '：', '\n':
			// CJK sentence/clause boundaries — flush immediately.
			return true
		}
	}
	return false
}

// --- Model ---

type watchModel struct {
	viewport    viewport.Model
	eventsPath  string // legacy: for file-based polling (machine mode only)
	offset      int64  // legacy: current read position in events.jsonl
	content     *strings.Builder
	ready       bool
	err         error
	width       int
	height      int
	lines       int    // total lines rendered
	mode        string // "replay" or "live"
	caughtUp    bool   // true when replay phase finishes
	runID       string
	statusLine  string
	sentBuf     *sentenceBuffer
	sinceFlag   int64 // --since offset for machine-readable mode
	machineMode bool  // if true, print raw events + cursor and exit

	// Streaming state: tracks current role prefix for inline content.
	// Role prefix printed once when role changes, then text appended inline
	currentRole  string // "", "assistant", "thinking", "tool", "ai"
	inlineActive bool   // true when we're in the middle of an inline stream
	showPrefixes bool   // whether to show "role: " prefixes (default true)
	showThinking bool   // whether to show thinking content
	showTools    bool   // whether to show tool content

	// In-memory event source (used by ai run embedded TUI).
	broadcaster    *run.EventBroadcaster
	broadcasterSub *run.Consumer

	// Socket stream event source (used by ai watch connecting to ai serve).
	sockConn    net.Conn
	sockPath    string
	sockScanner *bufio.Scanner
}

func newWatchModel(eventsPath, runID string, sinceOffset int64, machineMode bool) watchModel {
	m := watchModel{
		eventsPath:   eventsPath,
		runID:        runID,
		mode:         "replay",
		statusLine:   fmt.Sprintf("ai watch | run %s | replaying...", runID),
		content:      &strings.Builder{},
		sinceFlag:    sinceOffset,
		machineMode:  machineMode,
		showPrefixes: true,
		showThinking: true,
		showTools:    true,
	}
	m.sentBuf = newSentenceBuffer(func(text string) {
		m.appendInline(text)
	})
	return m
}

// newWatchModelFromBroadcaster creates a watchModel that reads events from
// an in-memory EventBroadcaster (used by the `ai run` embedded TUI).
func newWatchModelFromBroadcaster(b *run.EventBroadcaster, runID string) watchModel {
	m := watchModel{
		runID:        runID,
		mode:         "live",
		caughtUp:     true,
		statusLine:   fmt.Sprintf("ai run | run %s | live", runID),
		content:      &strings.Builder{},
		showPrefixes: true,
		showThinking: true,
		showTools:    true,
		broadcaster:  b,
	}
	m.sentBuf = newSentenceBuffer(func(text string) {
		m.appendInline(text)
	})

	// Subscribe to broadcaster for live events only (no replay).
	if b != nil {
		m.broadcasterSub = b.Subscribe(b.Seq())
	}

	return m
}

// newWatchModelFromSocket creates a watchModel that reads events from
// a unix socket stream (used by `ai watch` connecting to `ai serve`).
func newWatchModelFromSocket(sockPath, runID string) watchModel {
	m := watchModel{
		runID:        runID,
		mode:         "live",
		caughtUp:     true,
		statusLine:   fmt.Sprintf("ai watch | run %s | connecting...", runID),
		content:      &strings.Builder{},
		showPrefixes: true,
		showThinking: true,
		showTools:    true,
		sockPath:     sockPath,
	}
	m.sentBuf = newSentenceBuffer(func(text string) {
		m.appendInline(text)
	})
	return m
}

// scrollStep is the number of columns to scroll horizontally.
const scrollStep = 6

// wrapContent wraps the raw content string to the given width,
// preserving ANSI escape codes. Each line is wrapped independently.
func wrapContent(raw string, width int) string {
	if width <= 0 {
		return raw
	}
	lines := strings.Split(raw, "\n")
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if line == "" {
			continue
		}
		b.WriteString(ansi.Wrap(line, width, ""))
	}
	return b.String()
}

// syncContent applies word-wrapping to the raw content and pushes it
// to the viewport, then scrolls to the bottom.
func (m *watchModel) syncContent() {
	if !m.ready {
		return
	}
	wrapped := wrapContent(m.content.String(), m.width)
	m.viewport.SetContent(wrapped)
	m.viewport.GotoBottom()
}

func (m *watchModel) appendContent(text string) {
	m.endInline()
	m.content.WriteString(text)
	m.content.WriteString("\n")
	m.lines++
	m.syncContent()
}

func (m *watchModel) appendInline(text string) {
	m.content.WriteString(text)
	m.syncContent()
}

// ensureRole transitions the streaming role.
// If the role changes, it ends the current inline stream, prints a newline,
// and starts a new line with the role prefix (if showPrefixes is on).
// Returns false if this role's content should be suppressed.
func (m *watchModel) ensureRole(role string) bool {
	// Check visibility
	switch role {
	case "thinking":
		if !m.showThinking {
			return false
		}
	case "tool":
		if !m.showTools {
			return false
		}
	}

	if m.currentRole == role && m.inlineActive {
		return true // same role, continue inline
	}

	// Role changed — flush any buffered text, end previous inline
	m.sentBuf.flush()
	m.endInline()

	if m.showPrefixes && role != "" {
		var styled string
		switch role {
		case "assistant":
			styled = role + ": "
		case "thinking":
			styled = thinkingStyle.Render(role) + ": "
		case "tool":
			styled = toolStyle.Render(role) + ": "
		case "ai":
			styled = aiStyle.Render(role) + ": "
		default:
			styled = role + ": "
		}
		m.content.WriteString(styled)
	}

	m.currentRole = role
	m.inlineActive = true
	return true
}

// endInline finishes the current inline stream (if any) with a newline.
func (m *watchModel) endInline() {
	if m.inlineActive {
		m.content.WriteString("\n")
		m.lines++
		m.inlineActive = false
		m.currentRole = ""
		m.syncContent()
	}
}

func (m watchModel) Init() tea.Cmd {
	if m.machineMode {
		return nil
	}

	// If using broadcaster, start polling the consumer channel.
	if m.broadcaster != nil && m.broadcasterSub != nil {
		return pollBroadcaster(m.broadcasterSub)
	}

	// If using socket stream, connect first.
	if m.sockPath != "" {
		return connectSocketStream(m.sockPath)
	}

	// Legacy: file-based polling.
	return readAllExisting(m.eventsPath, m.sinceFlag)
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "left", "h":
			m.viewport.ScrollLeft(scrollStep)
			return m, nil
		case "right", "l":
			m.viewport.ScrollRight(scrollStep)
		case "ctrl+f":
			m.viewport.PageDown()
			return m, nil
		case "ctrl+b":
			m.viewport.PageUp()
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerHeight := 1 // status bar
		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-headerHeight)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - headerHeight
		}
		// Re-wrap content to the new width.
		m.syncContent()

	case broadcasterEvent:
		// Event from in-memory broadcaster (ai run).
		formatted := run.ParseEvent(msg.line)
		if formatted == nil {
			return m, pollBroadcaster(m.broadcasterSub)
		}
		m.processEvent(formatted)
		m.updateStatus()
		return m, pollBroadcaster(m.broadcasterSub)

	case socketConnected:
		// Socket stream connected — now read events.
		// Pick up the scanner stored by connectSocketStream.
		socketStreamMu.Lock()
		m.sockConn = socketStreamConn
		m.sockScanner = socketStreamScanner
		socketStreamMu.Unlock()

		m.mode = "live"
		m.caughtUp = true
		m.updateStatus()
		return m, readSocketEvent(m.sockScanner)

	case socketConnectFailed:
		m.appendContent(errStyle.Render(fmt.Sprintf("connection failed: %v", msg.err)))
		return m, tea.Quit

	case socketEvent:
		// Event from unix socket stream (ai watch → ai serve).
		formatted := run.ParseEvent(msg.line)
		if formatted == nil {
			return m, readSocketEvent(m.sockScanner)
		}
		m.processEvent(formatted)
		m.updateStatus()
		return m, readSocketEvent(m.sockScanner)

	case replayDone:
		// Finished replaying history, switch to live mode.
		m.offset = msg.offset
		m.caughtUp = true
		m.mode = "live"
		m.updateStatus()
		// Flush any remaining buffered text.
		m.sentBuf.flush()
		return m, waitForFile(m.eventsPath, m.offset)

	case replayBatch:
		// Batch of events from replay phase — render all at full speed.
		m.offset = msg.offset
		for _, line := range msg.lines {
			m.processEvent(run.ParseEvent(line))
		}
		m.endInline()
		m.updateStatus()
		return m, m.nextCmd()

	case eventLine:
		m.offset = msg.offset
		formatted := run.ParseEvent(msg.line)
		if formatted == nil {
			return m, m.nextCmd()
		}

		m.processEvent(formatted)
		m.updateStatus()
		return m, m.nextCmd()

	case fileChecked:
		m.offset = msg.offset
		return m, waitForFile(m.eventsPath, m.offset)

	case errMsg:
		m.err = msg.err
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m *watchModel) nextCmd() tea.Cmd {
	if m.caughtUp {
		return waitForFile(m.eventsPath, m.offset)
	}
	// Still in replay mode: read as fast as possible.
	return readAllExisting(m.eventsPath, m.offset)
}

func (m *watchModel) updateStatus() {
	m.statusLine = fmt.Sprintf("ai watch | run %s | %s | %d lines", m.runID, m.mode, m.lines)
}

func (m watchModel) View() string {
	if !m.ready {
		return fmt.Sprintf("ai watch | run %s | loading...\n", m.runID)
	}
	return m.viewport.View() + "\n" + statusBar.Render(m.statusLine)
}

// --- Event source commands ---

// pollBroadcaster reads one event from the broadcaster consumer channel
// and returns it as a broadcasterEvent tea.Msg.
func pollBroadcaster(c *run.Consumer) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-c.Events()
		if !ok {
			return tea.Quit
		}
		return broadcasterEvent{line: string(event)}
	}
}

// connectSocketStream connects to the unix socket and starts streaming events.
func connectSocketStream(sockPath string) tea.Cmd {
	return func() tea.Msg {
		conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
		if err != nil {
			return socketConnectFailed{err: err}
		}

		// Send stream command.
		cmd := run.Command{Type: "stream", FromSeq: 0}
		cmdData, err := json.Marshal(cmd)
		if err != nil {
			conn.Close()
			return socketConnectFailed{err: err}
		}
		cmdData = append(cmdData, '\n')
		if _, err := conn.Write(cmdData); err != nil {
			conn.Close()
			return socketConnectFailed{err: err}
		}

		// Read initial response.
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil {
			conn.Close()
			return socketConnectFailed{err: fmt.Errorf("read stream response: %w", err)}
		}

		var resp run.Response
		if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &resp); err != nil {
			conn.Close()
			return socketConnectFailed{err: fmt.Errorf("parse stream response: %w", err)}
		}
		if !resp.OK {
			conn.Close()
			return socketConnectFailed{err: fmt.Errorf("stream rejected: %s", resp.Error)}
		}

		// Clear deadline — long-lived connection.
		conn.SetDeadline(time.Time{})

		// Store connection in a temporary that will be picked up by the model.
		// We need to thread the scanner back. Use a channel-based approach instead.
		// Actually, we'll return the scanner through the message and the model will store it.
		// But we can't modify the model in a Cmd... 
		// Let's use a different approach: store conn and scanner in a package-level var.
		socketStreamMu.Lock()
		socketStreamConn = conn
		socketStreamScanner = bufio.NewScanner(conn)
		socketStreamScanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		socketStreamMu.Unlock()

		return socketConnected{}
	}
}

// Package-level state for socket stream (set by connectSocketStream, consumed by readSocketEvent).
// This is a pragmatic approach since tea.Cmd can't modify the model directly.
var (
	socketStreamMu       sync.Mutex
	socketStreamConn     net.Conn
	socketStreamScanner  *bufio.Scanner
)

// readSocketEvent reads one event from the socket scanner.
func readSocketEvent(scanner *bufio.Scanner) tea.Cmd {
	return func() tea.Msg {
		if scanner == nil {
			return socketConnectFailed{err: fmt.Errorf("scanner not initialized")}
		}
		if scanner.Scan() {
			return socketEvent{line: scanner.Text()}
		}
		if err := scanner.Err(); err != nil {
			return socketConnectFailed{err: err}
		}
		// Stream ended — broadcaster likely shut down.
		return tea.Quit
	}
}

// --- Legacy file reading commands ---

// replayBatch is returned by readAllExisting when multiple lines are read at once.
type replayBatch struct {
	lines  []string
	offset int64
}

// readAllExisting reads all available lines from offset without sleeping.
// Used during replay phase for fast-forward. Reads all available lines in a
// single file-open pass and returns them as a batch.
func readAllExisting(path string, offset int64) tea.Cmd {
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return replayDone{offset: offset}
		}
		defer f.Close()

		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return replayDone{offset: offset}
		}

		reader := bufio.NewReader(f)
		lastOffset := offset
		var lines []string

		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				lastOffset += int64(len(line))
				lines = append(lines, strings.TrimRight(line, "\n"))
			}
			if err != nil {
				break
			}
		}

		if len(lines) == 0 {
			// No more data — we've caught up.
			return replayDone{offset: lastOffset}
		}

		return replayBatch{lines: lines, offset: lastOffset}
	}
}

// waitForFile polls for a new line with a short sleep. Used in live mode.
func waitForFile(path string, offset int64) tea.Cmd {
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			return fileChecked{offset: offset}
		}
		defer f.Close()

		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			time.Sleep(200 * time.Millisecond)
			return fileChecked{offset: offset}
		}

		reader := bufio.NewReader(f)
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			time.Sleep(100 * time.Millisecond)
			return fileChecked{offset: offset}
		}

		newOffset := offset + int64(len(line))
		return eventLine{line: strings.TrimRight(line, "\n"), offset: newOffset}
	}
}

// --- Subcommand entry point ---

func watchSubcommand() {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	idFlag := fs.String("id", "", "run ID or prefix (auto-selects by cwd if omitted)")
	sinceFlag := fs.Int64("since", -1, "start reading from byte offset (machine-readable mode). Use 0 for beginning.")
				followFlag := fs.Bool("follow", false, "follow mode: continuously stream events until agent exits (machine-readable)")
	watchTimeoutFlag := fs.Duration("timeout", -1, "with --follow: max duration to wait (0 = until agent process exits; default without this flag: exit on agent_end)")
	prettyFlag := fs.Bool("pretty", false, "with --follow: format output as readable conversation instead of raw JSONL")
	fs.Parse(os.Args[1:])

	machineMode := *followFlag || *sinceFlag >= 0

	// Machine-readable modes (--since, --follow) allow completed runs.
	// TUI mode requires a running agent (for live socket stream).
	var meta *run.RunMeta
	var err error
	if machineMode {
		meta, err = resolveRunForMachineWatch(*idFlag)
	} else {
		meta, err = resolveRunForWatch(*idFlag)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	eventsPath := run.EventsPath("", meta.ID)

	// Follow mode: continuously stream events until agent exits.
	if *followFlag {
		// --follow requires the agent to be running (uses socket stream).
		if !run.IsRunning(meta) {
			fmt.Fprintf(os.Stderr, "error: run %s is not running (status: %s), --follow requires a live agent\n", meta.ID, meta.Status)
			os.Exit(1)
		}
								followWatch(meta, 0, *prettyFlag, *watchTimeoutFlag)
		return
	}

	// Machine-readable mode: print raw events + final offset.
	// This still uses file-based polling since machine mode is a one-shot read.
	if *sinceFlag >= 0 {
		machineWatch(eventsPath, *sinceFlag)
		return
	}

	// Connect via socket stream for live events.
	sockPath := run.SocketPath("", meta.ID)

	// Check that the socket exists.
	if _, err := os.Stat(sockPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: socket not found: %s\n", sockPath)
		os.Exit(1)
	}

	model := newWatchModelFromSocket(sockPath, meta.ID)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// machineWatch reads events from offset and prints raw lines + final offset.
// Used for machine-readable incremental consumption.
// NOTE: machineWatch still uses file-based polling as a fallback for
// completed runs where no broadcaster is active.
func machineWatch(eventsPath string, offset int64) {
	f, err := os.Open(eventsPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot open events file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		fmt.Fprintf(os.Stderr, "error: seek failed: %v\n", err)
		os.Exit(1)
	}

	reader := bufio.NewReader(f)
	lastOffset := offset
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			lastOffset += int64(len(line))
			fmt.Print(line)
		}
		if err != nil {
			break
		}
	}
		// Print final offset as last line.
	fmt.Printf("__offset:%d\n", lastOffset)
}

// followWatch continuously streams events from the agent via socket.
// It connects to the Unix domain socket and subscribes to the event stream,
// printing each event line to stdout until the connection closes (agent exits).
func followWatch(meta *run.RunMeta, fromSeq uint64, pretty bool, watchTimeout time.Duration) {
	sockPath := run.SocketPath("", meta.ID)

	client := run.NewSocketClient(sockPath)
	conn, _, err := client.Stream(fromSeq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot connect to agent stream: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// watchTimeout == -1: flag not set → default behavior (exit on agent_end)
	// watchTimeout == 0: wait forever (until agent process exits)
	// watchTimeout > 0: wait up to this duration
	if watchTimeout > 0 {
		conn.SetDeadline(time.Now().Add(watchTimeout))
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if !pretty {
		// Raw JSONL mode (original behavior).
		seq := fromSeq
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			fmt.Println(line)
			seq++
		}
		fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
		return
	}

	// Pretty mode: stream formatted output in real-time using ParseEvent.
	// No ANSI colors — this output is consumed by agents, not humans.
	seq := fromSeq
	lastKind := run.EventKind("")
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		seq++

		evt := run.ParseEvent(line)
		if evt == nil {
			continue
		}

		// On kind transition, add line break for readability.
		if evt.Kind != lastKind && lastKind != "" && lastKind != run.KindTool {
			fmt.Println()
		}

		switch evt.Kind {
		case run.KindText:
			fmt.Print(evt.Text)
		case run.KindThinking:
			fmt.Print(evt.Text)
		case run.KindTool:
			fmt.Printf("  %s\n", evt.Text)
		case run.KindMeta:
			fmt.Fprintf(os.Stderr, "%s\n", evt.Text)
		case run.KindResponse:
			fmt.Print(evt.Text)
		case run.KindSessionSwitch:
			fmt.Fprintf(os.Stderr, "%s\n", evt.Text)
		}
		if evt.Kind != run.KindMeta && evt.Kind != run.KindSessionSwitch {
			lastKind = evt.Kind
		}

								// On agent_end:
		// - Default (no --timeout flag): exit immediately. One-shot task complete.
		// - With --timeout 0 or --timeout N: continue waiting for more events.
		if strings.Contains(line, `"agent_end"`) {
			fmt.Println()
			fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
			if watchTimeout < 0 {
				// No --timeout flag → one-shot mode, exit.
				return
			}
			// --timeout set (0 or positive) → keep going.
			continue
		}
	}
	fmt.Fprintf(os.Stderr, "--- agent stream ended without agent_end event ---\n")
	fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
}

// --- Pretty printing helpers ---

type prettyContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type prettyMessage struct {
	Role    string               `json:"role"`
	Content []prettyContentBlock `json:"content"`
}

// summarizeToolInput returns a short summary of a tool call's input.
func summarizeToolInput(name string, raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return string(raw)
	}
	switch name {
	case "bash":
		if v, ok := input["command"]; ok {
			s := strings.Trim(string(v), `"`)
			if len(s) > 120 {
				return s[:120] + "..."
			}
			return s
		}
	case "read":
		if v, ok := input["path"]; ok {
			return strings.Trim(string(v), `"`)
		}
	case "write":
		if v, ok := input["path"]; ok {
			return strings.Trim(string(v), `"`)
		}
	case "edit":
		path := ""
		if v, ok := input["path"]; ok {
			path = strings.Trim(string(v), `"`)
		}
		return path
	case "grep":
		parts := []string{}
		if v, ok := input["pattern"]; ok {
			parts = append(parts, strings.Trim(string(v), `"`))
		}
		if v, ok := input["path"]; ok {
			parts = append(parts, strings.Trim(string(v), `"`))
		}
		return strings.Join(parts, " in ")
	}
	// Generic: show first field.
	for k, v := range input {
		s := strings.Trim(string(v), `"`)
		if len(s) > 80 {
			s = s[:80] + "..."
		}
		return k + "=" + s
	}
	return ""
}

// prettyPrintAgentEnd formats the complete conversation from agent_end.
func prettyPrintAgentEnd(line string) {
	var event struct {
		Messages []prettyMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to parse agent_end: %v\n", err)
		return
	}

				for _, msg := range event.Messages {
		// Skip tool result messages — output is too verbose.
		if msg.Role == "toolResult" {
			continue
		}
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				text := strings.TrimSpace(block.Text)
				if text == "" {
					continue
				}
				if msg.Role == "user" {
					fmt.Printf("user: %s\n", text)
				} else {
					fmt.Printf("assistant: %s\n", text)
				}
			case "thinking":
				t := strings.TrimSpace(block.Thinking)
				if t == "" {
					continue
				}
				if len(t) > 300 {
					t = t[:300] + "..."
				}
				fmt.Printf("thinking: %s\n", t)
			case "toolCall":
				fmt.Printf("tool: %s(%s)\n", block.Name, summarizeToolInput(block.Name, block.Arguments))
			}
		}
	}

	// Extract stop reason from the raw line.
	if idx := strings.Index(line, `"stopReason":"`); idx != -1 {
		start := idx + len(`"stopReason":"`)
		end := strings.IndexByte(line[start:], '"')
		if end > 0 {
			reason := line[start : start+end]
			fmt.Printf("--- done (stopReason: %s) ---\n", reason)
		}
	}
}

// resolveRunForWatch resolves a run by ID flag or auto-selection.
func resolveRunForWatch(idFlag string) (*run.RunMeta, error) {
	if idFlag != "" {
		// Try exact match first.
		meta, err := run.LoadRunMeta(run.RunMetaPath("", idFlag))
		if err == nil {
			if !run.IsRunning(meta) {
				return nil, fmt.Errorf("run %s is not running (status: %s)", meta.ID, meta.Status)
			}
			return meta, nil
		}
		// Try prefix match.
		results, err := run.FindByPrefix("", idFlag)
		if err != nil {
			return nil, fmt.Errorf("prefix lookup for %q: %w", idFlag, err)
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("no running run found matching %q", idFlag)
		}
		if len(results) == 1 {
			m := results[0]
			if !run.IsRunning(&m) {
				return nil, fmt.Errorf("run %s is not running (status: %s)", m.ID, m.Status)
			}
			return &m, nil
		}
		return nil, fmt.Errorf("ambiguous prefix %q matches %d runs", idFlag, len(results))
	}

	// Auto-select by cwd.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get cwd: %w", err)
	}
	running, err := run.FindRunningByCwd("", cwd)
	if err != nil {
		return nil, fmt.Errorf("find running: %w", err)
	}

	// Filter to only actually-alive processes.
	var alive []run.RunMeta
	for _, r := range running {
		if run.IsRunning(&r) {
			alive = append(alive, r)
		}
	}

	if len(alive) == 0 {
		return nil, fmt.Errorf("no running instances in %s", cwd)
	}
	if len(alive) > 1 {
		ids := make([]string, len(alive))
		for i, r := range alive {
			ids[i] = r.ID
		}
		return nil, fmt.Errorf("multiple running instances in %s: %v (use --id to select)", cwd, ids)
	}
		return &alive[0], nil
}

// resolveRunForMachineWatch resolves a run without requiring it to be running.
// Used by --since and --follow modes for replaying completed runs.
func resolveRunForMachineWatch(idFlag string) (*run.RunMeta, error) {
	if idFlag != "" {
		// Try exact match first.
		meta, err := run.LoadRunMeta(run.RunMetaPath("", idFlag))
		if err == nil {
			return meta, nil
		}
		// Try prefix match.
		results, err := run.FindByPrefix("", idFlag)
		if err != nil {
			return nil, fmt.Errorf("prefix lookup for %q: %w", idFlag, err)
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("no run found matching %q", idFlag)
		}
		if len(results) == 1 {
			return &results[0], nil
		}
		return nil, fmt.Errorf("ambiguous prefix %q matches %d runs", idFlag, len(results))
	}

	// Auto-select by cwd — prefer running, fall back to most recent.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get cwd: %w", err)
	}
	running, err := run.FindRunningByCwd("", cwd)
	if err != nil {
		return nil, fmt.Errorf("find runs: %w", err)
	}
	var alive []run.RunMeta
	for _, r := range running {
		if run.IsRunning(&r) {
			alive = append(alive, r)
		}
	}
	if len(alive) == 1 {
		return &alive[0], nil
	}
	if len(alive) > 1 {
		ids := make([]string, len(alive))
		for i, r := range alive {
			ids[i] = r.ID
		}
		return nil, fmt.Errorf("multiple running instances in %s: %v (use --id to select)", cwd, ids)
	}

		return nil, fmt.Errorf("no running instances in %s (use --id to select a specific run)", cwd)
}

// processEvent handles a single parsed event with role-aware streaming.
func (m *watchModel) processEvent(f *run.FormattedEvent) {
	if f == nil {
		return
	}

	switch f.Kind {
	case run.KindText:
		// Text content (assistant or user) — stream inline with role prefix
		role := f.Role
		if role == "" {
			role = "assistant"
		}
		if m.ensureRole(role) {
			text := f.Text
			m.appendInline(text)
		}

	case run.KindThinking:
		// Thinking delta — stream inline with role prefix
		if m.ensureRole("thinking") {
			text := f.Text
			m.appendInline(thinkingStyle.Render(text))
		}

	case run.KindTool:
		// Tool events — one line per event, prefixed
		m.endInline()
		m.appendContent(toolStyle.Render(f.Text))

	case run.KindResponse:
		// Slash command response — one line
		m.endInline()
		if strings.Contains(f.Text, "failed") || strings.Contains(f.Text, "error") {
			m.appendContent(errStyle.Render(f.Text))
		} else {
			m.appendContent(metaStyle.Render(f.Text))
		}

	case run.KindMeta:
		// System messages (ai: agent started, compaction, etc.)
		m.endInline()
		if strings.Contains(f.Text, "failed") || strings.Contains(f.Text, "error") {
			m.appendContent(errStyle.Render(f.Text))
		} else {
			m.appendContent(aiStyle.Render(f.Text))
		}

	case run.KindSessionSwitch:
		m.endInline()
		m.appendContent(sessStyle.Render(f.Text))

	default:
		m.endInline()
		m.appendContent(f.Text)
	}
}

// renderEvent converts a FormattedEvent to a styled string for display.
// Legacy function used for non-streaming contexts.
func renderEvent(f *run.FormattedEvent) string {
	switch f.Kind {
	case run.KindText:
		// Assistant text: no prefix, plain output (streamed via sentBuf)
		return f.Text

	case run.KindThinking:
		// Thinking: styled, with "thinking: " prefix when role is set
		return thinkingStyle.Render(f.Text)

	case run.KindTool:
		// Tool execution: styled
		return toolStyle.Render(f.Text)

	case run.KindResponse:
		// Slash command response: style errors differently
		if strings.Contains(f.Text, "ai:") && (strings.Contains(f.Text, "failed") || strings.Contains(f.Text, "error")) {
			return errStyle.Render(f.Text)
		}
		return metaStyle.Render(f.Text)

	case run.KindMeta:
		// System messages (ai: agent started, ai: compaction done, etc.)
		if strings.Contains(f.Text, "failed") || strings.Contains(f.Text, "error") {
			return errStyle.Render(f.Text)
		}
		return aiStyle.Render(f.Text)

	case run.KindSessionSwitch:
		return sessStyle.Render(f.Text)

	default:
		return f.Text
	}
}