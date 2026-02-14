package traceevent

import (
	"context"
	"strings"
	"time"
)

// TraceCategory represents event categories (bit flags for efficiency)
type TraceCategory uint64

const (
	CategoryLLM     TraceCategory = 1 << iota // LLM calls
	CategoryTool                              // Tool execution
	CategoryEvent                             // Agent events
	CategoryMetrics                           // Performance metrics
	CategoryLog                               // Log events (from slog)
)

// String returns the string representation of a TraceCategory.
func (c TraceCategory) String() string {
	switch c {
	case CategoryLLM:
		return "llm"
	case CategoryTool:
		return "tool"
	case CategoryEvent:
		return "event"
	case CategoryMetrics:
		return "metrics"
	case CategoryLog:
		return "log"
	default:
		return "unknown"
	}
}

// Phase represents trace event phase
type Phase string

const (
	PhaseBegin    Phase = "B"
	PhaseEnd      Phase = "E"
	PhaseComplete Phase = "X"
	PhaseInstant  Phase = "I"
	PhaseCounter  Phase = "C"
)

// Field is a key-value pair for event data
type Field struct {
	Key   string
	Value interface{}
}

// TraceEvent represents a single trace event
type TraceEvent struct {
	Timestamp time.Time
	Name      string
	Phase     Phase // B, E, X, I, C
	TraceID   []byte
	SpanID    []byte // Parent span ID for nested events
	Fields    []Field
	Category  TraceCategory
}

// Span represents an operation with automatic begin/end tracking
type Span struct {
	ctx       context.Context
	name      string
	category  TraceCategory
	fields    []Field
	startTime time.Time
	spanID    []byte
	parentID  []byte
	ended     bool
}

// StartSpan begins a new span and returns it.
// The span will automatically record begin event on creation.
// Call span.End() to record end event with duration.
func StartSpan(ctx context.Context, name string, category TraceCategory, fields ...Field) *Span {
	spanID := GenerateSpanID()
	startTime := time.Now()
	spanName := normalizeSpanName(name)

	tb := GetTraceBuf(ctx)
	if tb != nil && shouldRecordSpanEvent(spanName) {
		// Record begin event
		tb.RecordWithContext(ctx, TraceEvent{
			Timestamp: startTime,
			Name:      spanName,
			Phase:     PhaseBegin,
			Category:  category,
			SpanID:    spanID,
			Fields:    fields,
		})
	}

	return &Span{
		ctx:       ctx,
		name:      spanName,
		category:  category,
		fields:    fields,
		startTime: startTime,
		spanID:    spanID,
		parentID:  getParentSpanID(ctx),
		ended:     false,
	}
}

// StartChild begins a child span of this span.
func (s *Span) StartChild(name string, fields ...Field) *Span {
	childSpanID := GenerateSpanID()
	startTime := time.Now()
	spanName := normalizeSpanName(name)

	tb := GetTraceBuf(s.ctx)
	if tb != nil && shouldRecordSpanEvent(spanName) {
		// Record begin event
		tb.RecordWithContext(s.ctx, TraceEvent{
			Timestamp: startTime,
			Name:      spanName,
			Phase:     PhaseBegin,
			Category:  s.category,
			SpanID:    childSpanID,
			Fields:    fields,
		})
	}

	return &Span{
		ctx:       s.ctx,
		name:      spanName,
		category:  s.category,
		fields:    fields,
		startTime: startTime,
		spanID:    childSpanID,
		parentID:  s.spanID,
		ended:     false,
	}
}

// End marks the span as complete and records end event with duration.
// Safe to call multiple times (only records once).
func (s *Span) End() {
	if s.ended {
		return
	}
	s.ended = true

	endTime := time.Now()
	duration := endTime.Sub(s.startTime)

	tb := GetTraceBuf(s.ctx)
	if tb != nil && shouldRecordSpanEvent(s.name) {
		// Create end event with duration
		endFields := append(s.fields, Field{Key: "duration_ms", Value: duration.Milliseconds()})

		endEvent := TraceEvent{
			Timestamp: endTime,
			Name:      s.name,
			Phase:     PhaseEnd,
			Category:  s.category,
			SpanID:    s.spanID,
			Fields:    endFields,
		}

		// Record end event
		tb.RecordWithContext(s.ctx, endEvent)
	}
}

// AddField adds a field to the span (can be called before End).
func (s *Span) AddField(key string, value interface{}) {
	s.fields = append(s.fields, Field{Key: key, Value: value})
}

// Context returns a context with this span for creating child spans.
func (s *Span) Context() context.Context {
	return WithSpan(s.ctx, s)
}

// getParentSpanID extracts parent span ID from context.
func getParentSpanID(ctx context.Context) []byte {
	if span := GetCurrentSpan(ctx); span != nil {
		return span.spanID
	}
	return nil
}

func normalizeSpanName(name string) string {
	if strings.HasSuffix(name, "_start") {
		return strings.TrimSuffix(name, "_start")
	}
	if strings.HasSuffix(name, "_end") {
		return strings.TrimSuffix(name, "_end")
	}
	return name
}

func shouldRecordSpanEvent(name string) bool {
	baseName := normalizeSpanName(name)
	_, baseKnown := eventNameToBit[baseName]
	_, startKnown := eventNameToBit[baseName+"_start"]
	_, endKnown := eventNameToBit[baseName+"_end"]
	if !baseKnown && !startKnown && !endKnown {
		return true
	}
	return IsEventEnabled(baseName) || IsEventEnabled(baseName+"_start") || IsEventEnabled(baseName+"_end")
}
