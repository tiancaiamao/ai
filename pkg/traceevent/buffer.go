package traceevent

import (
	"context"
	"crypto/rand"
	"sync"
	"sync/atomic"
	"time"
)

type traceBufKeyType struct{}
type spanKeyType struct{}

var traceBufKey = traceBufKeyType{}
var spanKey = spanKeyType{}
var activeTraceBuf atomic.Pointer[TraceBuf]

// TraceBuf records trace events for a single turn (simplified, no trigger bits)
type TraceBuf struct {
	mu            sync.RWMutex
	events        []TraceEvent
	traceID       []byte
	maxEvents     int
	flushEvery    int
	flushInterval time.Duration
	lastFlushAt   time.Time
	dropped       int
	streamingOpen bool
	sinks         []func(TraceEvent)
}

// WithTraceBuf creates a context with TraceBuf
func WithTraceBuf(ctx context.Context, tb *TraceBuf) context.Context {
	return context.WithValue(ctx, traceBufKey, tb)
}

// GetTraceBuf returns TraceBuf from context
func GetTraceBuf(ctx context.Context) *TraceBuf {
	if ctx != nil {
		if val := ctx.Value(traceBufKey); val != nil {
			return val.(*TraceBuf)
		}
	}
	if tb := activeTraceBuf.Load(); tb != nil {
		return tb
	}
	return nil
}

// SetActiveTraceBuf sets the fallback trace buffer used when context has none.
func SetActiveTraceBuf(tb *TraceBuf) {
	activeTraceBuf.Store(tb)
}

// ClearActiveTraceBuf clears fallback trace buffer if it matches the given buffer.
func ClearActiveTraceBuf(tb *TraceBuf) {
	activeTraceBuf.CompareAndSwap(tb, nil)
}

// NewTraceBuf creates a new TraceBuf
func NewTraceBuf() *TraceBuf {
	return &TraceBuf{
		events:        make([]TraceEvent, 0, 1024),
		maxEvents:     2000,
		flushEvery:    2000,
		flushInterval: time.Second,
		lastFlushAt:   time.Now(),
	}
}

// SetTraceID sets the trace ID for this buffer
func (tb *TraceBuf) SetTraceID(traceID []byte) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.traceID = traceID
}

// SetMaxEvents sets the maximum number of events for testing
func (tb *TraceBuf) SetMaxEvents(max int) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.maxEvents = max
	tb.flushEvery = max
}

// SetFlushEvery sets the event-count threshold that triggers an incremental flush.
// Value <= 0 disables count-based flush (time-based flush may still apply).
func (tb *TraceBuf) SetFlushEvery(every int) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.flushEvery = every
}

// SetFlushInterval sets the max interval between incremental flushes.
// Value <= 0 disables time-based flush.
func (tb *TraceBuf) SetFlushInterval(interval time.Duration) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.flushInterval = interval
}

// AddSink registers a callback invoked for every recorded event.
func (tb *TraceBuf) AddSink(sink func(TraceEvent)) {
	if sink == nil {
		return
	}
	tb.mu.Lock()
	tb.sinks = append(tb.sinks, sink)
	tb.mu.Unlock()
}

// Record records an event to the buffer.
// Callers that have context should prefer RecordWithContext to enable auto-flush.
func (tb *TraceBuf) Record(event TraceEvent) {
	tb.mu.Lock()
	tb.events = append(tb.events, event)
	sinks := append([]func(TraceEvent){}, tb.sinks...)
	if len(tb.events) > tb.maxEvents && GetHandler() == nil {
		// Fallback ring behavior when no handler is configured.
		tb.events = tb.events[1:]
		tb.dropped++
	}
	tb.mu.Unlock()

	for _, sink := range sinks {
		sink(event)
	}
}

// RecordWithContext records an event and opportunistically flushes chunks.
func (tb *TraceBuf) RecordWithContext(ctx context.Context, event TraceEvent) {
	tb.Record(event)
	_ = tb.FlushIfNeeded(ctx)
}

// Snapshot returns a copy of all events
func (tb *TraceBuf) Snapshot() []TraceEvent {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	result := make([]TraceEvent, len(tb.events))
	copy(result, tb.events)
	return result
}

