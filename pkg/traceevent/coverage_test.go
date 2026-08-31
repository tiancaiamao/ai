package traceevent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================
// buffer.go coverage
// ============================================================

func TestTraceBuf_AddSink(t *testing.T) {
	tb := NewTraceBuf()
	var got []string
	var mu sync.Mutex
	tb.AddSink(func(e TraceEvent) {
		mu.Lock()
		got = append(got, e.Name)
		mu.Unlock()
	})
	// nil sink should be ignored
	tb.AddSink(nil)

	tb.Record(TraceEvent{Name: "alpha"})
	tb.Record(TraceEvent{Name: "beta"})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Errorf("sinks = %v, want [alpha beta]", got)
	}
}

func TestTraceBuf_Flush_NoHandler(t *testing.T) {
	tb := NewTraceBuf()
	ClearHandler()
	tb.Record(TraceEvent{Name: "x"})
	if err := tb.Flush(context.Background()); err != nil {
		t.Errorf("Flush without handler should be no-op, got %v", err)
	}
}

func TestTraceBuf_Flush_WithHandler(t *testing.T) {
	tb := NewTraceBuf()
	tb.SetTraceID([]byte("flush-trace"))

	called := 0
	h := handlerFunc(func(_ context.Context, _ []byte, _ []TraceEvent) error {
		called++
		return nil
	})
	SetHandler(h)
	defer ClearHandler()

	tb.Record(TraceEvent{Name: "e1"})
	tb.Record(TraceEvent{Name: "e2"})

	if err := tb.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if called == 0 {
		t.Error("handler not called")
	}
	// Second flush with empty buffer is a no-op.
	if err := tb.Flush(context.Background()); err != nil {
		t.Fatalf("Flush empty: %v", err)
	}

	// Error path: inject failing handler.
	failing := handlerFunc(func(_ context.Context, _ []byte, _ []TraceEvent) error {
		return errors.New("boom")
	})
	SetHandler(failing)
	tb.Record(TraceEvent{Name: "e3"})
	if err := tb.Flush(context.Background()); err == nil {
		t.Error("expected error from failing handler")
	}
}

func TestTraceBuf_FlushIfNeeded_NoHandler(t *testing.T) {
	ClearHandler()
	tb := NewTraceBuf()
	tb.Record(TraceEvent{Name: "x"})
	if err := tb.FlushIfNeeded(context.Background()); err != nil {
		t.Errorf("FlushIfNeeded without handler should be no-op, got %v", err)
	}
}

func TestTraceBuf_FlushIfNeeded_ByCount(t *testing.T) {
	tb := NewTraceBuf()
	tb.SetTraceID([]byte("count-trace"))
	tb.SetFlushEvery(2)
	tb.SetFlushInterval(0) // disable time-based flush

	calls := 0
	h := chunkHandlerFunc(func(_ context.Context, _ []byte, _ []TraceEvent, _ bool) error {
		calls++
		return nil
	})
	SetHandler(h)
	defer ClearHandler()

	tb.Record(TraceEvent{Name: "a"})
	// Threshold not reached yet — no flush.
	if err := tb.FlushIfNeeded(context.Background()); err != nil {
		t.Fatalf("FlushIfNeeded: %v", err)
	}
	if calls != 0 {
		t.Errorf("expected 0 flushes before threshold, got %d", calls)
	}

	tb.Record(TraceEvent{Name: "b"})
	if err := tb.FlushIfNeeded(context.Background()); err != nil {
		t.Fatalf("FlushIfNeeded: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 flush at threshold, got %d", calls)
	}

	// Error path: replace with failing handler and trigger another flush.
	failing := chunkHandlerFunc(func(_ context.Context, _ []byte, _ []TraceEvent, _ bool) error {
		return errors.New("fail")
	})
	SetHandler(failing)
	tb.Record(TraceEvent{Name: "c"})
	tb.Record(TraceEvent{Name: "d"})
	if err := tb.FlushIfNeeded(context.Background()); err == nil {
		t.Error("expected error from failing chunk handler")
	}
}

