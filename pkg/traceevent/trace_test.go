package traceevent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTraceBufRecord(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	// Enable test event
	EnableEvent("prompt_start")

	Log(ctx, CategoryEvent, "prompt_start",
		Field{Key: "message", Value: "test"})

	events := tb.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Name != "prompt_start" {
		t.Errorf("expected event name 'prompt_start', got '%s'", events[0].Name)
	}
}

func TestEventEnablement(t *testing.T) {
	// Test EnableEvent/DisableEvent functions with real events
	EnableEvent("prompt_start")

	if !IsEventEnabled("prompt_start") {
		t.Fatal("expected event to be enabled")
	}

	DisableEvent("prompt_start")

	if IsEventEnabled("prompt_start") {
		t.Fatal("expected event to be disabled")
	}
}

func TestEventFiltering(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	// Disable default events first to get clean state
	DisableEvent("llm_call_start")
	DisableEvent("llm_call_end")

	// Only enable specific events
	EnableEvent("prompt_start")
	EnableEvent("prompt_end")

	Log(ctx, CategoryEvent, "prompt_start", Field{Key: "test", Value: "value1"})
	Log(ctx, CategoryEvent, "prompt_end", Field{Key: "test", Value: "value2"})
	Log(ctx, CategoryLLM, "llm_call_start", Field{Key: "test", Value: "value3"})

	events := tb.Snapshot()
	// Only enabled events should be recorded
	if len(events) != 2 {
		t.Fatalf("expected 2 events (enabled only), got %d", len(events))
	}

	// Verify the events are the enabled ones
	if events[0].Name != "prompt_start" {
		t.Errorf("expected first event 'prompt_start', got '%s'", events[0].Name)
	}
	if events[1].Name != "prompt_end" {
		t.Errorf("expected second event 'prompt_end', got '%s'", events[1].Name)
	}
}

func TestTraceBufSetTraceID(t *testing.T) {
	tb := NewTraceBuf()
	traceID := []byte("test-trace-id")
	tb.SetTraceID(traceID)

	if tb.traceID == nil {
		t.Fatal("expected traceID to be set")
	}

	if string(tb.traceID) != string(traceID) {
		t.Errorf("expected traceID '%s', got '%s'", string(traceID), string(tb.traceID))
	}
}

func TestTraceBufRingBuffer(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	// Set a small max events for testing
	tb.SetMaxEvents(5)

	// Use a valid event name
	EnableEvent("prompt_start")

	// Add more events than maxEvents
	for i := 0; i < 10; i++ {
		Log(ctx, CategoryEvent, "prompt_start", Field{Key: "index", Value: i})
	}

	events := tb.Snapshot()
	// Should only have maxEvents (ring buffer behavior)
	if len(events) != 5 {
		t.Fatalf("expected 5 events (maxEvents), got %d", len(events))
	}

	// Verify ring buffer dropped oldest events
	// The last 5 events should be indices 5-9
	expectedIndices := []interface{}{5, 6, 7, 8, 9}
	for i, event := range events {
		found := false
		for _, expected := range expectedIndices {
			if event.Fields[0].Value == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("event %d has unexpected index value: %v", i, event.Fields[0].Value)
		}
	}
}

func TestTraceBufWithNilContext(t *testing.T) {
	ctx := context.Background()

	// Should not panic when TraceBuf is not in context
	EnableEvent("test_event")
	Log(ctx, CategoryEvent, "test_event", Field{Key: "test", Value: "value"})

	// If we got here without panic, test passed
}

