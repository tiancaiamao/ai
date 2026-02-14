package traceevent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

// WrapSlogHandler wraps a slog handler and mirrors records into trace events.
func WrapSlogHandler(next slog.Handler) slog.Handler {
	return &slogTraceHandler{next: next}
}

type slogTraceHandler struct {
	next   slog.Handler
	attrs  []slog.Attr
	groups []string
}

func (h *slogTraceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return true
}

func (h *slogTraceHandler) Handle(ctx context.Context, r slog.Record) error {
	// Only convert to trace events, don't call original handler (no log file writing)
	fields, category := h.traceFieldsAndCategory(r)

	eventName := slogEventName(r.Level, r.Message)

	// Record the event with proper name
	// Add message as a field
	logFields := append([]Field{{Key: "message", Value: r.Message}}, fields...)
	Log(ctx, category, eventName, logFields...)

	return nil
}

func (h *slogTraceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	combined = append(combined, h.attrs...)
	combined = append(combined, attrs...)

	var next slog.Handler
	if h.next != nil {
		next = h.next.WithAttrs(attrs)
	}
	return &slogTraceHandler{
		next:   next,
		attrs:  combined,
		groups: append([]string(nil), h.groups...),
	}
}

func (h *slogTraceHandler) WithGroup(name string) slog.Handler {
	if strings.TrimSpace(name) == "" {
		return h
	}

	var next slog.Handler
	if h.next != nil {
		next = h.next.WithGroup(name)
	}
	groups := append([]string(nil), h.groups...)
	groups = append(groups, name)
	return &slogTraceHandler{
		next:   next,
		attrs:  append([]slog.Attr(nil), h.attrs...),
		groups: groups,
	}
}

func (h *slogTraceHandler) traceFieldsAndCategory(r slog.Record) ([]Field, TraceCategory) {
	fields := make([]Field, 0, len(h.attrs)+8)
	category := CategoryLog // Slog logs always use CategoryLog

	fields = append(fields, Field{Key: "level", Value: levelString(r.Level)})
	if r.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{r.PC})
		frame, _ := frames.Next()
		if frame.File != "" {
			fields = append(fields, Field{Key: "source", Value: fmt.Sprintf("%s:%d", filepath.Base(frame.File), frame.Line)})
		}
	}

	for _, a := range h.attrs {
		// Allow explicit trace_category override, but default to CategoryLog
		if a.Key == "trace_category" {
			if c, ok := parseTraceCategory(a.Value); ok {
				category = c
			}
			continue
		}
		fields = append(fields, Field{Key: a.Key, Value: slogValueToAny(a.Value)})
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "trace_category" {
			if c, ok := parseTraceCategory(a.Value); ok {
				category = c
			}
			return true
		}
		fields = append(fields, Field{Key: a.Key, Value: slogValueToAny(a.Value)})
		return true
	})

	return fields, category
}

func slogValueToAny(v slog.Value) any {
	switch v.Kind() {
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindString:
		return v.String()
	case slog.KindTime:
		return v.Time()
	case slog.KindUint64:
		return v.Uint64()
	default:
		return v.String()
	}
}

func parseTraceCategory(v any) (TraceCategory, bool) {
	raw := strings.ToLower(strings.TrimSpace(fmt.Sprint(v)))
	switch raw {
	case "llm":
		return CategoryLLM, true
	case "tool":
		return CategoryTool, true
	case "event":
		return CategoryEvent, true
	case "metrics":
		return CategoryMetrics, true
	default:
		return 0, false
	}
}

func levelString(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "debug"
	case level < slog.LevelWarn:
		return "info"
	case level < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}

func slogEventName(level slog.Level, message string) string {
	switch {
	case level < slog.LevelInfo:
		// Fine-grained debug events can be toggled individually via /trace-events log:xxx.
		return "log:" + slugEventName(message)
	case level < slog.LevelWarn:
		return "log:info"
	case level < slog.LevelError:
		return "log:warn"
	default:
		return "log:error"
	}
}

func slugEventName(message string) string {
	s := strings.TrimSpace(strings.ToLower(message))
	if s == "" {
		return "debug"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "debug"
	}
	if len(out) > 96 {
		return out[:96]
	}
	return out
}
