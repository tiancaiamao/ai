package bridge

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/genius/ag/internal/conv"
)

const (
	streamFileName    = "stream.log"
	lastTextMaxLen    = 4096
	streamFlushInterval = 500 * time.Millisecond
)

// StreamWriter appends formatted, human-readable agent activity to stream.log.
// It replaces the previous strings.Builder accumulation pattern, solving two problems:
//   - No unbounded memory growth (all text goes directly to disk)
//   - Real-time observability (tail -f stream.log works immediately)
type StreamWriter struct {
	mu       sync.Mutex
	file     *os.File
	buf      []byte // buffered writes
	lastText []byte // last ~4KB for activity.json LastText
	path     string
}

// NewStreamWriter creates or appends to a stream.log in the given directory.
func NewStreamWriter(agentDir string) (*StreamWriter, error) {
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return nil, fmt.Errorf("create agent dir: %w", err)
	}

	path := filepath.Join(agentDir, streamFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("open stream.log: %w", err)
	}

	return &StreamWriter{
		file: f,
		path: path,
		buf:  make([]byte, 0, 4096),
	}, nil
}

// AppendText appends a text delta from the assistant.
func (sw *StreamWriter) AppendText(delta string) {
	sw.appendLine(conv.KindText, delta, delta)
}

// AppendToolCall appends a tool execution start event.
func (sw *StreamWriter) AppendToolCall(toolName, detail string) {
	display := fmt.Sprintf("🔧 %s%s", toolName, detail)
	sw.appendLine(conv.KindTool, display, "")
}

// AppendMeta appends a meta event (turn, agent start/end, etc.).
func (sw *StreamWriter) AppendMeta(text string) {
	sw.appendLine(conv.KindMeta, text, "")
}

// LastText returns the most recent ~4KB of assistant text.
// Used by ActivityWriter for activity.json LastText field.
func (sw *StreamWriter) LastText() string {
	return string(sw.lastText)
}

// Close flushes pending writes and closes the file.
func (sw *StreamWriter) Close() error {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if err := sw.flushLocked(); err != nil {
		return err
	}
	return sw.file.Close()
}

// Path returns the stream.log file path.
func (sw *StreamWriter) Path() string {
	return sw.path
}

func (sw *StreamWriter) appendLine(kind conv.EventKind, display string, rawText string) {
	timestamp := conv.FormatTimestamp(time.Now())
	var line string
	switch kind {
	case conv.KindText:
		line = display // text deltas are written as-is, no prefix
	case conv.KindTool:
		line = fmt.Sprintf("%s %s", timestamp, display)
	case conv.KindMeta:
		line = fmt.Sprintf("%s %s", timestamp, display)
	}

	sw.mu.Lock()
	sw.buf = append(sw.buf, line...)
	if !endsWithNewline(sw.buf) {
		sw.buf = append(sw.buf, '\n')
	}

	// Update lastText ring buffer for text deltas only
	if kind == conv.KindText && rawText != "" {
		sw.lastText = append(sw.lastText, rawText...)
		if len(sw.lastText) > lastTextMaxLen {
			sw.lastText = sw.lastText[len(sw.lastText)-lastTextMaxLen:]
		}
	}

	sw.mu.Unlock()
}

// Flush forces any buffered data to disk.
func (sw *StreamWriter) Flush() error {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.flushLocked()
}

func (sw *StreamWriter) flushLocked() error {
	if len(sw.buf) == 0 {
		return nil
	}
	_, err := sw.file.Write(sw.buf)
	if err != nil {
		return fmt.Errorf("write stream.log: %w", err)
	}
	sw.buf = sw.buf[:0]
	return nil
}

// RunFlusher starts a goroutine that periodically flushes the StreamWriter.
// Returns a stop function. Call it when the event reader finishes.
func (sw *StreamWriter) RunFlusher() (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(streamFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := sw.Flush(); err != nil {
					log.Printf("streamwriter: flush error: %v", err)
				}
			case <-done:
				// Final flush
				sw.Flush()
				return
			}
		}
	}()
	return func() { close(done) }
}

func endsWithNewline(b []byte) bool {
	return len(b) > 0 && b[len(b)-1] == '\n'
}