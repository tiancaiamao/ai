package run

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

	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
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

// --- Model ---

type watchModel struct {
	viewport    viewport.Model
	eventsPath  string // legacy: for file-based polling (machine mode only)
	offset      int64  // legacy: current read position in events.jsonl
	ready       bool
	err         error
	width       int
	height      int
	mode        string // "replay" or "live"
	caughtUp    bool   // true when replay phase finishes
	runID       string
	statusLine  string
	sinceFlag   int64 // --since offset for machine-readable mode
	machineMode bool  // if true, print raw events + cursor and exit

	// Content management (line-buffered, incremental wrapping).
	// - rawParas stores completed raw paragraphs (for resize re-wrap), capped.
	// - pendingRaw accumulates the current in-progress text_delta stream.
	// - wrappedLines stores pre-wrapped lines from completed paragraphs.
	rawParas     []string         // completed raw paragraphs (for resize)
	pendingRaw   *strings.Builder // current inline text accumulation
	wrappedLines []string         // pre-wrapped lines from completed paragraphs
	maxWrapped   int              // max wrapped lines before dropping oldest (0 = unlimited)
	// pendingFlushThreshold is the byte size at which pendingRaw is flushed
	// early to wrappedLines to avoid O(N²) wrapping of a single long paragraph.
	// 0 = never flush early (flush only on endInline).
	pendingFlushThreshold int

	// Streaming state: tracks current role prefix for inline content.
	// Role prefix printed once when role changes, then text appended inline
	currentRole  string // "", "assistant", "thinking", "tool", "ai"
	inlineActive bool   // true when we're in the middle of an inline stream
	dirty        bool   // true when content has changed but viewport not yet updated
	showPrefixes bool   // whether to show "role: " prefixes (default true)
	showThinking bool   // whether to show thinking content
	showTools    bool   // whether to show tool content

	// In-memory event source (used by ai run embedded TUI).
	broadcaster    *tui.EventBroadcaster
	broadcasterSub *tui.Consumer

	// Socket stream event source (used by ai watch connecting to ai serve).
	sockConn    net.Conn
	sockPath    string
	sockScanner *bufio.Scanner
}

func newWatchModel(eventsPath, runID string, sinceOffset int64, machineMode bool) watchModel {
	m := watchModel{
		eventsPath:            eventsPath,
		runID:                 runID,
		mode:                  "replay",
		statusLine:            fmt.Sprintf("ai watch | run %s | replaying...", runID),
		rawParas:              nil,
		pendingRaw:            &strings.Builder{},
		sinceFlag:             sinceOffset,
		machineMode:           machineMode,
		showPrefixes:          true,
		showThinking:          true,
		showTools:             true,
		maxWrapped:            5000,
		pendingFlushThreshold: 2000,
	}
	return m
}

// newWatchModelFromBroadcaster creates a watchModel that reads events from
// an in-memory EventBroadcaster (used by the `ai run` embedded TUI).
func newWatchModelFromBroadcaster(b *tui.EventBroadcaster, runID string) watchModel {
	m := watchModel{
		runID:                 runID,
		mode:                  "live",
		caughtUp:              true,
		statusLine:            fmt.Sprintf("ai run | run %s | live", runID),
		rawParas:              nil,
		pendingRaw:            &strings.Builder{},
		showPrefixes:          true,
		showThinking:          true,
		showTools:             true,
		maxWrapped:            5000,
		pendingFlushThreshold: 2000,
		broadcaster:           b,
	}

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
		runID:                 runID,
		mode:                  "live",
		caughtUp:              true,
		statusLine:            fmt.Sprintf("ai watch | run %s | connecting...", runID),
		rawParas:              nil,
		pendingRaw:            &strings.Builder{},
		showPrefixes:          true,
		showThinking:          true,
		showTools:             true,
		maxWrapped:            5000,
		pendingFlushThreshold: 2000,
		sockPath:              sockPath,
	}
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

// wrapWidth returns the effective wrapping width, with a fallback for the
// case where the terminal size has not been received yet (width <= 0).
func (m *watchModel) wrapWidth() int {
	if m.width <= 0 {
		return 80
	}
	return m.width
}