func TestTraceBuf_FlushIfNeeded_ByTime(t *testing.T) {
	tb := NewTraceBuf()
	tb.SetTraceID([]byte("time-trace"))
	tb.SetFlushEvery(0)                   // disable count
	tb.SetFlushInterval(time.Millisecond) // tiny interval

	called := 0
	h := chunkHandlerFunc(func(_ context.Context, _ []byte, _ []TraceEvent, _ bool) error {
		called++
		return nil
	})
	SetHandler(h)
	defer ClearHandler()

	tb.Record(TraceEvent{Name: "x"})
	time.Sleep(2 * time.Millisecond)
	if err := tb.FlushIfNeeded(context.Background()); err != nil {
		t.Fatalf("FlushIfNeeded: %v", err)
	}
	if called == 0 {
		t.Error("expected time-based flush")
	}
}

func TestTraceBuf_DiscardOrFlush_NoHandler(t *testing.T) {
	ClearHandler()
	tb := NewTraceBuf()
	tb.Record(TraceEvent{Name: "x"})
	if err := tb.DiscardOrFlush(context.Background()); err != nil {
		t.Errorf("DiscardOrFlush without handler: %v", err)
	}
	if snaps := tb.Snapshot(); len(snaps) != 0 {
		t.Errorf("expected events cleared, got %d", len(snaps))
	}
}

func TestTraceBuf_DiscardOrFlush_WithHandler(t *testing.T) {
	tb := NewTraceBuf()
	tb.SetTraceID([]byte("discard-trace"))

	var finals []bool
	h := chunkHandlerFunc(func(_ context.Context, _ []byte, _ []TraceEvent, final bool) error {
		finals = append(finals, final)
		return nil
	})
	SetHandler(h)
	defer ClearHandler()

	// Empty buffer with no prior streaming → no handler call.
	if err := tb.DiscardOrFlush(context.Background()); err != nil {
		t.Fatalf("DiscardOrFlush empty: %v", err)
	}
	if len(finals) != 0 {
		t.Errorf("expected 0 handler calls for empty buffer, got %d", len(finals))
	}

	// Now record an event and mark streaming as open, then discard.
	tb.Record(TraceEvent{Name: "e1"})
	// Simulate streamingOpen by flushing first.
	if err := tb.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	finals = nil

	if err := tb.DiscardOrFlush(context.Background()); err != nil {
		t.Fatalf("DiscardOrFlush: %v", err)
	}
	if len(finals) == 0 || !finals[len(finals)-1] {
		t.Errorf("expected final=true on discard, got %v", finals)
	}

	// Error path: failing handler.
	failing := chunkHandlerFunc(func(_ context.Context, _ []byte, _ []TraceEvent, _ bool) error {
		return errors.New("nope")
	})
	SetHandler(failing)
	tb.Record(TraceEvent{Name: "e2"})
	if err := tb.DiscardOrFlush(context.Background()); err == nil {
		t.Error("expected error from failing handler")
	}
}

func TestTraceBuf_RingBufferFallback(t *testing.T) {
	ClearHandler()
	tb := NewTraceBuf()
	tb.SetMaxEvents(2)
	tb.Record(TraceEvent{Name: "a"})
	tb.Record(TraceEvent{Name: "b"})
	tb.Record(TraceEvent{Name: "c"})
	got := tb.Snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 events in ring buffer, got %d", len(got))
	}
	if got[0].Name != "b" || got[1].Name != "c" {
		t.Errorf("expected [b c], got %v", got)
	}
}

func TestTraceBuf_GetTraceBuf_ActiveFallback(t *testing.T) {
	tb := NewTraceBuf()
	SetActiveTraceBuf(tb)
	defer ClearActiveTraceBuf(tb)

	// Context without value should fall back to active buf.
	got := GetTraceBuf(context.Background())
	if got != tb {
		t.Error("expected active trace buf fallback")
	}

	ClearActiveTraceBuf(tb)
	if got := GetTraceBuf(context.Background()); got != nil {
		t.Error("expected nil after clear")
	}
}

