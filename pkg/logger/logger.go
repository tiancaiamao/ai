package logger

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"gopkg.in/natefinch/lumberjack.v2"
	"log/slog"
)

// LogLevel represents the severity level of a log message.
type LogLevel int

const (
	// DEBUG level for detailed debugging information
	DEBUG LogLevel = iota
	// INFO level for general informational messages
	INFO
	// WARN level for warning messages
	WARN
	// ERROR level for error messages
	ERROR
)

// String returns the string representation of the log level.
func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// toSlogLevel converts LogLevel to slog.Level.
func (l LogLevel) toSlogLevel() slog.Level {
	switch l {
	case DEBUG:
		return slog.LevelDebug
	case INFO:
		return slog.LevelInfo
	case WARN:
		return slog.LevelWarn
	case ERROR:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ParseLogLevel parses a string to LogLevel.
func ParseLogLevel(level string) LogLevel {
	switch level {
	case "DEBUG", "debug":
		return DEBUG
	case "INFO", "info":
		return INFO
	case "WARN", "warn":
		return WARN
	case "ERROR", "error":
		return ERROR
	default:
		return INFO
	}
}

// Config contains logger configuration.
type Config struct {
	Level      LogLevel // Minimum log level to output
	Prefix     string   // Prefix for all log messages
	Console    bool     // Enable console output
	File       bool     // Enable file output
	FilePath   string   // Path to log file
	MaxSize    int64    // Maximum log file size in MB (0 = unlimited)
	MaxBackups int      // Maximum number of backup files (0 = no rotation)
	MaxAge     int      // Maximum number of days to retain old log files
	Compress   bool     // Compress rotated files
}

// NewLogger creates a new slog.Logger with the given configuration.
// The returned logger uses a custom text handler with the format:
// [LEVEL] 2006-01-02T15:04:05.999 file.go:42 "message" key=value
func NewLogger(cfg *Config) (*slog.Logger, error) {
	// Create writers
	var writers []io.Writer

	if cfg.Console {
		writers = append(writers, os.Stderr)
	}

	if cfg.File && cfg.FilePath != "" {
		// Ensure directory exists
		dir := filepath.Dir(cfg.FilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		// Use lumberjack for log rotation
		lj := &lumberjack.Logger{
			Filename:   cfg.FilePath,
			MaxSize:    int(cfg.MaxSize),
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAge,
			Compress:   cfg.Compress,
		}
		writers = append(writers, lj)
	}

	// Create multi-writer if needed
	var writer io.Writer
	if len(writers) == 0 {
		writer = io.Discard
	} else if len(writers) == 1 {
		writer = writers[0]
	} else {
		writer = io.MultiWriter(writers...)
	}

	// Create custom handler with source code location (file:line)
	opts := &slog.HandlerOptions{
		Level: cfg.Level.toSlogLevel(),
	}
	handler := NewTextHandler(writer, opts, cfg.Prefix)

	// Create and return logger
	return slog.New(handler), nil
}

// NewDefaultLogger creates a logger with default settings.
func NewDefaultLogger() *slog.Logger {
	l, _ := NewLogger(&Config{
		Level:   INFO,
		Prefix:  "[ai] ",
		Console: true,
		File:    false,
	})
	return l
}

// textHandler is a custom slog.Handler that formats log messages.
type textHandler struct {
	opts   *slog.HandlerOptions
	mu     sync.Mutex
	out    io.Writer
	prefix string
}

// NewTextHandler creates a new custom text handler.
func NewTextHandler(out io.Writer, opts *slog.HandlerOptions, prefix string) slog.Handler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &textHandler{out: out, opts: opts, prefix: prefix}
}

// Enabled reports whether the handler handles records at the given level.
func (h *textHandler) Enabled(ctx context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

// Handle handles a log record.
func (h *textHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Format: [LEVEL] 2006-01-02T15:04:05.999 file.go:42 "message" key=value
	// Get level string
	levelStr := levelToString(r.Level)

	// Format time without timezone
	timestamp := r.Time.Format("2006-01-02T15:04:05.999")

	// Get file and line - use r.PC
	var file, line string
	if r.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{r.PC})
		frame, _ := frames.Next()
		file = filepath.Base(frame.File)
		line = fmt.Sprintf("%d", frame.Line)
	}

	// Build the log line
	buf := make([]byte, 0, 256)
	buf = append(buf, '[')
	buf = append(buf, levelStr...)
	buf = append(buf, "] "...)
	buf = append(buf, timestamp...)
	buf = append(buf, ' ')
	if file != "" {
		buf = append(buf, file...)
		buf = append(buf, ':')
		buf = append(buf, line...)
		buf = append(buf, ' ')
	}

	// Add message
	buf = append(buf, '"')
	msg := r.Message
	if h.prefix != "" {
		msg = h.prefix + msg
	}
	buf = append(buf, msg...)
	buf = append(buf, '"')

	// Add attributes
	r.Attrs(func(a slog.Attr) bool {
		buf = append(buf, ' ')
		buf = append(buf, a.Key...)
		buf = append(buf, '=')
		buf = append(buf, quoteIfNeeded(a.Value.String())...)
		return true
	})

	buf = append(buf, '\n')

	_, err := h.out.Write(buf)
	return err
}

// WithAttrs returns a handler with the given attributes.
func (h *textHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &textHandler{out: h.out, opts: h.opts, prefix: h.prefix}
}

// WithGroup returns a handler with a group.
func (h *textHandler) WithGroup(name string) slog.Handler {
	return &textHandler{out: h.out, opts: h.opts, prefix: h.prefix}
}

// levelToString converts slog.Level to string.
func levelToString(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO"
	case level < slog.LevelError:
		return "WARN"
	default:
		return "ERROR"
	}
}

// quoteIfNeeded adds quotes around a string if it contains spaces.
func quoteIfNeeded(s string) string {
	if len(s) == 0 {
		return `""`
	}
	for _, c := range s {
		if c <= ' ' || c == '"' || c == '\\' {
			return fmt.Sprintf("%q", s)
		}
	}
	return s
}