// wrapAndAppend wraps a raw paragraph at the current width and appends the
// resulting lines to wrappedLines.
func (m *watchModel) wrapAndAppend(raw string) {
	wrapped := ansi.Wrap(raw, m.wrapWidth(), "")
	for _, line := range strings.Split(wrapped, "\n") {
		m.wrappedLines = append(m.wrappedLines, line)
	}
}

// syncContent pushes the current content to the viewport and scrolls to the bottom.
// Unlike the old implementation, it does NOT re-wrap all raw content every call.
// It joins pre-wrapped lines (from completed paragraphs) and only wraps the
// current in-progress inline text (usually short).
func (m *watchModel) syncContent() {
	if !m.ready {
		return
	}

	var content string
	if len(m.wrappedLines) > 0 {
		content = strings.Join(m.wrappedLines, "\n")
	}

	// If there's in-progress inline text, wrap it and append.
	// This is typically short (a few words), so wrapping is cheap.
	if m.pendingRaw.Len() > 0 {
		if content != "" {
			content += "\n"
		}
		content += ansi.Wrap(m.pendingRaw.String(), m.wrapWidth(), "")
	}

	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

// appendContent writes a complete, non-inline line to the content buffer.
// This is used for tool events, meta messages, etc.
func (m *watchModel) appendContent(text string) {
	m.endInline() // flush any pending inline text

	m.rawParas = append(m.rawParas, text)
	m.wrapAndAppend(text)
	m.capContent()

	m.dirty = true
}

// appendInline appends text to the current inline stream.
// The text is accumulated in pendingRaw and wrapped-on-demand by syncContent.
// If pendingRaw exceeds pendingFlushThreshold, it is flushed early to
// wrappedLines to avoid O(N²) wrapping of a single long paragraph.
func (m *watchModel) appendInline(text string) {
	m.pendingRaw.WriteString(text)
	if m.pendingFlushThreshold > 0 && m.pendingRaw.Len() >= m.pendingFlushThreshold {
		m.flushPendingInline()
	}
	m.dirty = true
}

// flushPendingInline moves the current pendingRaw content to rawParas and
// wrappedLines as a completed paragraph, then resets pendingRaw.
// inlineActive is NOT changed — the caller continues appending to the new
// (empty) pendingRaw as part of the same inline stream.
func (m *watchModel) flushPendingInline() {
	if m.pendingRaw.Len() == 0 {
		return
	}
	raw := m.pendingRaw.String()
	m.pendingRaw.Reset()
	m.rawParas = append(m.rawParas, raw)
	m.wrapAndAppend(raw)
	m.capContent()
}

// syncIfDirty flushes pending content changes to the viewport.
// Call this at the end of processing a batch of events (e.g. after processEvent).
func (m *watchModel) syncIfDirty() {
	if m.dirty {
		m.dirty = false
		m.syncContent()
	}
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

	// Role changed — end previous inline, start new role prefix
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
		m.pendingRaw.WriteString(styled)
	}

	m.currentRole = role
	m.inlineActive = true
	return true
}

// endInline finishes the current inline stream (if any) with a newline.
// It flushes any accumulated pendingRaw to rawParas and wrappedLines as a
// completed paragraph. If pendingRaw is empty, an empty line is still added
// to preserve paragraph spacing.
func (m *watchModel) endInline() {
	if m.inlineActive {
		if m.pendingRaw.Len() > 0 {
			raw := m.pendingRaw.String()
			m.pendingRaw.Reset()
			m.rawParas = append(m.rawParas, raw)
			m.wrapAndAppend(raw)
		} else {
			// Preserve empty paragraph as a blank line.
			m.rawParas = append(m.rawParas, "")
			m.wrappedLines = append(m.wrappedLines, "")
		}
		m.capContent()

		m.inlineActive = false
		m.currentRole = ""
		m.dirty = true
	}
}

