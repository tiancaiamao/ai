package logger

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	t.Run("DefaultLogger", func(t *testing.T) {
		l := NewDefaultLogger()
		if l == nil {
			t.Fatal("NewDefaultLogger returned nil")
		}

		if l.level != INFO {
			t.Errorf("Expected level INFO, got %v", l.level)
		}

		if !l.consoleEnable {
			t.Error("Console should be enabled by default")
		}

		l.Close()
	})

	t.Run("CustomLogger", func(t *testing.T) {
		cfg := &Config{
			Level:   DEBUG,
			Prefix:  "[test] ",
			Console: false,
			File:    false,
		}

		l, err := NewLogger(cfg)
		if err != nil {
			t.Fatalf("Failed to create logger: %v", err)
		}

		if l.level != DEBUG {
			t.Errorf("Expected level DEBUG, got %v", l.level)
		}

		if l.consoleEnable {
			t.Error("Console should be disabled")
		}

		l.Close()
	})
}

func TestLogLevel(t *testing.T) {
	t.Run("String", func(t *testing.T) {
		tests := []struct {
			level    LogLevel
			expected string
		}{
			{DEBUG, "DEBUG"},
			{INFO, "INFO"},
			{WARN, "WARN"},
			{ERROR, "ERROR"},
		}

		for _, tt := range tests {
			if got := tt.level.String(); got != tt.expected {
				t.Errorf("Level %d: expected %s, got %s", tt.level, tt.expected, got)
			}
		}
	})

	t.Run("ParseLogLevel", func(t *testing.T) {
		tests := []struct {
			input    string
			expected LogLevel
		}{
			{"debug", DEBUG},
			{"DEBUG", DEBUG},
			{"info", INFO},
			{"INFO", INFO},
			{"warn", WARN},
			{"WARN", WARN},
			{"error", ERROR},
			{"ERROR", ERROR},
			{"invalid", INFO}, // Default to INFO
		}

		for _, tt := range tests {
			if got := ParseLogLevel(tt.input); got != tt.expected {
				t.Errorf("ParseLogLevel(%q): expected %v, got %v", tt.input, tt.expected, got)
			}
		}
	})
}

func TestLogLevelFiltering(t *testing.T) {
	var buf bytes.Buffer

	cfg := &Config{
		Level:   WARN,
		Prefix:  "",
		Console: false,
		File:    false,
	}

	l, _ := NewLogger(cfg)
	l.consoleWriter = &buf
	l.consoleEnable = true

	l.Debug("debug message")
	l.Info("info message")
	l.Warn("warn message")
	l.Error("error message")

	output := buf.String()

	// Debug and Info should be filtered out
	if strings.Contains(output, "debug message") {
		t.Error("DEBUG message should be filtered")
	}

	if strings.Contains(output, "info message") {
		t.Error("INFO message should be filtered")
	}

	// Warn and Error should appear
	if !strings.Contains(output, "warn message") {
		t.Error("WARN message should appear")
	}

	if !strings.Contains(output, "error message") {
		t.Error("ERROR message should appear")
	}

	l.Close()
}

func TestLogOutput(t *testing.T) {
	var buf bytes.Buffer

	cfg := &Config{
		Level:   INFO,
		Prefix:  "[test] ",
		Console: false,
		File:    false,
	}

	l, _ := NewLogger(cfg)
	l.consoleWriter = &buf
	l.consoleEnable = true

	l.Info("test message")

	output := buf.String()

	// Check prefix
	if !strings.Contains(output, "[test]") {
		t.Error("Output should contain prefix")
	}

	// Check level
	if !strings.Contains(output, "[INFO]") {
		t.Error("Output should contain level")
	}

	// Check message
	if !strings.Contains(output, "test message") {
		t.Error("Output should contain message")
	}

	// Check timestamp format (YYYY-MM-DD HH:MM:SS)
	if !matchesTimestamp(output) {
		t.Error("Output should contain timestamp in format YYYY-MM-DD HH:MM:SS")
	}

	l.Close()
}