func TestLogHelpers(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	EnableEvent("log:debug_message")
	EnableEvent("log:info")
	EnableEvent("log:error")
	EnableEvent("log:warn")

	Log(ctx, CategoryEvent, "log:debug_message", Field{Key: "key", Value: "value"}, Field{Key: "message", Value: "debug message"})
	Log(ctx, CategoryEvent, "log:info", Field{Key: "key", Value: "value"}, Field{Key: "message", Value: "info message"})
	Log(ctx, CategoryEvent, "log:error", Field{Key: "key", Value: "value"}, Field{Key: "message", Value: "error message"})
	Log(ctx, CategoryEvent, "log:warn", Field{Key: "key", Value: "value"}, Field{Key: "message", Value: "warn message"})

	events := tb.Snapshot()
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}

	// Verify event names (new naming scheme)
	expectedNames := []string{"log:debug_message", "log:info", "log:error", "log:warn"}
	for i, event := range events {
		if event.Name != expectedNames[i] {
			t.Errorf("expected event name '%s', got '%s'", expectedNames[i], event.Name)
		}
	}

	// Verify message field is set
	for _, event := range events {
		messageFound := false
		for _, field := range event.Fields {
			if field.Key == "message" {
				messageFound = true
				break
			}
		}
		if !messageFound {
			t.Errorf("event '%s' missing message field", event.Name)
		}
	}
}

func TestStructuredLogHelpersKV(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)
	EnableEvent("log:error")

	// Test Error function which handles KV pairs
	Error(ctx, CategoryEvent, "structured message", "k1", "v1", "k2", 2, "dangling")

	events := tb.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Name != "log:error" {
		t.Fatalf("expected error event, got %s", events[0].Name)
	}
	args := map[string]any{}
	for _, f := range events[0].Fields {
		args[f.Key] = f.Value
	}
	if args["message"] != "structured message" {
		t.Fatalf("missing message field: %#v", args)
	}
	if args["k1"] != "v1" || args["k2"] != 2 {
		t.Fatalf("missing kv fields: %#v", args)
	}
	if args["dangling"] != "<missing>" {
		t.Fatalf("expected dangling key placeholder, got %#v", args["dangling"])
	}
}

func TestLogPhaseInferenceForLifecycleNames(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	EnableEvent("prompt_start")
	EnableEvent("prompt_end")
	EnableEvent("turn_start")
	EnableEvent("turn_end")

	Log(ctx, CategoryEvent, "prompt_start")
	Log(ctx, CategoryEvent, "prompt_end")
	Log(ctx, CategoryEvent, "turn_start")
	Log(ctx, CategoryEvent, "turn_end")

	events := tb.Snapshot()
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	for i, ev := range events {
		if ev.Phase != PhaseInstant {
			t.Fatalf("expected event %d phase I, got %s", i, ev.Phase)
		}
	}
}

func TestTraceEventPhase(t *testing.T) {
	tests := []struct {
		phase    Phase
		expected string
	}{
		{PhaseBegin, "B"},
		{PhaseEnd, "E"},
		{PhaseComplete, "X"},
		{PhaseInstant, "I"},
		{PhaseCounter, "C"},
	}

	for _, tt := range tests {
		if string(tt.phase) != tt.expected {
			t.Errorf("expected phase '%s', got '%s'", tt.expected, string(tt.phase))
		}
	}
}

func TestTraceEventTimestamp(t *testing.T) {
	before := time.Now()
	event := TraceEvent{
		Timestamp: before,
		Name:      "test",
		Phase:     PhaseInstant,
		Category:  CategoryEvent,
	}
	after := time.Now()

	if event.Timestamp.Before(before) || event.Timestamp.After(after) {
		t.Error("timestamp not within expected range")
	}
}

func TestTraceCategoryString(t *testing.T) {
	tests := []struct {
		category TraceCategory
		expected string
	}{
		{CategoryLLM, "llm"},
		{CategoryTool, "tool"},
		{CategoryEvent, "event"},
		{CategoryMetrics, "metrics"},
	}

	for _, tt := range tests {
		if tt.category.String() != tt.expected {
			t.Errorf("category %d: expected '%s', got '%s'", tt.category, tt.expected, tt.category.String())
		}
	}
}

