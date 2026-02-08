package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
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

// Logger is a thread-safe logger with level filtering and multiple outputs.
type Logger struct {
	mu            sync.Mutex
	level         LogLevel
	prefix        string
	consoleWriter io.Writer
	fileWriter    io.Writer
	consoleEnable bool
	fileEnable    bool
}

// Config contains logger configuration.
type Config struct {
	Level      LogLevel // Minimum log level to output
	Prefix     string   // Prefix for all log messages
	Console    bool     // Enable console output
	File       bool     // Enable file output
	FilePath   string   // Path to log file
	MaxSize    int64    // Maximum log file size in bytes (0 = unlimited)
	MaxBackups int      // Maximum number of backup files (0 = no rotation)
}

// NewLogger creates a new logger with the given configuration.
func NewLogger(cfg *Config) (*Logger, error) {
	l := &Logger{
		level:         cfg.Level,
		prefix:        cfg.Prefix,
		consoleWriter: os.Stderr,
		consoleEnable: cfg.Console,
		fileEnable:    cfg.File,
	}

	// Setup file output if enabled
	if cfg.File && cfg.FilePath != "" {
		// Ensure directory exists
		dir := filepath.Dir(cfg.FilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		// Open log file in append mode
		file, err := os.OpenFile(cfg.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}

		l.fileWriter = file
	}

	return l, nil
}

// NewDefaultLogger creates a logger with default settings.
func NewDefaultLogger() *Logger {
	l, _ := NewLogger(&Config{
		Level:   INFO,
		Prefix:  "[ai] ",
		Console: true,
		File:    false,
	})
	return l
}

// SetLevel sets the minimum log level.
func (l *Logger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current minimum log level.
func (l *Logger) GetLevel() LogLevel {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.level
}

// SetConsoleEnabled enables or disables console output.
func (l *Logger) SetConsoleEnabled(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.consoleEnable = enabled
}

// SetFileEnabled enables or disables file output.
func (l *Logger) SetFileEnabled(enabled bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fileEnable = enabled
}

// Close closes any open file handles.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.fileWriter != nil {
		if closer, ok := l.fileWriter.(io.Closer); ok {
			return closer.Close()
		}
	}

	return nil
}

// log is the internal logging method.
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Format message
	msg := fmt.Sprintf(format, args...)

	// Add timestamp and level
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	logLine := fmt.Sprintf("%s%s%s %s\n", l.prefix, timestamp, " ["+level.String()+"]", msg)

	// Write to console
	if l.consoleEnable && l.consoleWriter != nil {
		l.consoleWriter.Write([]byte(logLine))
	}

	// Write to file
	if l.fileEnable && l.fileWriter != nil {
		l.fileWriter.Write([]byte(logLine))
	}
}

// Debug logs a debug message.
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(DEBUG, format, args...)
}

// Info logs an info message.
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(INFO, format, args...)
}

// Warn logs a warning message.
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(WARN, format, args...)
}

// Error logs an error message.
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(ERROR, format, args...)
}

// Debugf logs a debug message (alias for Debug).
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.Debug(format, args...)
}

// Infof logs an info message (alias for Info).
func (l *Logger) Infof(format string, args ...interface{}) {
	l.Info(format, args...)
}

// Warnf logs a warning message (alias for Warn).
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.Warn(format, args...)
}

// Errorf logs an error message (alias for Error).
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.Error(format, args...)
}

// Fatal logs an error message and exits the application.
func (l *Logger) Fatal(format string, args ...interface{}) {
	l.Error(format, args...)
	os.Exit(1)
}

// Fatalf logs an error message and exits the application (alias for Fatal).
func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.Fatal(format, args...)
}

// WithPrefix returns a new logger with the given prefix.
func (l *Logger) WithPrefix(prefix string) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	return &Logger{
		level:         l.level,
		prefix:        prefix,
		consoleWriter: l.consoleWriter,
		fileWriter:    l.fileWriter,
		consoleEnable: l.consoleEnable,
		fileEnable:    l.fileEnable,
	}
}
