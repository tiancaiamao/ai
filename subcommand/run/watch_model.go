package run

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	ansi "github.com/charmbracelet/x/ansi"

	"github.com/tiancaiamao/ai/pkg/rpc"
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

// acpEventMsg is a tea.Msg delivered for each ACP session/update received
// from the agent (via a live ACP client connection).
type acpEventMsg struct {
	u rpc.ACPUpdate
}

// acpStreamClosedMsg is a tea.Msg delivered when the ACP connection is closed
// (the agent process exited).
type acpStreamClosedMsg struct{}

// --- Model ---

type watchModel struct {
	viewport   viewport.Model
	ready      bool
	err        error
	width      int
	height     int
	mode       string // "live"
	runID      string
	statusLine string
	label      string // "ai run" or "ai watch"
	p          *tea.Program

	// Content management (line-buffered, incremental wrapping).
	// - rawParas stores completed raw paragraphs (for resize re-wrap), capped.
	// - pendingRaw accumulates the current in-progress text_delta stream.
	// - wrappedLines stores pre-wrapped lines from completed paragraphs.
	rawParas     []string
	pendingRaw   *strings.Builder
	wrappedLines []string
	maxWrapped   int // max wrapped lines before dropping oldest (0 = unlimited)
	// pendingFlushThreshold is the byte size at which pendingRaw is flushed
	// early to avoid O(N²) wrapping of a single long paragraph.
	pendingFlushThreshold int

	// Streaming state: tracks current role prefix for inline content.
	// Role prefix printed once when role changes, then text appended inline
	currentRole  string
	inlineActive bool
	dirty        bool
	showPrefixes bool // whether to show "role: " prefixes (default true)
	showThinking bool // whether to show thinking content
	showTools    bool // whether to show tool content
	quitOnStream bool // quit the TUI when the ACP stream closes (ai watch)
}

