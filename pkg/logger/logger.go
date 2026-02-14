package logger

import (
	"io"
	"log/slog"

	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// Config contains logger configuration.
type Config struct{}

// NewLogger creates a new slog.Logger with the given configuration.
// All logs are converted to trace events only (no log file output).
func NewLogger(cfg *Config) (*slog.Logger, error) {
	// Create a discard handler. Event filtering is handled by traceevent.
	textHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{})

	// Always wrap with trace event bridge (unified event stream)
	handler := traceevent.WrapSlogHandler(textHandler)

	// Create and return logger
	return slog.New(handler), nil
}

// NewDefaultLogger creates a logger with default settings.
func NewDefaultLogger() *slog.Logger {
	l, _ := NewLogger(&Config{})
	return l
}
