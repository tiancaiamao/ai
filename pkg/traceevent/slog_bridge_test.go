package traceevent

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

type slogCaptureHandler struct {
	messages *[]string
}

func (h *slogCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	*h.messages = append(*h.messages, r.Message)
	return nil
}

func (h *slogCaptureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *slogCaptureHandler) WithGroup(_ string) slog.Handler      { return h }
func (h *slogCaptureHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func TestSlogBridgeMapsToLogEvents(t *testing.T) {
	ResetToDefaultEvents()
	EnableEvent("log:test_debug_message")

	buf := NewTraceBuf()
	buf.SetMaxEvents(100)
	SetActiveTraceBuf(buf)
	defer ClearActiveTraceBuf(buf)

	logger := slog.New(WrapSlogHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger.Debug("test debug message", "k", 1)
	logger.Info("test info message")
	logger.Warn("test warn message")
	logger.Error("test error message")

	events := buf.Snapshot()
	if findEvent(events, "log:test_debug_message") == nil {
		t.Fatal("missing debug log event")
	}
	if findEvent(events, "log:info") == nil {
		t.Fatal("missing info log event")
	}
	if findEvent(events, "log:warn") == nil {
		t.Fatal("missing warn log event")
	}
	if findEvent(events, "log:error") == nil {
		t.Fatal("missing error log event")
	}
}

func TestSlogBridgeDebugDisabledByDefault(t *testing.T) {
	ResetToDefaultEvents()

	buf := NewTraceBuf()
	buf.SetMaxEvents(100)
	SetActiveTraceBuf(buf)
	defer ClearActiveTraceBuf(buf)

	logger := slog.New(WrapSlogHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	logger.Debug("debug should be disabled by default")

	if got := len(buf.Snapshot()); got != 0 {
		t.Fatalf("expected 0 events, got %d", got)
	}
}

func TestSlogBridgeDisabledEvents(t *testing.T) {
	ResetToDefaultEvents()
	DisableEvent("log:info")
	DisableEvent("log:error")

	buf := NewTraceBuf()
	buf.SetMaxEvents(100)
	SetActiveTraceBuf(buf)
	defer ClearActiveTraceBuf(buf)

	logger := slog.New(WrapSlogHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})))
	logger.Info("should not be recorded")
	logger.Error("should not be recorded")

	if got := len(buf.Snapshot()); got != 0 {
		t.Fatalf("expected 0 events, got %d", got)
	}
}

func TestSlogBridgeBaseHandlerNotCalled(t *testing.T) {
	ResetToDefaultEvents()

	var messages []string
	handler := &slogCaptureHandler{messages: &messages}

	buf := NewTraceBuf()
	buf.SetMaxEvents(100)
	SetActiveTraceBuf(buf)
	defer ClearActiveTraceBuf(buf)

	logger := slog.New(WrapSlogHandler(handler))
	logger.Info("message 1")
	logger.Warn("message 2")

	if len(messages) != 0 {
		t.Fatalf("expected base handler to be bypassed, got %d messages", len(messages))
	}
	if got := len(buf.Snapshot()); got != 2 {
		t.Fatalf("expected 2 trace events, got %d", got)
	}
}

func TestSlogBridgeDefaultEnabledLogs(t *testing.T) {
	ResetToDefaultEvents()
	if !IsEventEnabled("log:info") {
		t.Fatal("log:info should be enabled by default")
	}
	if !IsEventEnabled("log:warn") {
		t.Fatal("log:warn should be enabled by default")
	}
	if !IsEventEnabled("log:error") {
		t.Fatal("log:error should be enabled by default")
	}
	if IsEventEnabled("log:example_debug_event") {
		t.Fatal("dynamic debug events should not be enabled by default")
	}
}

func findEvent(events []TraceEvent, name string) *TraceEvent {
	for i := range events {
		if events[i].Name == name {
			return &events[i]
		}
	}
	return nil
}