func TestSetLevel(t *testing.T) {
	l := NewDefaultLogger()

	// Test setting level
	l.SetLevel(ERROR)
	if l.level != ERROR {
		t.Errorf("Expected level ERROR, got %v", l.level)
	}

	// Test getting level
	if l.GetLevel() != ERROR {
		t.Error("GetLevel should return ERROR")
	}

	l.Close()
}

func TestConsoleEnable(t *testing.T) {
	l := NewDefaultLogger()

	// Disable console
	l.SetConsoleEnabled(false)
	if l.consoleEnable {
		t.Error("Console should be disabled")
	}

	// Enable console
	l.SetConsoleEnabled(true)
	if !l.consoleEnable {
		t.Error("Console should be enabled")
	}

	l.Close()
}

func TestFileLogging(t *testing.T) {
	tempFile := os.TempDir() + "/ai-test-log-" + strings.ReplaceAll(t.Name(), "/", "_") + ".log"
	defer os.Remove(tempFile)

	cfg := &Config{
		Level:    INFO,
		Prefix:   "[test] ",
		Console:  false,
		File:     true,
		FilePath: tempFile,
	}

	l, err := NewLogger(cfg)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	l.Info("test message to file")

	// Close to flush
	l.Close()

	// Read file
	data, err := os.ReadFile(tempFile)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "test message to file") {
		t.Error("Log file should contain the message")
	}
}

func TestWithPrefix(t *testing.T) {
	l := NewDefaultLogger()

	l2 := l.WithPrefix("[custom] ")
	if l2.prefix != "[custom] " {
		t.Errorf("Expected prefix '[custom] ', got '%s'", l2.prefix)
	}

	// Original logger should be unchanged
	if l.prefix != "[ai] " {
		t.Errorf("Original prefix should be unchanged")
	}

	l.Close()
}

func TestLogAliases(t *testing.T) {
	var buf bytes.Buffer

	cfg := &Config{
		Level:   DEBUG,
		Prefix:  "",
		Console: false,
		File:    false,
	}

	l, _ := NewLogger(cfg)
	l.consoleWriter = &buf
	l.consoleEnable = true

	// Test aliases
	l.Debugf("debug: %s", "test")
	l.Infof("info: %s", "test")
	l.Warnf("warn: %s", "test")
	l.Errorf("error: %s", "test")

	output := buf.String()

	if !strings.Contains(output, "debug: test") {
		t.Error("Debugf should work")
	}

	if !strings.Contains(output, "info: test") {
		t.Error("Infof should work")
	}

	if !strings.Contains(output, "warn: test") {
		t.Error("Warnf should work")
	}

	if !strings.Contains(output, "error: test") {
		t.Error("Errorf should work")
	}

	l.Close()
}

// Helper function to check if output matches timestamp format
func matchesTimestamp(output string) bool {
	// Timestamp format: [prefix]YYYY-MM-DD HH:MM:SS [LEVEL] message
	// Example: [test] 2024-01-15 14:30:45 [INFO] test message

	// Extract the part after prefix and before level
	prefixEnd := strings.Index(output, "] ")
	if prefixEnd == -1 {
		return false
	}

	afterPrefix := output[prefixEnd+2:]

	// Find the level part
	levelStart := strings.Index(afterPrefix, " [")
	if levelStart == -1 {
		return false
	}

	timestampPart := afterPrefix[:levelStart]

	// Check date format (YYYY-MM-DD HH:MM:SS)
	parts := strings.Split(timestampPart, " ")
	if len(parts) != 2 {
		return false
	}

	// Check date (YYYY-MM-DD)
	dateParts := strings.Split(parts[0], "-")
	if len(dateParts) != 3 {
		return false
	}

	// Check time (HH:MM:SS)
	timeParts := strings.Split(parts[1], ":")
	if len(timeParts) != 3 {
		return false
	}

	return true
}