func TestDefaultEnabledEvents(t *testing.T) {
	// Reset to defaults by disabling all, then re-enabling defaults
	for eventName := range eventNameToBit {
		DisableEvent(eventName)
	}

	// Enable default events (simulating package init behavior)
	defaultEvents := DefaultEvents()
	for _, eventName := range defaultEvents {
		EnableEvent(eventName)
	}

	// Verify default events are enabled
	for _, eventName := range defaultEvents {
		if !IsEventEnabled(eventName) {
			t.Errorf("expected default event '%s' to be enabled", eventName)
		}
	}

	// Log events should be enabled by default for unified event stream.
	for _, name := range []string{"log:info", "log:warn", "log:error"} {
		if !IsEventEnabled(name) {
			t.Errorf("expected '%s' to be enabled by default", name)
		}
	}
}

func TestDiscardOrFlush(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	EnableEvent("test_event")
	Log(ctx, CategoryEvent, "test_event", Field{Key: "test", Value: "value"})

	// DiscardOrFlush should clear the buffer
	_ = tb.DiscardOrFlush(ctx)

	events := tb.Snapshot()
	if len(events) != 0 {
		t.Errorf("expected buffer to be cleared, got %d events", len(events))
	}
}

func TestDiscardOrFlushWithNilHandler(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	EnableEvent("test_event")
	Log(ctx, CategoryEvent, "test_event", Field{Key: "test", Value: "value"})

	// With no global handler set, DiscardOrFlush should not panic
	// (it just returns without doing anything)
	ClearHandler()
	_ = tb.DiscardOrFlush(ctx)

	// Buffer should still be cleared
	events := tb.Snapshot()
	if len(events) != 0 {
		t.Errorf("expected buffer to be cleared, got %d events", len(events))
	}
}

func TestSpanBasic(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	EnableEvent("test_operation")

	// Create a span and end it
	span := StartSpan(ctx, "test_operation", CategoryEvent, Field{Key: "param", Value: "value"})
	span.End()

	events := tb.Snapshot()
	// Should have 2 events: begin and end
	if len(events) != 2 {
		t.Fatalf("expected 2 events (begin+end), got %d", len(events))
	}

	// Check begin event
	if events[0].Phase != PhaseBegin {
		t.Errorf("expected first event to be Begin phase, got %s", events[0].Phase)
	}
	if events[0].Name != "test_operation" {
		t.Errorf("expected operation name 'test_operation', got '%s'", events[0].Name)
	}

	// Check end event has duration
	if events[1].Phase != PhaseEnd {
		t.Errorf("expected second event to be End phase, got %s", events[1].Phase)
	}
	if events[1].Name != "test_operation" {
		t.Errorf("expected end event name 'test_operation', got '%s'", events[1].Name)
	}
	durationFound := false
	for _, field := range events[1].Fields {
		if field.Key == "duration_ms" {
			durationFound = true
			break
		}
	}
	if !durationFound {
		t.Error("expected end event to have duration_ms field")
	}
}

func TestSpanNested(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	EnableEvent("parent_operation")
	EnableEvent("child_operation")

	// Create parent and child spans
	parent := StartSpan(ctx, "parent_operation", CategoryEvent)
	child := parent.StartChild("child_operation", Field{Key: "child_param", Value: "child_value"})
	child.End()
	parent.End()

	events := tb.Snapshot()
	// Should have 4 events: parent_begin, child_begin, child_end, parent_end
	if len(events) != 4 {
		t.Fatalf("expected 4 events (2 spans), got %d", len(events))
	}

	// Verify parent-child relationship through SpanID
	// Parent begin
	if events[0].Name != "parent_operation" || events[0].Phase != PhaseBegin {
		t.Errorf("expected first event to be parent_operation begin")
	}
	// Child begin (should have parent span ID)
	if events[1].Name != "child_operation" || events[1].Phase != PhaseBegin {
		t.Errorf("expected second event to be child_operation begin")
	}
	if len(events[1].SpanID) == 0 {
		t.Error("expected child event to have non-empty SpanID")
	}
}

func TestSpanContext(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	EnableEvent("test_op")

	// Create span and get context
	span := StartSpan(ctx, "test_op", CategoryEvent)
	spanCtx := span.Context()

	// Verify we can get the span from the new context
	retrievedSpan := GetCurrentSpan(spanCtx)
	if retrievedSpan == nil {
		t.Fatal("expected to retrieve span from context")
	}
	if retrievedSpan != span {
		t.Error("retrieved span is not the same as original")
	}

	span.End()
}