// rebuildWrappedLines re-wraps all completed raw paragraphs from rawParas
// and rebuilds wrappedLines. This is called on terminal resize (rare).
// It does NOT touch pendingRaw — the current inline text is preserved
// and will be wrapped by syncContent on the next update cycle.
func (m *watchModel) rebuildWrappedLines() {
	m.wrappedLines = nil

	for _, para := range m.rawParas {
		m.wrapAndAppend(para)
	}
	m.capContent()

	// Update viewport with rebuilt content.
	m.syncContent()
}

// capContent trims both wrappedLines and rawParas to their respective limits
// by dropping the oldest entries. This bounds memory usage and ensures resize
// cost is proportional to maxWrapped, not total session output.
func (m *watchModel) capContent() {
	if m.maxWrapped <= 0 {
		return
	}
	if len(m.wrappedLines) > m.maxWrapped {
		n := len(m.wrappedLines) - m.maxWrapped
		m.wrappedLines = m.wrappedLines[n:]
	}
	// Cap rawParas to the same limit. Each para produces ≥1 wrapped line,
	// so this ensures rawParas never exceeds wrappedLines in count.
	if len(m.rawParas) > m.maxWrapped {
		m.rawParas = m.rawParas[len(m.rawParas)-m.maxWrapped:]
	}
}

