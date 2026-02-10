package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiancaiamao/ai/pkg/compact"
)

// TestLoadConfigDefaults tests loading config with default values.
func TestLoadConfigDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify defaults
	if cfg.Model.ID != "glm-4.5-air" {
		t.Errorf("Expected default model ID 'glm-4.5-air', got '%s'", cfg.Model.ID)
	}

	if cfg.Model.Provider != "zai" {
		t.Errorf("Expected default provider 'zai', got '%s'", cfg.Model.Provider)
	}

	if cfg.Compactor == nil {
		t.Error("Expected compactor config to be initialized")
	} else {
		if cfg.Compactor.MaxMessages != 50 {
			t.Errorf("Expected default MaxMessages 50, got %d", cfg.Compactor.MaxMessages)
		}
		if cfg.Compactor.MaxTokens != 8000 {
			t.Errorf("Expected default MaxTokens 8000, got %d", cfg.Compactor.MaxTokens)
		}
		if cfg.Compactor.KeepRecentTokens != 20000 {
			t.Errorf("Expected default KeepRecentTokens 20000, got %d", cfg.Compactor.KeepRecentTokens)
		}
	}

	if cfg.ToolOutput == nil {
		t.Error("Expected tool output config to be initialized")
	} else {
		if cfg.ToolOutput.MaxLines != 2000 {
			t.Errorf("Expected default tool output MaxLines 2000, got %d", cfg.ToolOutput.MaxLines)
		}
		if cfg.ToolOutput.MaxBytes != 50*1024 {
			t.Errorf("Expected default tool output MaxBytes 51200, got %d", cfg.ToolOutput.MaxBytes)
		}
	}
}

// TestLoadConfigFromFile tests loading config from file.
func TestLoadConfigFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Create config file
	testConfig := `{
		"model": {
			"id": "custom-model",
			"provider": "custom-provider",
			"baseUrl": "https://custom.api.com",
			"api": "custom-api"
		},
		"compactor": {
			"maxMessages": 100,
			"maxTokens": 5000,
			"keepRecent": 10,
			"keepRecentTokens": 12000,
			"autoCompact": false
		},
		"toolOutput": {
			"maxLines": 500,
			"maxBytes": 8192
		},
		"log": {
			"level": "debug"
		}
	}`

	err := os.WriteFile(configPath, []byte(testConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify loaded values
	if cfg.Model.ID != "custom-model" {
		t.Errorf("Expected model ID 'custom-model', got '%s'", cfg.Model.ID)
	}

	if cfg.Model.Provider != "custom-provider" {
		t.Errorf("Expected provider 'custom-provider', got '%s'", cfg.Model.Provider)
	}

	if cfg.Model.BaseURL != "https://custom.api.com" {
		t.Errorf("Expected BaseURL 'https://custom.api.com', got '%s'", cfg.Model.BaseURL)
	}

	if cfg.Compactor.MaxMessages != 100 {
		t.Errorf("Expected MaxMessages 100, got %d", cfg.Compactor.MaxMessages)
	}

	if cfg.Compactor.MaxTokens != 5000 {
		t.Errorf("Expected MaxTokens 5000, got %d", cfg.Compactor.MaxTokens)
	}
	if cfg.Compactor.KeepRecentTokens != 12000 {
		t.Errorf("Expected KeepRecentTokens 12000, got %d", cfg.Compactor.KeepRecentTokens)
	}

	if cfg.Compactor.AutoCompact != false {
		t.Error("Expected AutoCompact to be false")
	}

	if cfg.ToolOutput == nil {
		t.Error("Expected tool output config to be initialized")
	} else {
		if cfg.ToolOutput.MaxLines != 500 {
			t.Errorf("Expected tool output MaxLines 500, got %d", cfg.ToolOutput.MaxLines)
		}
		if cfg.ToolOutput.MaxBytes != 8192 {
			t.Errorf("Expected tool output MaxBytes 8192, got %d", cfg.ToolOutput.MaxBytes)
		}
	}

	if cfg.Log == nil || cfg.Log.Level != "debug" {
		t.Errorf("Expected log level 'debug', got '%v'", cfg.Log)
	}
}

// TestLoadConfigEnvOverride tests that env vars override config file.
func TestLoadConfigEnvOverride(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Create config file
	testConfig := `{
		"model": {
			"id": "file-model",
			"provider": "file-provider",
			"baseUrl": "https://file.api.com",
			"api": "file-api"
		}
	}`

	err := os.WriteFile(configPath, []byte(testConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Set environment variables
	os.Setenv("ZAI_MODEL", "env-model")
	os.Setenv("ZAI_BASE_URL", "https://env.api.com")
	defer os.Unsetenv("ZAI_MODEL")
	defer os.Unsetenv("ZAI_BASE_URL")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify env vars override file
	if cfg.Model.ID != "env-model" {
		t.Errorf("Expected model ID from env 'env-model', got '%s'", cfg.Model.ID)
	}

	if cfg.Model.BaseURL != "https://env.api.com" {
		t.Errorf("Expected BaseURL from env 'https://env.api.com', got '%s'", cfg.Model.BaseURL)
	}

	// Provider should still be from file
	if cfg.Model.Provider != "file-provider" {
		t.Errorf("Expected provider from file 'file-provider', got '%s'", cfg.Model.Provider)
	}
}

// TestSaveConfig tests saving config to file.
func TestSaveConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_save.json")

	cfg := &Config{
		Model: ModelConfig{
			ID:       "test-model",
			Provider: "test-provider",
			BaseURL:  "https://test.api.com",
			API:      "test-api",
		},
		Compactor: &compact.Config{
			MaxMessages: 75,
			MaxTokens:   6000,
			KeepRecent:  7,
			AutoCompact: true,
		},
		Log: &LogConfig{
			Level: "info",
		},
	}

	err := SaveConfig(cfg, configPath)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("Config file was not created")
	}

	// Load and verify
	loadedCfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load saved config: %v", err)
	}

	if loadedCfg.Model.ID != "test-model" {
		t.Errorf("Expected model ID 'test-model', got '%s'", loadedCfg.Model.ID)
	}

	if loadedCfg.Compactor.MaxMessages != 75 {
		t.Errorf("Expected MaxMessages 75, got %d", loadedCfg.Compactor.MaxMessages)
	}
}

