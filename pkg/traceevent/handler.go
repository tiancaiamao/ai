package traceevent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TraceHandler handles trace events.
type TraceHandler interface {
	Handle(ctx context.Context, traceID []byte, events []TraceEvent) error
}

// ChunkTraceHandler supports incremental flushes and explicit finalization.
type ChunkTraceHandler interface {
	HandleChunk(ctx context.Context, traceID []byte, events []TraceEvent, final bool) error
}

// FileHandler writes traces to perfetto-compatible JSON files.
type FileHandler struct {
	outputDir        string
	processID        int
	maxFileSizeBytes int64
	mu               sync.Mutex
	streams          map[string]*traceStream
}

type traceStream struct {
	baseName    string
	part        int
	file        *os.File
	path        string
	pid         int
	hasAnyEvent bool
	dataEvents  int
}

const traceJSONSuffix = "]}\n"
const defaultTraceMaxFileSizeBytes int64 = 4 * 1024 * 1024

// NewFileHandler creates a new file handler.
func NewFileHandler(outputDir string) (*FileHandler, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, err
	}
	return &FileHandler{
		outputDir:        outputDir,
		processID:        os.Getpid(),
		maxFileSizeBytes: defaultTraceMaxFileSizeBytes,
		streams:          make(map[string]*traceStream),
	}, nil
}

// SetMaxFileSizeBytes configures the max bytes per output trace file.
// When the limit is exceeded, subsequent events are written to a new part file.
// A value <= 0 disables size-based splitting.
func (h *FileHandler) SetMaxFileSizeBytes(maxBytes int64) {
	h.mu.Lock()
	h.maxFileSizeBytes = maxBytes
	h.mu.Unlock()
}

// Handle writes trace events to a file.
func (h *FileHandler) Handle(ctx context.Context, traceID []byte, events []TraceEvent) error {
	return h.HandleChunk(ctx, traceID, events, true)
}

// HandleChunk incrementally writes trace events, finalizing JSON when final=true.
func (h *FileHandler) HandleChunk(_ context.Context, traceID []byte, events []TraceEvent, final bool) error {
	traceIDStr := sanitizeFilenameComponent(string(traceID))
	h.mu.Lock()
	defer h.mu.Unlock()

	stream := h.streams[traceIDStr]
	if stream == nil {
		if len(events) == 0 && !final {
			return nil
		}

		var err error
		baseName := h.traceBaseName(traceIDStr, time.Now())
		stream, err = h.createTraceStream(baseName, processIDForTrace(traceID), 0)
		if err != nil {
			return err
		}
		h.streams[traceIDStr] = stream
	}

	for _, event := range events {
		if err := h.appendEventWithRotation(stream, buildTraceEventJSON(stream.pid, event)); err != nil {
			_ = stream.file.Close()
			delete(h.streams, traceIDStr)
			return err
		}
	}

	if len(events) > 0 {
		stream.hasAnyEvent = true
	}

	if err := stream.file.Sync(); err != nil {
		_ = stream.file.Close()
		delete(h.streams, traceIDStr)
		return err
	}

	if final {
		if err := stream.file.Close(); err != nil {
			delete(h.streams, traceIDStr)
			return err
		}
		delete(h.streams, traceIDStr)
	}

	return nil
}

func (h *FileHandler) appendEventWithRotation(stream *traceStream, obj map[string]any) error {
	blob, err := json.Marshal(obj)
	if err != nil {
		return err
	}

	if h.maxFileSizeBytes > 0 && stream.dataEvents > 0 {
		size, err := stream.file.Seek(0, io.SeekEnd)
		if err != nil {
			return err
		}
		// Appending replaces closing suffix in-place, so net growth is comma+payload.
		predictedSize := size + int64(len(blob))
		if stream.hasAnyEvent {
			predictedSize++
		}
		if predictedSize > h.maxFileSizeBytes {
			if err := h.rotateStream(stream); err != nil {
				return err
			}
		}
	}

	return h.appendObject(stream, blob, true)
}

func (h *FileHandler) appendObject(stream *traceStream, blob []byte, dataEvent bool) error {
	suffixLen := int64(len(traceJSONSuffix))
	size, err := stream.file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if size < suffixLen {
		return fmt.Errorf("invalid trace file state for %s", stream.path)
	}
	if _, err := stream.file.Seek(-suffixLen, io.SeekEnd); err != nil {
		return err
	}
	if stream.hasAnyEvent {
		if _, err := stream.file.WriteString(","); err != nil {
			return err
		}
	}
	if _, err := stream.file.Write(blob); err != nil {
		return err
	}
	if _, err := stream.file.WriteString(traceJSONSuffix); err != nil {
		return err
	}
	stream.hasAnyEvent = true
	if dataEvent {
		stream.dataEvents++
	}
	return nil
}

func (h *FileHandler) createTraceStream(baseName string, pid, part int) (*traceStream, error) {
	path := h.traceFilePath(baseName, part)
	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	stream := &traceStream{
		baseName: baseName,
		part:     part,
		file:     file,
		path:     path,
		pid:      pid,
	}
	if _, err := stream.file.WriteString("{\"displayTimeUnit\":\"ms\",\"traceEvents\":[" + traceJSONSuffix); err != nil {
		_ = stream.file.Close()
		return nil, err
	}
	metaBlob, err := json.Marshal(processMetadataEvent(stream.pid))
	if err != nil {
		_ = stream.file.Close()
		return nil, err
	}
	if err := h.appendObject(stream, metaBlob, false); err != nil {
		_ = stream.file.Close()
		return nil, err
	}
	return stream, nil
}

func (h *FileHandler) rotateStream(stream *traceStream) error {
	if err := stream.file.Close(); err != nil {
		return err
	}
	nextPart := stream.part + 1
	next, err := h.createTraceStream(stream.baseName, stream.pid, nextPart)
	if err != nil {
		return err
	}
	stream.part = next.part
	stream.file = next.file
	stream.path = next.path
	stream.hasAnyEvent = next.hasAnyEvent
	stream.dataEvents = next.dataEvents
	return nil
}

func (h *FileHandler) traceFilePath(baseName string, part int) string {
	if part <= 0 {
		filename := fmt.Sprintf("%s.perfetto.json", baseName)
		return filepath.Join(h.outputDir, filename)
	}
	filename := fmt.Sprintf("%s-part-%d.perfetto.json", baseName, part)
	return filepath.Join(h.outputDir, filename)
}

func (h *FileHandler) traceBaseName(traceID string, createdAt time.Time) string {
	ts := createdAt.UTC().Format("20060102T150405.000Z")
	return fmt.Sprintf("pid%d-%s-%s", h.processID, ts, traceID)
}

func sanitizeFilenameComponent(s string) string {
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

var globalHandler atomic.Pointer[TraceHandler]

// SetHandler sets the global trace handler.
func SetHandler(handler TraceHandler) {
	globalHandler.Store(&handler)
}

// GetHandler returns the global trace handler.
func GetHandler() TraceHandler {
	if ptr := globalHandler.Load(); ptr != nil {
		return *ptr
	}
	return nil
}

// ClearHandler removes the global trace handler.
func ClearHandler() {
	globalHandler.Store(nil)
}