func newWatchModelForACP(label, runID string) watchModel {
	return watchModel{
		label:                 label,
		runID:                 runID,
		mode:                  "live",
		statusLine:            fmt.Sprintf("%s | run %s | live", label, runID),
		pendingRaw:            &strings.Builder{},
		showPrefixes:          true,
		showThinking:          true,
		showTools:             true,
		maxWrapped:            5000,
		pendingFlushThreshold: 2000,
	}
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
func (m *watchModel) syncContent() {
	if !m.ready {
		return
	}

	var content string
	if len(m.wrappedLines) > 0 {
		content = strings.Join(m.wrappedLines, "\n")
	}

	// If there's in-progress inline text, wrap it and append.
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

func (m watchModel) Init() tea.Cmd { return nil }

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
			return m, nil
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

	case acpEventMsg:
		formatted := tui.ParseACPUpdate(msg.u)
		if formatted == nil {
			return m, nil
		}
		m.processEvent(formatted)
		m.syncIfDirty()
		m.updateStatus()
		return m, nil

	case acpStreamClosedMsg:
		// The agent process exited — leave the TUI.
		return m, tea.Quit

	case errMsg:
		m.err = msg.err
		return m, tea.Quit
	}

	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// errMsg is a tea.Msg delivered when the TUI hits a fatal error.
type errMsg struct {
	err error
}

func (m *watchModel) updateStatus() {
	m.statusLine = fmt.Sprintf("%s | run %s | %s | %d lines", m.label, m.runID, m.mode, len(m.wrappedLines))
}

func (m watchModel) View() string {
	if !m.ready {
		return fmt.Sprintf("%s | run %s | loading...\n", m.label, m.runID)
	}
	return m.viewport.View() + "\n" + statusBar.Render(m.statusLine)
}

// --- Subcommand entry point ---

func WatchSubcommand() {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	idFlag := fs.String("id", "", "run ID or prefix (auto-selects by cwd if omitted)")
	sinceFlag := fs.Int64("since", -1, "start reading from byte offset (machine-readable mode). Use 0 for beginning.")
	followFlag := fs.Bool("follow", false, "follow mode: continuously stream events until the turn ends (machine-readable)")
	watchTimeoutFlag := fs.Duration("timeout", -1, "with --follow: max duration to wait (0 = until the agent process exits; default without this flag: exit on turn end)")
	prettyFlag := fs.Bool("pretty", false, "with --follow: format output as readable conversation instead of raw JSONL")
	summaryFlag := fs.Bool("summary", false, "with --follow --pretty: only show final assistant text (no intermediate thinking/tools)")
	fs.Parse(os.Args[1:])

	machineMode := *followFlag || *sinceFlag >= 0

	// Machine-readable modes (--since, --follow) allow completed runs.
	// TUI mode requires a running agent (for the live ACP connection).
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

	// Follow mode: continuously stream events until the turn ends.
	if *followFlag {
		if !tui.IsRunning(meta) {
			fmt.Fprintf(os.Stderr, "error: run %s is not running (status: %s), --follow requires a live agent\n", meta.ID, meta.Status)
			os.Exit(1)
		}
		result := followWatch(meta, 0, *prettyFlag, *summaryFlag, *watchTimeoutFlag)
		if result.timedOut {
			fmt.Fprintln(os.Stderr, "--- watch timeout; agent may still be running, continue watching before cleanup ---")
		}
		if code := followWatchExitCode(result); code != 0 {
			os.Exit(code)
		}
		return
	}

	// Machine-readable mode: print raw events + final offset.
	// One-shot file read — works for both running and completed runs.
	if *sinceFlag >= 0 {
		machineWatch(eventsPath, *sinceFlag)
		return
	}

	// TUI mode: attach to the live ACP agent over its unix socket.
	sockPath := tui.SocketPath("", meta.ID)
	client, sid, err := rpc.DialACP(sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot attach to agent: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	model := newWatchModelForACP("ai watch", meta.ID)
	model.quitOnStream = true
	p := tea.NewProgram(model, tea.WithAltScreen())
	go model.consumeACP(client.Updates(), p)

	// Replay persisted history. The agent rejects the replay while a prompt
	// is in flight — in that case show live updates only.
	if err := client.LoadSession(sid); err != nil {
		fmt.Fprintf(os.Stderr, "warning: history replay unavailable (%v); showing live updates only\n", err)
	}

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// consumeACP pumps ACP updates from the client into the tea program. It runs
// in a background goroutine and exits when the update stream is closed.
// Note: tea.Program.Send is safe to call after the program exits.
func (m *watchModel) consumeACP(updates <-chan rpc.ACPUpdate, p *tea.Program) {
	for u := range updates {
		p.Send(acpEventMsg{u: u})
	}
	if m.quitOnStream {
		p.Send(acpStreamClosedMsg{})
	}
}

// machineWatch reads events from offset and prints raw lines + final offset.
// Used for machine-readable incremental consumption.
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

// followWatchResult describes why a follow watch stopped.
type followWatchResult struct {
	ended    bool
	timedOut bool
}

func followWatchExitCode(result followWatchResult) int {
	if result.timedOut {
		return 2
	}
	if !result.ended {
		return 1
	}
	return 0
}

// followWatch streams ACP updates from the agent until the current turn ends
// (_turn_end update), the connection closes, or the timeout fires.
func followWatch(meta *tui.RunMeta, fromSeq uint64, pretty bool, summary bool, watchTimeout time.Duration) followWatchResult {
	// watchTimeout == -1: flag not set → default behavior (exit on _turn_end)
	// watchTimeout == 0: wait forever (until the agent process exits)
	// watchTimeout > 0: wait up to this duration
	client, sid, err := rpc.DialACP(tui.SocketPath("", meta.ID))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot connect to agent: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	var timedOut atomic.Bool
	var timeoutTimer *time.Timer
	timerDone := make(chan struct{})
	if watchTimeout > 0 {
		timeoutTimer = time.AfterFunc(watchTimeout, func() {
			timedOut.Store(true)
			// Signal timer completion before closing the client. Close waits for
			// the read/update loops, so waiting for timerDone first would deadlock
			// when the deadline races with normal completion.
			close(timerDone)
			_ = client.Close()
		})
	}
	var stopTimerOnce sync.Once
	stopTimer := func() {
		stopTimerOnce.Do(func() {
			if timeoutTimer == nil || timeoutTimer.Stop() {
				return
			}
			<-timerDone
		})
	}
	finish := func(ended bool) followWatchResult {
		// Stop and drain the timer before reading timedOut, so a completion
		// event racing with the deadline is not reported as a timeout.
		stopTimer()
		return followWatchResult{ended: ended, timedOut: timedOut.Load()}
	}
	defer stopTimer()

	updates := client.Updates()
	replayLoaded := false
	if err := client.LoadSession(sid); err != nil {
		// Not fatal — continue with live updates only. This is expected when
		// the initial prompt is already in flight.
		fmt.Fprintf(os.Stderr, "warning: session replay failed (%v); showing live updates only\n", err)
	} else {
		replayLoaded = true
	}

	if summary {
		ended := followWatchSummaryWithReplay(updates, fromSeq, replayLoaded)
		return finish(ended)
	}

	if !pretty {
		// Raw JSONL mode: re-emit updates as ACP session/update envelopes.
		seq := fromSeq
		ended := false
		for u := range updates {
			if u.SessionUpdate == rpc.ACPUpdateSessionLoadEnd {
				if replayLoaded {
					ended = true
					break
				}
				continue
			}

			env, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"method":  "session/update",
				"params": map[string]any{
					"sessionId": sid,
					"update":    u,
				},
			})
			if err != nil {
				continue
			}
			fmt.Println(string(env))
			seq++
			if u.SessionUpdate == "_turn_end" {
				ended = true
				break
			}
		}
		if !ended {
			fmt.Fprintln(os.Stderr, "--- agent stream ended without _turn_end event ---")
		}
		fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
		return finish(ended)
	}

	// Pretty mode: stream formatted output in real-time using ParseACPUpdate.
	// No ANSI colors — this output is consumed by agents, not humans.
	seq := fromSeq
	lastKind := tui.EventKind("")
	lastTextRole := ""
	ended := false
	for u := range updates {
		if u.SessionUpdate == rpc.ACPUpdateSessionLoadEnd {
			if replayLoaded {
				ended = true
				break
			}
			continue
		}

		seq++

		evt := tui.ParseACPUpdate(u)
		if evt == nil {
			continue
		}

		// On kind transition, add line break for readability.
		if evt.Kind != lastKind && lastKind != "" && lastKind != tui.KindTool {
			fmt.Println()
		}

		switch evt.Kind {
		case tui.KindText:
			// Prefix user text (echo of the sent prompt) so consumers can
			// distinguish it from the assistant's reply.
			if evt.Role == "user" && lastTextRole != "user" {
				if lastTextRole != "" {
					fmt.Println()
				}
				fmt.Print("user: ")
			}
			fmt.Print(evt.Text)
			lastTextRole = evt.Role
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

		// On _turn_end: always exit — the turn is complete.
		// The --timeout flag controls maximum wait time for the agent to
		// finish, not how long to wait after it finishes.
		if u.SessionUpdate == "_turn_end" {
			ended = true
			fmt.Println()
			break
		}
	}
	if !ended {
		fmt.Fprintln(os.Stderr, "--- agent stream ended without _turn_end event ---")
	}
	fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
	return finish(ended)
}

// followWatchSummary accumulates events and only prints the final assistant text
// when _turn_end or session/load replay completion is reached. This avoids
// flooding tool output with intermediate thinking, tool calls, and tool results.
func followWatchSummary(updates <-chan rpc.ACPUpdate, fromSeq uint64) bool {
	return followWatchSummaryWithReplay(updates, fromSeq, true)
}

func followWatchSummaryWithReplay(updates <-chan rpc.ACPUpdate, fromSeq uint64, stopOnReplay bool) bool {
	var lastAssistantText strings.Builder
	var currentAssistantText strings.Builder
	seq := fromSeq
	ended := false

	for u := range updates {
		if u.SessionUpdate == rpc.ACPUpdateSessionLoadEnd {
			if !stopOnReplay {
				continue
			}
			ended = true
			if currentAssistantText.Len() > 0 {
				lastAssistantText.Reset()
				lastAssistantText.WriteString(currentAssistantText.String())
				currentAssistantText.Reset()
			}
			break
		}
		seq++
		if u.SessionUpdate == "_turn_end" {
			ended = true
			if currentAssistantText.Len() > 0 {
				lastAssistantText.Reset()
				lastAssistantText.WriteString(currentAssistantText.String())
				currentAssistantText.Reset()
			}
			break
		}
		evt := tui.ParseACPUpdate(u)
		if evt != nil && evt.Kind == tui.KindText && evt.Role != "user" {
			currentAssistantText.WriteString(evt.Text)
		}
	}

	if ended {
		if text := strings.TrimSpace(lastAssistantText.String()); text != "" {
			fmt.Println(text)
		}
	} else {
		text := strings.TrimSpace(currentAssistantText.String())
		if text == "" {
			text = strings.TrimSpace(lastAssistantText.String())
		}
		if text != "" {
			fmt.Println(text)
		}
	}
	fmt.Fprintf(os.Stderr, "__seq:%d\n", seq)
	return ended
}

// --- Run resolution ---

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
			m.appendInline(f.Text)
		}

	case tui.KindThinking:
		// Thinking delta — stream inline with role prefix
		if m.ensureRole("thinking") {
			m.appendInline(thinkingStyle.Render(f.Text))
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