func TestSpanAddField(t *testing.T) {
	tb := NewTraceBuf()
	ctx := WithTraceBuf(context.Background(), tb)

	EnableEvent("test_op")

	span := StartSpan(ctx, "test_op", CategoryEvent, Field{Key: "initial", Value: "value"})
	span.AddField("added_field", "added_value")
	span.End()

	events := tb.Snapshot()
	// End event should have both initial and added fields
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Check end event fields (should have initial + added + duration_ms)
	if len(events[1].Fields) != 3 {
		t.Errorf("expected 3 fields in end event, got %d", len(events[1].Fields))
	}
}

func TestResetToDefaultEvents(t *testing.T) {
	DisableAllEvents()
	for _, eventName := range ResetToDefaultEvents() {
		if !IsEventEnabled(eventName) {
			t.Fatalf("expected default event '%s' to be enabled", eventName)
		}
	}
	for _, name := range []string{"log:info", "log:warn", "log:error"} {
		if !IsEventEnabled(name) {
			t.Fatalf("expected %s enabled by default", name)
		}
	}
}

func TestWritePerfettoFileJSON(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "trace.perfetto.json")
	events := []TraceEvent{
		{
			Timestamp: time.Now(),
			Name:      "prompt",
			Phase:     PhaseBegin,
			Category:  CategoryEvent,
			Fields:    []Field{{Key: "message", Value: "hello"}},
		},
		{
			Timestamp: time.Now().Add(2 * time.Millisecond),
			Name:      "prompt",
			Phase:     PhaseEnd,
			Category:  CategoryEvent,
			Fields:    []Field{{Key: "duration_ms", Value: 2}},
		},
	}

	if err := WritePerfettoFile(path, []byte("trace-1"), events); err != nil {
		t.Fatalf("WritePerfettoFile failed: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var doc struct {
		TraceEvents []map[string]any `json:"traceEvents"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("trace file is not valid JSON: %v", err)
	}
	if len(doc.TraceEvents) < 3 {
		t.Fatalf("expected metadata + 2 events, got %d", len(doc.TraceEvents))
	}

	first := doc.TraceEvents[1]
	if first["name"] != "prompt" {
		t.Fatalf("expected first trace event name prompt, got %v", first["name"])
	}
	if first["ph"] != "B" {
		t.Fatalf("expected first trace event phase B, got %v", first["ph"])
	}
	args, ok := first["args"].(map[string]any)
	if !ok || args["message"] != "hello" {
		t.Fatalf("expected args.message=hello, got %#v", first["args"])
	}

	second := doc.TraceEvents[2]
	if second["name"] != "prompt" {
		t.Fatalf("expected second trace event name prompt, got %v", second["name"])
	}
	if second["ph"] != "E" {
		t.Fatalf("expected second trace event phase E, got %v", second["ph"])
	}
}

func TestGenerateTraceIDIsUnique(t *testing.T) {
	id1 := string(GenerateTraceID("session", 1))
	id2 := string(GenerateTraceID("session", 1))
	if id1 == id2 {
		t.Fatalf("expected unique trace IDs, got same id %q", id1)
	}
}

type captureHandler struct {
	events []TraceEvent
}

func (h *captureHandler) Handle(_ context.Context, _ []byte, events []TraceEvent) error {
	h.events = append([]TraceEvent(nil), events...)
	return nil
}

type chunkCaptureHandler struct {
	chunks [][]TraceEvent
	finals []bool
}

func (h *chunkCaptureHandler) Handle(_ context.Context, _ []byte, _ []TraceEvent) error {
	return nil
}

func (h *chunkCaptureHandler) HandleChunk(_ context.Context, _ []byte, events []TraceEvent, final bool) error {
	copied := make([]TraceEvent, len(events))
	copy(copied, events)
	h.chunks = append(h.chunks, copied)
	h.finals = append(h.finals, final)
	return nil
}

func TestDiscardOrFlushAddsOverflowEvent(t *testing.T) {
	tb := NewTraceBuf()
	tb.SetMaxEvents(2)
	ctx := WithTraceBuf(context.Background(), tb)
	EnableEvent("prompt_start")

	Log(ctx, CategoryEvent, "prompt_start")
	Log(ctx, CategoryEvent, "prompt_start")
	Log(ctx, CategoryEvent, "prompt_start") // overflow one event

	h := &captureHandler{}
	SetHandler(h)
	defer ClearHandler()

	if err := tb.DiscardOrFlush(ctx); err != nil {
		t.Fatalf("DiscardOrFlush failed: %v", err)
	}

	found := false
	for _, ev := range h.events {
		if ev.Name == "trace_overflow" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected trace_overflow event in flushed events")
	}
}

func TestTraceBufAutoFlushWhenFull(t *testing.T) {
	tb := NewTraceBuf()
	tb.SetMaxEvents(2)
	ctx := WithTraceBuf(context.Background(), tb)
	EnableEvent("prompt_start")

	h := &chunkCaptureHandler{}
	SetHandler(h)
	defer ClearHandler()

	Log(ctx, CategoryEvent, "prompt_start")
	Log(ctx, CategoryEvent, "prompt_start") // hits threshold, auto-flush

	if got := len(h.chunks); got != 1 {
		t.Fatalf("expected 1 auto-flushed chunk, got %d", got)
	}
	if len(h.chunks[0]) != 2 {
		t.Fatalf("expected 2 events in first chunk, got %d", len(h.chunks[0]))
	}
	if h.finals[0] {
		t.Fatalf("expected auto flush chunk final=false")
	}
	if got := len(tb.Snapshot()); got != 0 {
		t.Fatalf("expected in-memory buffer cleared after auto flush, got %d events", got)
	}

	if err := tb.DiscardOrFlush(ctx); err != nil {
		t.Fatalf("DiscardOrFlush failed: %v", err)
	}
	if got := len(h.finals); got != 2 || !h.finals[1] {
		t.Fatalf("expected finalizing flush as second chunk, finals=%v", h.finals)
	}
}

func TestTraceBufAutoFlushByInterval(t *testing.T) {
	tb := NewTraceBuf()
	tb.SetMaxEvents(1000)
	tb.SetFlushEvery(0)
	tb.SetFlushInterval(10 * time.Millisecond)
	ctx := WithTraceBuf(context.Background(), tb)
	EnableEvent("prompt_start")

	h := &chunkCaptureHandler{}
	SetHandler(h)
	defer ClearHandler()

	Log(ctx, CategoryEvent, "prompt_start")
	time.Sleep(15 * time.Millisecond)
	Log(ctx, CategoryEvent, "prompt_start")

	if got := len(h.chunks); got == 0 {
		t.Fatalf("expected interval-based auto flush chunk, got 0")
	}
	if h.finals[0] {
		t.Fatalf("expected interval auto flush chunk final=false")
	}
}

func TestFileHandlerHandleChunkProducesValidPerfettoJSON(t *testing.T) {
	tmp := t.TempDir()
	h, err := NewFileHandler(tmp)
	if err != nil {
		t.Fatalf("NewFileHandler failed: %v", err)
	}

	traceID := []byte("chunk-trace")
	ev1 := TraceEvent{
		Timestamp: time.Now(),
		Name:      "prompt",
		Phase:     PhaseBegin,
		Category:  CategoryEvent,
	}
	ev2 := TraceEvent{
		Timestamp: time.Now().Add(2 * time.Millisecond),
		Name:      "prompt",
		Phase:     PhaseEnd,
		Category:  CategoryEvent,
		Fields:    []Field{{Key: "duration_ms", Value: 2}},
	}

	if err := h.HandleChunk(context.Background(), traceID, []TraceEvent{ev1}, false); err != nil {
		t.Fatalf("HandleChunk first failed: %v", err)
	}
	if err := h.HandleChunk(context.Background(), traceID, []TraceEvent{ev2}, true); err != nil {
		t.Fatalf("HandleChunk final failed: %v", err)
	}

	path := filepath.Join(tmp, "trace-chunk-trace.perfetto.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var doc struct {
		TraceEvents []map[string]any `json:"traceEvents"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("trace file is not valid JSON: %v", err)
	}
	if len(doc.TraceEvents) != 3 {
		t.Fatalf("expected metadata + 2 events, got %d", len(doc.TraceEvents))
	}
	if doc.TraceEvents[1]["name"] != "prompt" || doc.TraceEvents[2]["name"] != "prompt" {
		t.Fatalf("expected prompt span pair, got %v and %v", doc.TraceEvents[1]["name"], doc.TraceEvents[2]["name"])
	}
}