// FlushIfNeeded flushes a chunk when count/time thresholds are reached.
func (tb *TraceBuf) FlushIfNeeded(ctx context.Context) error {
	handler := GetHandler()
	if handler == nil {
		return nil
	}

	now := time.Now()
	tb.mu.Lock()
	flushEvery := tb.flushEvery
	if flushEvery <= 0 {
		flushEvery = tb.maxEvents
	}

	shouldFlushByCount := flushEvery > 0 && len(tb.events) >= flushEvery
	shouldFlushByTime := tb.flushInterval > 0 && len(tb.events) > 0 && now.Sub(tb.lastFlushAt) >= tb.flushInterval
	if !shouldFlushByCount && !shouldFlushByTime {
		tb.mu.Unlock()
		return nil
	}
	events := make([]TraceEvent, len(tb.events))
	copy(events, tb.events)
	dropped := tb.dropped
	tb.events = tb.events[:0]
	tb.dropped = 0
	tb.mu.Unlock()

	events = appendOverflowEvent(events, dropped, tb.maxEvents)
	if err := flushToHandler(ctx, handler, tb.traceID, events, false); err != nil {
		tb.mu.Lock()
		tb.events = append(events, tb.events...)
		tb.mu.Unlock()
		return err
	}

	tb.mu.Lock()
	tb.streamingOpen = true
	tb.lastFlushAt = now
	tb.mu.Unlock()
	return nil
}

// Flush writes current buffered events without finalizing the trace stream.
func (tb *TraceBuf) Flush(ctx context.Context) error {
	handler := GetHandler()
	if handler == nil {
		return nil
	}

	now := time.Now()
	tb.mu.Lock()
	events := make([]TraceEvent, len(tb.events))
	copy(events, tb.events)
	dropped := tb.dropped
	if len(events) == 0 && dropped == 0 {
		tb.mu.Unlock()
		return nil
	}
	tb.events = tb.events[:0]
	tb.dropped = 0
	tb.mu.Unlock()

	events = appendOverflowEvent(events, dropped, tb.maxEvents)
	if err := flushToHandler(ctx, handler, tb.traceID, events, false); err != nil {
		tb.mu.Lock()
		tb.events = append(events, tb.events...)
		tb.mu.Unlock()
		return err
	}

	tb.mu.Lock()
	tb.streamingOpen = true
	tb.lastFlushAt = now
	tb.mu.Unlock()
	return nil
}

// DiscardOrFlush flushes remaining events and finalizes streaming traces.
func (tb *TraceBuf) DiscardOrFlush(ctx context.Context) error {
	handler := GetHandler()
	if handler == nil {
		tb.mu.Lock()
		tb.events = tb.events[:0]
		tb.dropped = 0
		tb.streamingOpen = false
		tb.mu.Unlock()
		return nil
	}

	tb.mu.Lock()
	events := make([]TraceEvent, len(tb.events))
	copy(events, tb.events)
	dropped := tb.dropped
	streamingOpen := tb.streamingOpen
	tb.events = tb.events[:0]
	tb.dropped = 0
	tb.streamingOpen = false
	tb.mu.Unlock()

	events = appendOverflowEvent(events, dropped, tb.maxEvents)
	if len(events) == 0 && !streamingOpen {
		return nil
	}

	if err := flushToHandler(ctx, handler, tb.traceID, events, true); err != nil {
		tb.mu.Lock()
		tb.events = append(events, tb.events...)
		tb.streamingOpen = streamingOpen
		tb.mu.Unlock()
		return err
	}

	return nil
}

func appendOverflowEvent(events []TraceEvent, dropped, maxEvents int) []TraceEvent {
	if dropped == 0 {
		return events
	}
	return append(events, TraceEvent{
		Timestamp: time.Now(),
		Name:      "trace_overflow",
		Phase:     PhaseInstant,
		Category:  CategoryMetrics,
		Fields: []Field{
			{Key: "dropped_events", Value: dropped},
			{Key: "max_events", Value: maxEvents},
		},
	})
}

func flushToHandler(ctx context.Context, handler TraceHandler, traceID []byte, events []TraceEvent, final bool) error {
	if chunkHandler, ok := handler.(ChunkTraceHandler); ok {
		return chunkHandler.HandleChunk(ctx, traceID, events, final)
	}
	if len(events) == 0 {
		return nil
	}
	return handler.Handle(ctx, traceID, events)
}

// WithSpan creates a context with the current span.
func WithSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, spanKey, span)
}

// GetCurrentSpan retrieves the current span from context.
func GetCurrentSpan(ctx context.Context) *Span {
	if val := ctx.Value(spanKey); val != nil {
		return val.(*Span)
	}
	return nil
}

// GenerateSpanID generates a unique span ID.
func GenerateSpanID() []byte {
	b := make([]byte, 8)
	// Prefer random ID over failure
	_, _ = rand.Read(b)
	return b
}
