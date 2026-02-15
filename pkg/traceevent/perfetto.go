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

// Thread ID allocation following unified observability principles:
// - 0: Log and Metrics (metadata)
// - 1: LLM operations
// - 2: Tool operations (base)
// - 3: Agent events
// - 4: Metrics counters
// - 2000-2499: Individual tool calls (base*1000 + hash)
// - 5000-9999: Individual metric series (base*1000 + hash)
const (
	tidLog     = 0
	tidLLM     = 1
	tidTool    = 2
	tidEvent   = 3
	tidMetrics = 4

	tidToolCallBase     = 2000 // Tool calls: 2000-2499
	tidMetricSeriesBase = 5000 // Metric series: 5000-9999
)

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

	// Override tid based on event-specific identifiers
	switch event.Category {
	case CategoryTool:
		if tid, ok := threadIDForToolCall(args); ok {
			item["tid"] = tid
		}
	case CategoryMetrics:
		if tid, ok := threadIDForMetricSeries(event.Name, args); ok {
			item["tid"] = tid
		}
	}

	if len(args) > 0 {
		item["args"] = args
	}

	switch event.Phase {
	case PhaseInstant:
		item["s"] = "t" // thread scoped instant event
	case PhaseCounter:
		// Counter events for real-time metrics
		if v, ok := args["value"]; ok {
			item["args"] = map[string]any{"value": v}
		}
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
		return tidLLM
	case CategoryTool:
		return tidTool
	case CategoryEvent:
		return tidEvent
	case CategoryMetrics:
		return tidMetrics
	case CategoryLog:
		return tidLog
	default:
		return tidLog
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

// threadIDForToolCall generates a stable thread ID for a tool call.
// Same tool_call_id must map to the same tid so B/E span events pair correctly in Perfetto.
func threadIDForToolCall(args map[string]any) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	rawID, ok := args["tool_call_id"]
	if !ok {
		return 0, false
	}
	id := strings.TrimSpace(toStableString(rawID))
	if id == "" {
		return 0, false
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return tidToolCallBase + int(h.Sum32()%500), true
}

func toStableString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return ""
	}
}

// threadIDForMetricSeries generates a thread ID for a metric series.
// Metrics with the same name get the same tid to show them in one track.
func threadIDForMetricSeries(metricName string, args map[string]any) (int, bool) {
	// Hash metric name to get consistent tid in range 5000-9999
	h := fnv.New32a()
	_, _ = h.Write([]byte(metricName))
	return tidMetricSeriesBase + int(h.Sum32()%5000), true
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