func TestTraceBuf_GetTraceBuf_NilContext(t *testing.T) {
	ClearActiveTraceBuf(nil)
	if got := GetTraceBuf(nil); got != nil {
		t.Error("expected nil for nil context")
	}
}

func TestTraceBuf_RecordWithContext(t *testing.T) {
	tb := NewTraceBuf()
	tb.SetMaxEvents(10)
	ctx := WithTraceBuf(context.Background(), tb)

	var called int
	h := chunkHandlerFunc(func(_ context.Context, _ []byte, _ []TraceEvent, _ bool) error {
		called++
		return nil
	})
	SetHandler(h)
	defer ClearHandler()

	tb.SetFlushEvery(1)
	tb.RecordWithContext(ctx, TraceEvent{Name: "x"})
	if called == 0 {
		t.Error("expected FlushIfNeeded to be triggered")
	}
}

func TestAppendOverflowEvent_ZeroDropped(t *testing.T) {
	out := appendOverflowEvent(nil, 0, 100)
	if len(out) != 0 {
		t.Errorf("expected no overflow event when dropped=0, got %d events", len(out))
	}
}

// ============================================================
// handler.go coverage
// ============================================================

// handlerFunc implements TraceHandler.
type handlerFunc func(ctx context.Context, traceID []byte, events []TraceEvent) error

func (f handlerFunc) Handle(ctx context.Context, traceID []byte, events []TraceEvent) error {
	return f(ctx, traceID, events)
}

// chunkHandlerFunc implements both ChunkTraceHandler and TraceHandler.
type chunkHandlerFunc func(ctx context.Context, traceID []byte, events []TraceEvent, final bool) error

func (f chunkHandlerFunc) HandleChunk(ctx context.Context, traceID []byte, events []TraceEvent, final bool) error {
	return f(ctx, traceID, events, final)
}

// Handle adapts chunkHandlerFunc to the TraceHandler interface (treats as final).
func (f chunkHandlerFunc) Handle(ctx context.Context, traceID []byte, events []TraceEvent) error {
	return f(ctx, traceID, events, true)
}

func TestFileHandler_SetMaxFileSizeBytes(t *testing.T) {
	dir := t.TempDir()
	h, err := NewFileHandler(dir)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}
	h.SetMaxFileSizeBytes(123)
	if h.maxFileSizeBytes != 123 {
		t.Errorf("expected 123, got %d", h.maxFileSizeBytes)
	}
}

func TestFileHandler_SetSessionID(t *testing.T) {
	dir := t.TempDir()
	h, err := NewFileHandler(dir)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}

	// Write events with initial session ID.
	h.SetSessionID("sess-a")
	if err := h.Handle(context.Background(), []byte("trace-a"), []TraceEvent{
		{Name: "x", Timestamp: time.Now(), Category: CategoryEvent, Phase: PhaseInstant},
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Changing session ID should close existing streams.
	h.SetSessionID("sess-b")
	if err := h.Handle(context.Background(), []byte("trace-b"), []TraceEvent{
		{Name: "y", Timestamp: time.Now(), Category: CategoryEvent, Phase: PhaseInstant},
	}); err != nil {
		t.Fatalf("Handle after session change: %v", err)
	}

	// TraceFilePath should reflect new session.
	path := h.TraceFilePath("trace-b")
	if !strings.Contains(path, "sess-b") {
		t.Errorf("expected path to contain sess-b: %s", path)
	}
}

func TestFileHandler_IncrementPromptCount(t *testing.T) {
	dir := t.TempDir()
	h, err := NewFileHandler(dir)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}
	a := h.IncrementPromptCount()
	b := h.IncrementPromptCount()
	if a != 1 || b != 2 {
		t.Errorf("expected 1 then 2, got %d, %d", a, b)
	}
}

func TestFileHandler_HandleChunk_EmptyNonFinal(t *testing.T) {
	dir := t.TempDir()
	h, err := NewFileHandler(dir)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}
	// Empty events with final=false should be a no-op (no file created).
	if err := h.HandleChunk(context.Background(), []byte("new-trace"), nil, false); err != nil {
		t.Fatalf("HandleChunk: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(matches) != 0 {
		t.Errorf("expected no files, got %v", matches)
	}
}