// TestGetLLMModel tests converting config to llm.Model.
func TestGetLLMModel(t *testing.T) {
	cfg := &Config{
		Model: ModelConfig{
			ID:       "test-model",
			Provider: "test-provider",
			BaseURL:  "https://test.api.com",
			API:      "openai-completions",
		},
	}

	model := cfg.GetLLMModel()

	if model.ID != "test-model" {
		t.Errorf("Expected model ID 'test-model', got '%s'", model.ID)
	}

	if model.Provider != "test-provider" {
		t.Errorf("Expected provider 'test-provider', got '%s'", model.Provider)
	}

	if model.BaseURL != "https://test.api.com" {
		t.Errorf("Expected BaseURL 'https://test.api.com', got '%s'", model.BaseURL)
	}

	if model.API != "openai-completions" {
		t.Errorf("Expected API 'openai-completions', got '%s'", model.API)
	}
}

// TestGetDefaultConfigPath tests getting default config path.
func TestGetDefaultConfigPath(t *testing.T) {
	path, err := GetDefaultConfigPath()
	if err != nil {
		t.Fatalf("Failed to get default config path: %v", err)
	}

	if path == "" {
		t.Error("Path should not be empty")
	}

	// Should contain .ai
	if filepath.Base(filepath.Dir(path)) != ".ai" {
		t.Errorf("Expected path to contain .ai, got %s", path)
	}

	// Should end with config.json
	if filepath.Base(path) != "config.json" {
		t.Errorf("Expected filename to be config.json, got %s", filepath.Base(path))
	}
}

// TestLoadInvalidJSON tests loading invalid JSON config.
func TestLoadInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.json")

	// Write invalid JSON
	err := os.WriteFile(configPath, []byte("{invalid json}"), 0644)
	if err != nil {
		t.Fatalf("Failed to write invalid config: %v", err)
	}

	_, err = LoadConfig(configPath)
	if err == nil {
		t.Error("Expected error when loading invalid JSON")
	}
}

// TestPartialConfig tests loading partial config (missing fields).
func TestPartialConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "partial.json")

	// Only set model ID
	partialConfig := `{
		"model": {
			"id": "partial-model"
		}
	}`

	err := os.WriteFile(configPath, []byte(partialConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to write partial config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load partial config: %v", err)
	}

	// Verify ID is set
	if cfg.Model.ID != "partial-model" {
		t.Errorf("Expected model ID 'partial-model', got '%s'", cfg.Model.ID)
	}

	// Verify defaults are used for missing fields
	if cfg.Model.Provider != "zai" {
		t.Errorf("Expected default provider 'zai', got '%s'", cfg.Model.Provider)
	}
}

// TestEmptyConfigFile tests loading an empty config file.
func TestEmptyConfigFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "empty.json")

	err := os.WriteFile(configPath, []byte("{}"), 0644)
	if err != nil {
		t.Fatalf("Failed to write empty config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load empty config: %v", err)
	}

	// Should use all defaults
	if cfg.Model.ID != "glm-4.5-air" {
		t.Errorf("Expected default model ID, got '%s'", cfg.Model.ID)
	}

	if cfg.Compactor == nil {
		t.Error("Expected compactor config to be initialized")
	}
}

// TestCompactorDefaults tests that compactor defaults are properly set.
func TestCompactorDefaults(t *testing.T) {
	cfg := &Config{
		Compactor: compact.DefaultConfig(),
	}

	compactorConfig := cfg.Compactor
	if compactorConfig.MaxMessages != 50 {
		t.Errorf("Expected MaxMessages 50, got %d", compactorConfig.MaxMessages)
	}

	if compactorConfig.MaxTokens != 8000 {
		t.Errorf("Expected MaxTokens 8000, got %d", compactorConfig.MaxTokens)
	}

	if compactorConfig.KeepRecent != 5 {
		t.Errorf("Expected KeepRecent 5, got %d", compactorConfig.KeepRecent)
	}
	if compactorConfig.KeepRecentTokens != 20000 {
		t.Errorf("Expected KeepRecentTokens 20000, got %d", compactorConfig.KeepRecentTokens)
	}

	if compactorConfig.ReserveTokens != 16384 {
		t.Errorf("Expected ReserveTokens 16384, got %d", compactorConfig.ReserveTokens)
	}

	if !compactorConfig.AutoCompact {
		t.Error("Expected AutoCompact to be true")
	}
}