func TestFileHandlerSplitsLargeTraceIntoParts(t *testing.T) {
	tmp := t.TempDir()
	h, err := NewFileHandler(tmp)
	if err != nil {
		t.Fatalf("NewFileHandler failed: %v", err)
	}
	h.SetMaxFileSizeBytes(700)

	traceID := []byte("split-trace")
	largeText := strings.Repeat("x", 350)
	events := []TraceEvent{
		{
			Timestamp: time.Now(),
			Name:      "log:info",
			Phase:     PhaseInstant,
			Category:  CategoryLog,
			Fields:    []Field{{Key: "msg", Value: largeText}},
		},
		{
			Timestamp: time.Now().Add(1 * time.Millisecond),
			Name:      "log:info",
			Phase:     PhaseInstant,
			Category:  CategoryLog,
			Fields:    []Field{{Key: "msg", Value: largeText}},
		},
		{
			Timestamp: time.Now().Add(2 * time.Millisecond),
			Name:      "log:info",
			Phase:     PhaseInstant,
			Category:  CategoryLog,
			Fields:    []Field{{Key: "msg", Value: largeText}},
		},
	}

	if err := h.HandleChunk(context.Background(), traceID, events, true); err != nil {
		t.Fatalf("HandleChunk split failed: %v", err)
	}

	paths, err := filepath.Glob(filepath.Join(tmp, "trace-split-trace*.perfetto.json"))
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}
	if len(paths) < 2 {
		t.Fatalf("expected split output files, got %d", len(paths))
	}

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile failed for %s: %v", path, err)
		}
		var doc struct {
			TraceEvents []map[string]any `json:"traceEvents"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("trace file %s is not valid JSON: %v", path, err)
		}
		if len(doc.TraceEvents) == 0 {
			t.Fatalf("trace file %s should contain at least metadata event", path)
		}
	}
}

func TestBuildTraceEventJSONToolCallTidStableForSpanPair(t *testing.T) {
	toolCallID := "call_123"
	begin := TraceEvent{
		Timestamp: time.Now(),
		Name:      "tool_execution",
		Phase:     PhaseBegin,
		Category:  CategoryTool,
		Fields: []Field{
			{Key: "tool_call_id", Value: toolCallID},
			{Key: "tool", Value: "read"},
		},
	}
	end := TraceEvent{
		Timestamp: time.Now().Add(1 * time.Millisecond),
		Name:      "tool_execution",
		Phase:     PhaseEnd,
		Category:  CategoryTool,
		Fields: []Field{
			{Key: "tool_call_id", Value: toolCallID},
			{Key: "tool", Value: "read"},
		},
	}

	beginJSON := buildTraceEventJSON(1, begin)
	endJSON := buildTraceEventJSON(1, end)

	beginTid, ok := beginJSON["tid"].(int)
	if !ok {
		t.Fatalf("expected begin tid to be int, got %T", beginJSON["tid"])
	}
	endTid, ok := endJSON["tid"].(int)
	if !ok {
		t.Fatalf("expected end tid to be int, got %T", endJSON["tid"])
	}
	if beginTid != endTid {
		t.Fatalf("expected same tid for span pair, got begin=%d end=%d", beginTid, endTid)
	}
}