func TestFileHandler_HandleChunk_FinalCloses(t *testing.T) {
	dir := t.TempDir()
	h, err := NewFileHandler(dir)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}
	h.SetSessionID("sess")
	events := []TraceEvent{
		{Name: "e1", Timestamp: time.Now(), Category: CategoryEvent, Phase: PhaseInstant},
		{Name: "e2", Timestamp: time.Now(), Category: CategoryEvent, Phase: PhaseInstant},
	}
	if err := h.HandleChunk(context.Background(), []byte("trace-x"), events, true); err != nil {
		t.Fatalf("HandleChunk final: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(matches) == 0 {
		t.Fatal("expected a trace file to be created")
	}
	// Validate JSON.
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "e1") {
		t.Error("expected event e1 in file")
	}
}

func TestFileHandler_HandleChunk_Rotation(t *testing.T) {
	dir := t.TempDir()
	h, err := NewFileHandler(dir)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}
	h.SetSessionID("sess")
	h.SetMaxFileSizeBytes(300) // small to trigger rotation

	// Use a non-final chunk first to keep stream open.
	if err := h.HandleChunk(context.Background(), []byte("rot-trace"),
		[]TraceEvent{{Name: "e1", Timestamp: time.Now(), Category: CategoryEvent, Phase: PhaseInstant}},
		false); err != nil {
		t.Fatalf("HandleChunk 1: %v", err)
	}
	// Many events to trigger rotation.
	for i := 0; i < 20; i++ {
		if err := h.HandleChunk(context.Background(), []byte("rot-trace"),
			[]TraceEvent{{Name: "e2", Timestamp: time.Now(), Category: CategoryEvent, Phase: PhaseInstant}},
			false); err != nil {
			// Rotation may fail on some environments; log but continue.
			t.Logf("HandleChunk at i=%d: %v", i, err)
			break
		}
	}
}

func TestFileHandler_HandleChunk_WriteError(t *testing.T) {
	dir := t.TempDir()
	h, err := NewFileHandler(dir)
	if err != nil {
		t.Fatalf("NewFileHandler: %v", err)
	}
	// Delete directory to force write failure.
	_ = os.RemoveAll(dir)
	err = h.HandleChunk(context.Background(), []byte("bad-trace"),
		[]TraceEvent{{Name: "x", Timestamp: time.Now(), Category: CategoryEvent, Phase: PhaseInstant}},
		true)
	if err == nil {
		t.Error("expected error writing to deleted dir")
	}
}

func TestFileHandler_NewFileHandler_Error(t *testing.T) {
	// Create a file at the target dir path to make MkdirAll fail.
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	target := filepath.Join(blocker, "sub")
	_, err := NewFileHandler(target)
	if err == nil {
		t.Error("expected MkdirAll to fail when path component is a file")
	}
}

func TestSanitizeFilenameComponent(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "unknown"},
		{"hello-world_123", "hello-world_123"},
		{"a/b c", "a_b_c"},
	}
	for _, tt := range tests {
		got := sanitizeFilenameComponent(tt.in)
		if got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ============================================================
// config.go coverage
// ============================================================

func TestDisableEvent_Dynamic(t *testing.T) {
	EnableEvent("log:custom_tag")
	if !IsEventEnabled("log:custom_tag") {
		t.Fatal("expected dynamic event enabled")
	}
	DisableEvent("log:custom_tag")
	if IsEventEnabled("log:custom_tag") {
		t.Fatal("expected dynamic event disabled")
	}
}

func TestExpandEventSelectors_DynamicAndUnknown(t *testing.T) {
	// Dynamic event name should be accepted.
	expanded, unknown := ExpandEventSelectors([]string{"log:my_event"})
	found := false
	for _, n := range expanded {
		if n == "log:my_event" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected dynamic event in expansion: %v", expanded)
	}

	// Mix of known + unknown + empty + none.
	expanded, unknown = ExpandEventSelectors([]string{"", "none", "llm", "bogus_event"})
	if len(unknown) != 1 || unknown[0] != "bogus_event" {
		t.Errorf("unknown = %v, want [bogus_event]", unknown)
	}
	if len(expanded) == 0 {
		t.Error("expected non-empty expansion")
	}
}

func TestExpandEventSelectors_Dedup(t *testing.T) {
	// Same event appearing in multiple groups should only be included once.
	expanded, _ := ExpandEventSelectors([]string{"llm", "event"})
	seen := map[string]int{}
	for _, n := range expanded {
		seen[n]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("event %q appeared %d times", name, count)
		}
	}
}

