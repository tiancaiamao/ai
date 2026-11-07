package traceevent

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

var traceIDGen atomic.Uint64

// GenerateTraceID generates a unique trace ID.
func GenerateTraceID(prefix string, seq int) []byte {
	id := traceIDGen.Add(1)
	return []byte(fmt.Sprintf("%s-%d-%d", prefix, seq, id))
}

// Log records a trace event if enabled.
func Log(ctx context.Context, category TraceCategory, name string, fields ...Field) {
	tb := GetTraceBuf(ctx)
	if tb == nil {
		return
	}

	if !IsEventEnabled(name) {
		return
	}

	event := TraceEvent{
		Timestamp: time.Now(),
		Name:      name,
		Phase:     PhaseInstant,
		Category:  category,
		Fields:    fields,
	}
	tb.RecordWithContext(ctx, event)
}

// DebugLog records a debug log event (convenience wrapper, NOT for filtering).

// WarnLog records a warning log event (convenience wrapper, NOT for filtering).

// Error records an error-level instant event using key/value pairs.
func Error(ctx context.Context, category TraceCategory, message string, kv ...any) {
	fields := fieldsFromKV(kv...)
	// Add message as a field
	fields = append([]Field{{Key: "message", Value: message}}, fields...)
	Log(ctx, category, "log:error", fields...)
}

func fieldsFromKV(kv ...any) []Field {
	if len(kv) == 0 {
		return nil
	}
	fields := make([]Field, 0, (len(kv)+1)/2)
	for i := 0; i < len(kv); i += 2 {
		key := fmt.Sprintf("arg_%d", i)
		if s, ok := kv[i].(string); ok && s != "" {
			key = s
		}
		var value any
		if i+1 < len(kv) {
			value = kv[i+1]
		} else {
			value = "<missing>"
		}
		fields = append(fields, Field{Key: key, Value: value})
	}
	return fields
}
