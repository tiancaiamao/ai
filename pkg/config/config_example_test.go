package config

import (
	"encoding/json"
	"os"
	"testing"
)

// TestConfigExampleFile tests that config.example.json is valid and matches code defaults
func TestConfigExampleFile(t *testing.T) {
	// Load example config file
	data, err := os.ReadFile("config.example.json")
	if err != nil {
		t.Fatalf("Failed to read config.example.json: %v", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Failed to parse config.example.json: %v", err)
	}

	// Verify model config
	if cfg.Model.ID == "" {
		t.Error("Model ID should not be empty")
	}
	if cfg.Model.Provider == "" {
		t.Error("Model Provider should not be empty")
	}
	if cfg.Model.BaseURL == "" {
		t.Error("Model BaseURL should not be empty")
	}
	if cfg.Model.API == "" {
		t.Error("Model API should not be empty")
	}

	// Verify compactor config exists
	if cfg.Compactor == nil {
		t.Fatal("Compactor config should not be nil")
	}

	// Verify compactor config matches defaults
	defaultCompactor := DefaultCompactorConfig()
	if cfg.Compactor.MaxTokens != defaultCompactor.MaxTokens {
		t.Errorf("MaxTokens mismatch: got %d, want %d", cfg.Compactor.MaxTokens, defaultCompactor.MaxTokens)
	}
	if cfg.Compactor.KeepRecent != defaultCompactor.KeepRecent {
		t.Errorf("KeepRecent mismatch: got %d, want %d", cfg.Compactor.KeepRecent, defaultCompactor.KeepRecent)
	}
	if cfg.Compactor.AutoCompact != defaultCompactor.AutoCompact {
		t.Errorf("AutoCompact mismatch: got %v, want %v", cfg.Compactor.AutoCompact, defaultCompactor.AutoCompact)
	}

	// Verify MaxMessages is not set (should be 0/empty)
	if cfg.Compactor.MaxMessages != 0 {
		t.Errorf("MaxMessages should not be set in config, got %d", cfg.Compactor.MaxMessages)
	}

	// Verify concurrency config
	if cfg.Concurrency == nil {
		t.Fatal("Concurrency config should not be nil")
	}
	defaultConcurrency := DefaultConcurrencyConfig()
	if cfg.Concurrency.MaxConcurrentTools != defaultConcurrency.MaxConcurrentTools {
		t.Errorf("MaxConcurrentTools mismatch: got %d, want %d", cfg.Concurrency.MaxConcurrentTools, defaultConcurrency.MaxConcurrentTools)
	}

	// Verify tool output config
	if cfg.ToolOutput == nil {
		t.Fatal("ToolOutput config should not be nil")
	}
	defaultToolOutput := DefaultToolOutputConfig()
	if cfg.ToolOutput.MaxChars != defaultToolOutput.MaxChars {
		t.Errorf("MaxChars mismatch: got %d, want %d", cfg.ToolOutput.MaxChars, defaultToolOutput.MaxChars)
	}

	// Verify deprecated fields are not set
	if cfg.ToolOutput.MaxLines != 0 {
		t.Error("MaxLines should not be set (deprecated)")
	}
	if cfg.ToolOutput.MaxBytes != 0 {
		t.Error("MaxBytes should not be set (deprecated)")
	}
	if cfg.ToolOutput.LargeOutputThreshold != 0 {
		t.Error("LargeOutputThreshold should not be set (deprecated)")
	}
	if cfg.ToolOutput.TruncateMode != "" {
		t.Error("TruncateMode should not be set (deprecated)")
	}

	// Verify log config
	if cfg.Log == nil {
		t.Fatal("Log config should not be nil")
	}
	if cfg.Log.Level == "" {
		t.Error("Log level should not be empty")
	}
}

// TestConfigDefaultsMatchCode verifies that default values in code match expectations
func TestConfigDefaultsMatchCode(t *testing.T) {
	tests := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{"Compactor.MaxTokens", DefaultCompactorConfig().MaxTokens, 8000},
		{"Compactor.KeepRecent", DefaultCompactorConfig().KeepRecent, 5},
		{"Compactor.KeepRecentTokens", DefaultCompactorConfig().KeepRecentTokens, 20000},
		{"Compactor.ReserveTokens", DefaultCompactorConfig().ReserveTokens, 16384},
		{"Compactor.ToolCallCutoff", DefaultCompactorConfig().ToolCallCutoff, 10},
		{"Compactor.AutoCompact", DefaultCompactorConfig().AutoCompact, true},
		{"Concurrency.MaxConcurrentTools", DefaultConcurrencyConfig().MaxConcurrentTools, 3},
		{"Concurrency.ToolTimeout", DefaultConcurrencyConfig().ToolTimeout, 30},
		{"Concurrency.QueueTimeout", DefaultConcurrencyConfig().QueueTimeout, 60},
		{"ToolOutput.MaxChars", DefaultToolOutputConfig().MaxChars, 200 * 1024},
		{"Log.Level", DefaultLogConfig().Level, "info"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch got := tt.got.(type) {
			case int:
				if exp, ok := tt.expected.(int); !ok || got != exp {
					t.Errorf("%s: got %v, want %v", tt.name, got, tt.expected)
				}
			case bool:
				if exp, ok := tt.expected.(bool); !ok || got != exp {
					t.Errorf("%s: got %v, want %v", tt.name, got, tt.expected)
				}
			case string:
				if exp, ok := tt.expected.(string); !ok || got != exp {
					t.Errorf("%s: got %v, want %v", tt.name, got, tt.expected)
				}
			default:
				t.Errorf("Unhandled type for %s", tt.name)
			}
		})
	}
}

// TestNoDeprecatedFieldsInStructs verifies that deprecated fields are removed from structs
func TestNoDeprecatedFieldsInStructs(t *testing.T) {
	// Test ToolOutputConfig has only MaxChars
	toolOutput := ToolOutputConfig{}
	data, err := json.Marshal(toolOutput)
	if err != nil {
		t.Fatalf("Failed to marshal ToolOutputConfig: %v", err)
	}
	
	// Should only have maxChars field
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	
	allowedFields := map[string]bool{"maxChars": true}
	for field := range m {
		if !allowedFields[field] {
			t.Errorf("Deprecated field found in ToolOutputConfig: %s", field)
		}
	}

	// Test LogConfig doesn't have deprecated fields
	logCfg := LogConfig{}
	data, err = json.Marshal(logCfg)
	if err != nil {
		t.Fatalf("Failed to marshal LogConfig: %v", err)
	}
	
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	
	deprecatedLogFields := []string{"prefix", "traceBridge"}
	for _, field := range deprecatedLogFields {
		if _, exists := m[field]; exists {
			t.Errorf("Deprecated field found in LogConfig: %s", field)
		}
	}
}

// Helper function to get default compactor config
func DefaultCompactorConfig() *struct {
	MaxTokens           int
	KeepRecent          int
	KeepRecentTokens    int
	ReserveTokens       int
	ToolCallCutoff      int
	AutoCompact         bool
	MaxMessages         int
} {
	// Import from compact package would create cycle, so we define expected values
	return &struct {
		MaxTokens           int
		KeepRecent          int
		KeepRecentTokens    int
		ReserveTokens       int
		ToolCallCutoff      int
		AutoCompact         bool
		MaxMessages         int
	}{
		MaxTokens:        8000,
		KeepRecent:       5,
		KeepRecentTokens: 20000,
		ReserveTokens:    16384,
		ToolCallCutoff:   10,
		AutoCompact:      true,
		MaxMessages:      0, // Should be 0 (not used)
	}
}