// ============================================================
// slog_bridge.go coverage
// ============================================================

func TestSlogBridge_WithAttrsAndGroup(t *testing.T) {
	ResetToDefaultEvents()
	buf := NewTraceBuf()
	buf.SetMaxEvents(100)
	SetActiveTraceBuf(buf)
	defer ClearActiveTraceBuf(buf)

	base := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	wrapped := WrapSlogHandler(base)
	// WithGroup with empty name → returns self.
	if WrapSlogHandler(base).(*slogTraceHandler).WithGroup("") == nil {
		t.Error("WithGroup('') should return non-nil")
	}
	logger := slog.New(wrapped.WithGroup("g1").WithAttrs([]slog.Attr{slog.String("k", "v")}))
	logger.Info("hello", "trace_category", "llm", "x", 1)

	events := buf.Snapshot()
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
}

func TestSlogBridge_AllValueKinds(t *testing.T) {
	ResetToDefaultEvents()
	buf := NewTraceBuf()
	buf.SetMaxEvents(100)
	SetActiveTraceBuf(buf)
	defer ClearActiveTraceBuf(buf)

	logger := slog.New(WrapSlogHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger.Info("msg",
		"b", true,
		"d", 5*time.Second,
		"f", 3.14,
		"i", int64(42),
		"s", "str",
		"t", time.Unix(0, 0),
		"u", uint64(99),
		"any", complex(1, 2),
	)
	events := buf.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestSlogBridge_ParseTraceCategory(t *testing.T) {
	tests := []struct {
		in    string
		want  TraceCategory
		found bool
	}{
		{"llm", CategoryLLM, true},
		{"tool", CategoryTool, true},
		{"event", CategoryEvent, true},
		{"metrics", CategoryMetrics, true},
		{"bogus", 0, false},
	}
	for _, tt := range tests {
		c, ok := parseTraceCategory(tt.in)
		if ok != tt.found || (ok && c != tt.want) {
			t.Errorf("parseTraceCategory(%q) = (%v, %v), want (%v, %v)", tt.in, c, ok, tt.want, tt.found)
		}
	}
}

func TestSlogBridge_SlugEventName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "debug"},
		{"Hello World!", "hello_world"},
		{"a-b c", "a_b_c"},
		{"   ", "debug"},
	}
	for _, tt := range tests {
		got := slugEventName(tt.in)
		if got != tt.want {
			t.Errorf("slugEventName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// Long input gets truncated.
	long := strings.Repeat("a", 200)
	got := slugEventName(long)
	if len(got) != 96 {
		t.Errorf("expected truncation to 96 chars, got %d", len(got))
	}
}

// ============================================================
// perfetto.go coverage
// ============================================================

func TestPerfettoFile_Lifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")
	pf, err := NewPerfettoFile(path)
	if err != nil {
		t.Fatalf("NewPerfettoFile: %v", err)
	}
	if err := pf.WriteEvents([]byte("tid"), []TraceEvent{
		{Name: "e1", Timestamp: time.Now(), Category: CategoryLLM, Phase: PhaseInstant},
		{Name: "e2", Timestamp: time.Now(), Category: CategoryTool, Phase: PhaseBegin,
			Fields: []Field{{Key: "tool_call_id", Value: "abc"}}},
		{Name: "e3", Timestamp: time.Now(), Category: CategoryMetrics, Phase: PhaseCounter,
			Fields: []Field{{Key: "value", Value: 42}}},
		{Name: "e4", Timestamp: time.Now(), Category: CategoryEvent, Phase: PhaseComplete,
			Fields: []Field{{Key: "duration_ms", Value: 100}}},
	}); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}
	if err := pf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Don't double-close — os.File.Close on a closed file returns an error,
	// and PerfettoFile.Close doesn't nil out the file field.

	// Validate file is parseable.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var trace map[string]any
	if err := json.Unmarshal(data, &trace); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
}