// rawText returns the full raw text (completed paragraphs + pending inline).
// Used for testing and debugging.
func (m *watchModel) rawText() string {
	var parts []string
	parts = append(parts, m.rawParas...)
	if m.pendingRaw.Len() > 0 {
		parts = append(parts, m.pendingRaw.String())
	}
	return strings.Join(parts, "\n")
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
		// Re-wrap all content at the new width (one-time cost on resize).
		m.rebuildWrappedLines()

	case broadcasterEvent:
		// Event from in-memory broadcaster (ai run).
		formatted := tui.ParseEvent(msg.line)
		if formatted == nil {
			return m, pollBroadcaster(m.broadcasterSub)
		}
		m.processEvent(formatted)
		m.syncIfDirty()
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
		m.syncIfDirty()
		return m, tea.Quit

	case socketEvent:
		// Event from unix socket stream (ai watch → ai serve).
		formatted := tui.ParseEvent(msg.line)
		if formatted == nil {
			return m, readSocketEvent(m.sockScanner)
		}
		m.processEvent(formatted)
		m.syncIfDirty()
		m.updateStatus()
		return m, readSocketEvent(m.sockScanner)

	case replayDone:
		// Finished replaying history, switch to live mode.
		m.offset = msg.offset
		m.caughtUp = true
		m.mode = "live"
		m.updateStatus()
		m.syncIfDirty()
		return m, waitForFile(m.eventsPath, m.offset)

	case replayBatch:
		// Batch of events from replay phase — render all at full speed.
		m.offset = msg.offset
		for _, line := range msg.lines {
			m.processEvent(tui.ParseEvent(line))
		}
		m.endInline()
		m.syncIfDirty()
		m.updateStatus()
		return m, m.nextCmd()

	case eventLine:
		m.offset = msg.offset
		formatted := tui.ParseEvent(msg.line)
		if formatted == nil {
			return m, m.nextCmd()
		}

		m.processEvent(formatted)
		m.syncIfDirty()
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
	m.statusLine = fmt.Sprintf("ai watch | run %s | %s | %d lines", m.runID, m.mode, len(m.wrappedLines))
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
func pollBroadcaster(c *tui.Consumer) tea.Cmd {
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
		cmd := tui.Command{Type: "stream", FromSeq: 0}
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

		var resp tui.Response
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
	socketStreamMu      sync.Mutex
	socketStreamConn    net.Conn
	socketStreamScanner *bufio.Scanner
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

func WatchSubcommand() {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	idFlag := fs.String("id", "", "run ID or prefix (auto-selects by cwd if omitted)")
	sinceFlag := fs.Int64("since", -1, "start reading from byte offset (machine-readable mode). Use 0 for beginning.")
	followFlag := fs.Bool("follow", false, "follow mode: continuously stream events until agent exits (machine-readable)")
	watchTimeoutFlag := fs.Duration("timeout", -1, "with --follow: max duration to wait (0 = until agent process exits; default without this flag: exit on agent_end)")
	prettyFlag := fs.Bool("pretty", false, "with --follow: format output as readable conversation instead of raw JSONL")
	summaryFlag := fs.Bool("summary", false, "with --follow --pretty: only show final assistant text (no intermediate thinking/tools)")
	fs.Parse(os.Args[1:])

	machineMode := *followFlag || *sinceFlag >= 0

	// Machine-readable modes (--since, --follow) allow completed runs.
	// TUI mode requires a running agent (for live socket stream).
	var meta *tui.RunMeta
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

	eventsPath := tui.EventsPath("", meta.ID)

	// Follow mode: continuously stream events until agent exits.
	if *followFlag {
		// --follow requires the agent to be running (uses socket stream).
		if !tui.IsRunning(meta) {
			fmt.Fprintf(os.Stderr, "error: run %s is not running (status: %s), --follow requires a live agent\n", meta.ID, meta.Status)
			os.Exit(1)
		}
		followWatch(meta, 0, *prettyFlag, *summaryFlag, *watchTimeoutFlag)
		return
	}

	// Machine-readable mode: print raw events + final offset.
	// This still uses file-based polling since machine mode is a one-shot read.
	if *sinceFlag >= 0 {
		machineWatch(eventsPath, *sinceFlag)
		return
	}

	// Connect via socket stream for live events.
	sockPath := tui.SocketPath("", meta.ID)

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
func followWatch(meta *tui.RunMeta, fromSeq uint64, pretty bool, summary bool, watchTimeout time.Duration) {
	sockPath := tui.SocketPath("", meta.ID)

	client := tui.NewSocketClient(sockPath)
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

	// Summary mode: accumulate only the last assistant text, suppress intermediate output.
	if summary {
		followWatchSummary(scanner, fromSeq, watchTimeout)
		return
	}

	// Pretty mode: stream formatted output in real-time using ParseEvent.
	// No ANSI colors — this output is consumed by agents, not humans.
	seq := fromSeq
	lastKind := tui.EventKind("")
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		seq++

		evt := tui.ParseEvent(line)
		if evt == nil {
			continue
		}

		// On kind transition, add line break for readability.
		if evt.Kind != lastKind && lastKind != "" && lastKind != tui.KindTool {
			fmt.Println()
		}

		switch evt.Kind {
		case tui.KindText:
			fmt.Print(evt.Text)
		case tui.KindThinking:
			fmt.Print(evt.Text)
		case tui.KindTool:
			fmt.Printf("  %s\n", evt.Text)
		case tui.KindMeta:
			fmt.Fprintf(os.Stderr, "%s\n", evt.Text)
		case tui.KindResponse:
			fmt.Print(evt.Text)
		case tui.KindSessionSwitch:
			fmt.Fprintf(os.Stderr, "%s\n", evt.Text)
		}
		if evt.Kind != tui.KindMeta && evt.Kind != tui.KindSessionSwitch {
			lastKind = evt.Kind
		}

		// On agent_end: always exit — the task is complete.
		// The --timeout flag controls maximum wait time for the agent to finish,
		// not how long to wait after it finishes.
		if strings.Contains(line, `"agent_end"`) {
			fmt.Println()
			fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "--- agent stream ended without agent_end event ---\n")
	fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
}

// followWatchSummary accumulates events and only prints the final assistant text
// when agent_end is reached. This avoids flooding tool output with intermediate
// thinking, tool calls, and tool results.
func followWatchSummary(scanner *bufio.Scanner, fromSeq uint64, watchTimeout time.Duration) {
	var lastAssistantText strings.Builder
	var currentAssistantText strings.Builder
	seq := fromSeq

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		seq++

		// Check for agent_end — always exit when task is complete.
		if strings.Contains(line, `"agent_end"`) {
			// Save current assistant text as the "last" one.
			if currentAssistantText.Len() > 0 {
				lastAssistantText.Reset()
				lastAssistantText.WriteString(currentAssistantText.String())
				currentAssistantText.Reset()
			}

			fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
			// Print the final assistant text and exit.
			text := strings.TrimSpace(lastAssistantText.String())
			if text != "" {
				fmt.Println(text)
			}
			return
		}

		// Parse and accumulate assistant text only.
		evt := tui.ParseEvent(line)
		if evt == nil {
			continue
		}
		if evt.Kind == tui.KindText {
			currentAssistantText.WriteString(evt.Text)
		}
	}

	// Stream ended without agent_end — print whatever we have.
	text := strings.TrimSpace(currentAssistantText.String())
	if text != "" {
		fmt.Println(text)
	} else {
		text = strings.TrimSpace(lastAssistantText.String())
		if text != "" {
			fmt.Println(text)
		}
	}
	fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
}

// resolveRunForWatch resolves a run by ID flag or auto-selection.
func resolveRunForWatch(idFlag string) (*tui.RunMeta, error) {
	if idFlag != "" {
		// Try exact match first.
		meta, err := tui.LoadRunMeta(tui.RunMetaPath("", idFlag))
		if err == nil {
			if !tui.IsRunning(meta) {
				return nil, fmt.Errorf("run %s is not running (status: %s)", meta.ID, meta.Status)
			}
			return meta, nil
		}
		// Try prefix match.
		results, err := tui.FindByPrefix("", idFlag)
		if err != nil {
			return nil, fmt.Errorf("prefix lookup for %q: %w", idFlag, err)
		}
		if len(results) == 0 {
			return nil, fmt.Errorf("no running run found matching %q", idFlag)
		}
		if len(results) == 1 {
			m := results[0]
			if !tui.IsRunning(&m) {
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
	running, err := tui.FindRunningByCwd("", cwd)
	if err != nil {
		return nil, fmt.Errorf("find running: %w", err)
	}

	// Filter to only actually-alive processes.
	var alive []tui.RunMeta
	for _, r := range running {
		if tui.IsRunning(&r) {
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
func resolveRunForMachineWatch(idFlag string) (*tui.RunMeta, error) {
	if idFlag != "" {
		// Try exact match first.
		meta, err := tui.LoadRunMeta(tui.RunMetaPath("", idFlag))
		if err == nil {
			return meta, nil
		}
		// Try prefix match.
		results, err := tui.FindByPrefix("", idFlag)
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
	running, err := tui.FindRunningByCwd("", cwd)
	if err != nil {
		return nil, fmt.Errorf("find runs: %w", err)
	}
	var alive []tui.RunMeta
	for _, r := range running {
		if tui.IsRunning(&r) {
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
func (m *watchModel) processEvent(f *tui.FormattedEvent) {
	if f == nil {
		return
	}

	switch f.Kind {
	case tui.KindText:
		// Text content (assistant or user) — stream inline with role prefix
		role := f.Role
		if role == "" {
			role = "assistant"
		}
		if m.ensureRole(role) {
			text := f.Text
			m.appendInline(text)
		}

	case tui.KindThinking:
		// Thinking delta — stream inline with role prefix
		if m.ensureRole("thinking") {
			text := f.Text
			m.appendInline(thinkingStyle.Render(text))
		}

	case tui.KindTool:
		// Tool events — one line per event, prefixed
		m.endInline()
		m.appendContent(toolStyle.Render(f.Text))

	case tui.KindResponse:
		// Slash command response — one line
		m.endInline()
		if strings.Contains(f.Text, "failed") || strings.Contains(f.Text, "error") {
			m.appendContent(errStyle.Render(f.Text))
		} else {
			m.appendContent(metaStyle.Render(f.Text))
		}

	case tui.KindMeta:
		// System messages (ai: agent started, compaction, etc.)
		m.endInline()
		if strings.Contains(f.Text, "failed") || strings.Contains(f.Text, "error") {
			m.appendContent(errStyle.Render(f.Text))
		} else {
			m.appendContent(aiStyle.Render(f.Text))
		}

	case tui.KindSessionSwitch:
		m.endInline()
		m.appendContent(sessStyle.Render(f.Text))

	default:
		m.endInline()
		m.appendContent(f.Text)
	}
}
