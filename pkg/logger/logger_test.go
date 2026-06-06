package logger

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewLogger(t *testing.T) {
	l, err := NewLogger(&Config{})
	if err != nil {
		t.Fatalf("NewLogger returned error: %v", err)
	}
	if l == nil {
		t.Fatal("NewLogger returned nil logger")
	}

	// Verify the logger can be used to write records without panicking.
	// Records are forwarded to the trace event bridge; we just need to
	// exercise the handler path so the statements register as covered.
	l.Info("test message", "key", "value")
	l.Warn("warn message")
	l.Error("error message")
	l.Debug("debug message")
	l.WithGroup("sub").LogAttrs(context.Background(), slog.LevelInfo, "attr msg", slog.String("k", "v"))
}

func TestNewDefaultLogger(t *testing.T) {
	l := NewDefaultLogger()
	if l == nil {
		t.Fatal("NewDefaultLogger returned nil")
	}
	l.Info("default logger works")
}
