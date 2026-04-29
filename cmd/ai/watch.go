package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tiancaiamao/ai/pkg/run"
)

// --- Styles ---

var (
	metaStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
	toolStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true)
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	sessStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Underline(true)
	statusBar = lipgloss.NewStyle().
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
	eventsPath  string
	offset      int64 // current read position in events.jsonl
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
	sinceFlag   int64  // --since offset for machine-readable mode
	machineMode bool   // if true, print raw events + cursor and exit
}

func newWatchModel(eventsPath, runID string, sinceOffset int64, machineMode bool) watchModel {
	m := watchModel{
		eventsPath:  eventsPath,
		runID:       runID,
		mode:        "replay",
		statusLine:  fmt.Sprintf("ai watch | run %s | replaying...", runID),
		content:     &strings.Builder{},
		sinceFlag:   sinceOffset,
		machineMode: machineMode,
	}
	m.sentBuf = newSentenceBuffer(func(text string) {
		m.appendContent(text)
	})
	return m
}

func (m *watchModel) appendContent(text string) {
	m.content.WriteString(text)
	m.content.WriteString("\n")
	m.lines++
	if m.ready {
		m.viewport.SetContent(m.content.String())
		m.viewport.GotoBottom()
	}
}

func (m watchModel) Init() tea.Cmd {
	if m.machineMode {
		return nil
	}
	return readAllExisting(m.eventsPath, m.sinceFlag)
}

func (m watchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerHeight := 1 // status bar
		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-headerHeight)
			m.viewport.SetContent(m.content.String())
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - headerHeight
		}

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
			formatted := run.ParseEvent(line)
			if formatted == nil {
				continue
			}
			var rendered string
			switch formatted.Kind {
			case run.KindText:
				rendered = formatted.Text
			case run.KindTool:
				rendered = toolStyle.Render(formatted.Text)
			case run.KindMeta:
				if strings.Contains(formatted.Text, "failed") || strings.Contains(formatted.Text, "error") {
					rendered = errStyle.Render(formatted.Text)
				} else {
					rendered = metaStyle.Render(formatted.Text)
				}
			case run.KindSessionSwitch:
				rendered = sessStyle.Render(formatted.Text)
			}
			m.appendContent(rendered)
		}
		m.updateStatus()
		return m, m.nextCmd()

	case eventLine:
		m.offset = msg.offset
		formatted := run.ParseEvent(msg.line)
		if formatted == nil {
			return m, m.nextCmd()
		}

		var rendered string
		switch formatted.Kind {
		case run.KindText:
			rendered = formatted.Text
		case run.KindTool:
			rendered = toolStyle.Render(formatted.Text)
		case run.KindMeta:
			if strings.Contains(formatted.Text, "failed") || strings.Contains(formatted.Text, "error") {
				rendered = errStyle.Render(formatted.Text)
			} else {
				rendered = metaStyle.Render(formatted.Text)
			}
		case run.KindSessionSwitch:
			rendered = sessStyle.Render(formatted.Text)
		}

		// In replay mode: render immediately at full speed (no buffering).
		// In live mode: use sentence-buffered typewriter for KindText.
		if m.mode == "live" && formatted.Kind == run.KindText {
			m.sentBuf.write(rendered)
		} else {
			m.appendContent(rendered)
		}

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

// --- File reading commands ---

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
	fs.Parse(os.Args[1:])

	// Resolve the run.
	meta, err := resolveRunForWatch(*idFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	eventsPath := run.EventsPath("", meta.ID)

	// Machine-readable mode: print raw events + final offset.
	if *sinceFlag >= 0 {
		machineWatch(eventsPath, *sinceFlag)
		return
	}

	// Check that events.jsonl exists (or wait briefly for it).
	if _, err := os.Stat(eventsPath); err != nil {
		time.Sleep(500 * time.Millisecond)
		if _, err := os.Stat(eventsPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: events file not found: %s\n", eventsPath)
			os.Exit(1)
		}
	}

	model := newWatchModel(eventsPath, meta.ID, 0, false)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
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