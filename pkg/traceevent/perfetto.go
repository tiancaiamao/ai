package traceevent

import (
	"encoding/json"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// PerfettoFile represents a perfetto-compatible trace-event JSON file.
type PerfettoFile struct {
	path string
	file *os.File
}

var traceIDSeq atomic.Uint64

// NewPerfettoFile creates a new perfetto trace file.
func NewPerfettoFile(path string) (*PerfettoFile, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &PerfettoFile{path: path, file: f}, nil
}

// WriteEvents writes events to perfetto file.
// Output format is Chrome Trace Event JSON, which ui.perfetto.dev imports directly.
func (pf *PerfettoFile) WriteEvents(traceID []byte, events []TraceEvent) error {
	pid := processIDForTrace(traceID)
	trace := map[string]any{
		"displayTimeUnit": "ms",
		"traceEvents":     buildTraceEventsJSON(pid, events),
	}
	enc := json.NewEncoder(pf.file)
	enc.SetEscapeHTML(false)
	return enc.Encode(trace)
}

// Close closes the file.
func (pf *PerfettoFile) Close() error {
	if pf.file != nil {
		return pf.file.Close()
	}
	return nil
}

// WritePerfettoFile writes trace events to a perfetto file at the given path.
func WritePerfettoFile(path string, traceID []byte, events []TraceEvent) error {
	pf, err := NewPerfettoFile(path)
	if err != nil {
		return err
	}
	defer pf.Close()
	if err := pf.WriteEvents(traceID, events); err != nil {
		return err
	}
	return nil
}

// buildTraceEventJSON creates a JSON object for a single trace event.
func buildTraceEventJSON(pid int, event TraceEvent) map[string]any {
	item := map[string]any{
		"name": event.Name,              // Use event name directly (debug, info, warn, error, llm_call, etc.)
		"cat":  event.Category.String(), // Use category directly (llm, tool, event, metrics, log)
		"ph":   string(event.Phase),
		"ts":   event.Timestamp.UnixMicro(),
		"pid":  pid,
		"tid":  threadIDForCategory(event.Category),
	}
	args := fieldsToArgs(event.Fields)
	if tid, ok := threadIDFromArgs(event.Category, args); ok {
		item["tid"] = tid
	}
	if len(args) > 0 {
		item["args"] = args
	}
	switch event.Phase {
	case PhaseInstant:
		item["s"] = "t" // thread scoped instant event
	case PhaseComplete:
		if v, ok := args["duration_ms"]; ok {
			if durUs, ok := toDurationMicroseconds(v); ok {
				item["dur"] = durUs
			}
		}
	}
	return item
}

// buildTraceEventsJSON builds the complete JSON array for all events.
func buildTraceEventsJSON(pid int, events []TraceEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events)+5)
	out = append(out, processMetadataEvent(pid))
	for _, event := range events {
		out = append(out, buildTraceEventJSON(pid, event))
	}
	return out
}

// processMetadataEvent creates the process_name metadata event.
func processMetadataEvent(pid int) map[string]any {
	return map[string]any{
		"name": "process_name",
		"ph":   "M",
		"pid":  pid,
		"tid":  0,
		"args": map[string]any{
			"name": "ai-agent",
		},
	}
}

// processIDForTrace generates a process ID from trace ID.
func processIDForTrace(traceID []byte) int {
	h := fnv.New32a()
	_, _ = h.Write(traceID)
	return int(h.Sum32())
}

// threadIDForCategory returns the thread ID for a given category.
func threadIDForCategory(c TraceCategory) int {
	switch c {
	case CategoryLLM:
		return 1
	case CategoryTool:
		return 2
	case CategoryEvent:
		return 4
	default:
		return 0
	}
}

// fieldsToArgs converts trace fields to key-value pairs for args.
func fieldsToArgs(fields []Field) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	args := make(map[string]any)
	for _, f := range fields {
		args[f.Key] = f.Value
	}
	return args
}

// threadIDFromArgs extracts thread ID from args (for tool calls).
func threadIDFromArgs(category TraceCategory, args map[string]any) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	raw, ok := args["tool_call_id"]
	if !ok {
		return 0, false
	}
	id, ok := raw.(string)
	if !ok || strings.TrimSpace(id) == "" {
		return 0, false
	}
	base := threadIDForCategory(category)
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return base*1000 + int(h.Sum32()%500), true
}

// toDurationMicroseconds converts a duration value to microseconds.
func toDurationMicroseconds(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n) * 1000, true
	case int64:
		return n * 1000, true
	case float64:
		return int64(n * 1000), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, false
		}
		return int64(parsed * 1000), true
	default:
		return 0, false
	}
}