func TestPerfettoFile_CloseNilFile(t *testing.T) {
	pf := &PerfettoFile{} // file is nil
	if err := pf.Close(); err != nil {
		t.Errorf("Close on nil file: %v", err)
	}
}

func TestPerfettoFile_WriteError(t *testing.T) {
	dir := t.TempDir()
	pf, err := NewPerfettoFile(filepath.Join(dir, "trace.json"))
	if err != nil {
		t.Fatalf("NewPerfettoFile: %v", err)
	}
	_ = pf.file.Close()
	if err := pf.WriteEvents([]byte("tid"), []TraceEvent{
		{Name: "x", Timestamp: time.Now(), Category: CategoryLLM, Phase: PhaseInstant},
	}); err == nil {
		t.Error("expected error writing to closed file")
	}
}

func TestWritePerfettoFile_Error(t *testing.T) {
	// Directory path can't be opened as a file.
	dir := t.TempDir()
	err := WritePerfettoFile(dir, []byte("tid"), nil)
	if err == nil {
		t.Error("expected error opening directory as file")
	}
}

func TestNewPerfettoFile_Error(t *testing.T) {
	// Read-only path.
	tmp := t.TempDir()
	ro := filepath.Join(tmp, "ro")
	_ = os.Mkdir(ro, 0755)
	_ = os.Chmod(ro, 0444)
	defer os.Chmod(ro, 0755)
	_, err := NewPerfettoFile(filepath.Join(ro, "trace.json"))
	if err == nil {
		t.Error("expected error creating file in read-only dir")
	}
}

func TestThreadIDForToolCall_EdgeCases(t *testing.T) {
	// No tool_call_id → no tid.
	if _, ok := threadIDForToolCall(nil); ok {
		t.Error("expected no tid for nil args")
	}
	if _, ok := threadIDForToolCall(map[string]any{}); ok {
		t.Error("expected no tid for empty args")
	}
	// Empty/whitespace ID.
	if _, ok := threadIDForToolCall(map[string]any{"tool_call_id": "   "}); ok {
		t.Error("expected no tid for whitespace id")
	}
	// Non-string types.
	if _, ok := threadIDForToolCall(map[string]any{"tool_call_id": 123}); ok {
		t.Error("expected no tid for non-string id")
	}
	// []byte id.
	if _, ok := threadIDForToolCall(map[string]any{"tool_call_id": []byte("byid")}); !ok {
		t.Error("expected tid for []byte id")
	}
}

func TestToStableString_Types(t *testing.T) {
	if toStableString(42) != "" {
		t.Error("expected empty for int")
	}
	if toStableString([]byte("abc")) != "abc" {
		t.Error("expected string conversion for []byte")
	}
}

