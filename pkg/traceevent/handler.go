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
	outputDir string
	mu        sync.Mutex
	streams   map[string]*traceStream
}

type traceStream struct {
	file        *os.File
	path        string
	pid         int
	hasAnyEvent bool
}

const traceJSONSuffix = "]}\n"

// NewFileHandler creates a new file handler.
func NewFileHandler(outputDir string) (*FileHandler, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, err
	}
	return &FileHandler{
		outputDir: outputDir,
		streams:   make(map[string]*traceStream),
	}, nil
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

		filename := fmt.Sprintf("trace-%s.perfetto.json", traceIDStr)
		path := filepath.Join(h.outputDir, filename)
		file, err := os.Create(path)
		if err != nil {
			return err
		}

		stream = &traceStream{
			file: file,
			path: path,
			pid:  processIDForTrace(traceID),
		}
		h.streams[traceIDStr] = stream

		if _, err := stream.file.WriteString("{\"displayTimeUnit\":\"ms\",\"traceEvents\":[" + traceJSONSuffix); err != nil {
			_ = stream.file.Close()
			delete(h.streams, traceIDStr)
			return err
		}
		if err := h.appendObject(stream, processMetadataEvent(stream.pid)); err != nil {
			_ = stream.file.Close()
			delete(h.streams, traceIDStr)
			return err
		}
	}

	for _, event := range events {
		if err := h.appendObject(stream, buildTraceEventJSON(stream.pid, event)); err != nil {
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

func (h *FileHandler) appendObject(stream *traceStream, obj map[string]any) error {
	blob, err := json.Marshal(obj)
	if err != nil {
		return err
	}
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
	return nil
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