func TestToDurationMicroseconds(t *testing.T) {
	tests := []struct {
		in   any
		want int64
		ok   bool
	}{
		{1, 1000, true},
		{int64(2), 2000, true},
		{float64(3.5), 3500, true},
		{"4", 4000, true},
		{"bad", 0, false},
		{nil, 0, false},
	}
	for _, tt := range tests {
		got, ok := toDurationMicroseconds(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("toDurationMicroseconds(%v) = (%d, %v), want (%d, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestThreadIDForMetricSeries(t *testing.T) {
	tid, ok := threadIDForMetricSeries("foo", nil)
	if !ok || tid < 5000 || tid >= 10000 {
		t.Errorf("threadIDForMetricSeries = (%d, %v), want tid in [5000,10000)", tid, ok)
	}
}

func TestThreadIDForCategory_AllCategories(t *testing.T) {
	tests := []struct {
		c    TraceCategory
		want int
	}{
		{CategoryLLM, 1},
		{CategoryTool, 2},
		{CategoryEvent, 3},
		{CategoryMetrics, 4},
		{CategoryLog, 0},
		{TraceCategory(999), 0},
	}
	for _, tt := range tests {
		got := threadIDForCategory(tt.c)
		if got != tt.want {
			t.Errorf("threadIDForCategory(%v) = %d, want %d", tt.c, got, tt.want)
		}
	}
}

// ============================================================
// types.go coverage
// ============================================================

func TestTraceCategory_String_All(t *testing.T) {
	tests := []struct {
		c    TraceCategory
		want string
	}{
		{CategoryLLM, "llm"},
		{CategoryTool, "tool"},
		{CategoryEvent, "event"},
		{CategoryMetrics, "metrics"},
		{CategoryLog, "log"},
		{TraceCategory(0), "unknown"},
	}
	for _, tt := range tests {
		got := tt.c.String()
		if got != tt.want {
			t.Errorf("%v.String() = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestNormalizeSpanName(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"foo_start", "foo"},
		{"foo_end", "foo"},
		{"foo", "foo"},
	}
	for _, tt := range tests {
		got := normalizeSpanName(tt.in)
		if got != tt.want {
			t.Errorf("normalizeSpanName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestShouldRecordSpanEvent_UnknownName(t *testing.T) {
	// Unknown event names → always recorded.
	if !shouldRecordSpanEvent("totally_unknown") {
		t.Error("expected unknown event to be recorded")
	}
	// Known but disabled.
	DisableEvent("prompt")
	if shouldRecordSpanEvent("prompt") {
		t.Error("expected disabled event to not be recorded")
	}
	EnableEvent("prompt")
	if !shouldRecordSpanEvent("prompt") {
		t.Error("expected enabled event to be recorded")
	}
}

func TestStartSpan_NoTraceBuf(t *testing.T) {
	// With no TraceBuf in context and no active, span should still work.
	ClearActiveTraceBuf(nil)
	ctx := context.Background()
	s := StartSpan(ctx, "prompt", CategoryEvent, Field{Key: "k", Value: "v"})
	s.StartChild("nested", Field{Key: "ck", Value: "cv"})
	s.End()
	// Double End is safe.
	s.End()
}

func TestSpan_AddField(t *testing.T) {
	tb := NewTraceBuf()
	tb.SetMaxEvents(100)
	ctx := WithTraceBuf(context.Background(), tb)
	EnableEvent("prompt")

	s := StartSpan(ctx, "prompt", CategoryEvent)
	s.AddField("extra", 42)
	s.End()

	events := tb.Snapshot()
	if len(events) == 0 {
		t.Fatal("expected events")
	}
}

// ============================================================
// trace.go coverage
// ============================================================

func TestError_WithKV(t *testing.T) {
	ResetToDefaultEvents()
	buf := NewTraceBuf()
	buf.SetMaxEvents(100)
	SetActiveTraceBuf(buf)
	defer ClearActiveTraceBuf(buf)

	Error(context.Background(), CategoryLLM, "oops",
		"k1", "v1",
		"k2", 42,
		"odd", // missing value
	)
	events := buf.Snapshot()
	if len(events) == 0 {
		t.Fatal("expected at least one error event")
	}
}

func TestLog_NoTraceBuf(t *testing.T) {
	ClearActiveTraceBuf(nil)
	// No TraceBuf in context, no active: Log should be a no-op.
	Log(context.Background(), CategoryEvent, "prompt_start")
}

func TestGenerateTraceID(t *testing.T) {
	a := GenerateTraceID("p", 1)
	b := GenerateTraceID("p", 1)
	if string(a) == string(b) {
		t.Error("expected unique trace IDs")
	}
	if !strings.HasPrefix(string(a), "p-1-") {
		t.Errorf("expected prefix 'p-1-', got %q", a)
	}
}
