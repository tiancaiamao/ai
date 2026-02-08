package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/logger"
)

// Config represents the application configuration.
type Config struct {
	// Model configuration
	Model ModelConfig `json:"model"`

	// Compactor configuration
	Compactor *compact.Config `json:"compactor,omitempty"`

	// Concurrency configuration
	Concurrency *ConcurrencyConfig `json:"concurrency,omitempty"`

	// Logging configuration
	Log *LogConfig `json:"log,omitempty"`
}

// LogConfig contains logging configuration.
type LogConfig struct {
	Level  string `json:"level,omitempty"`  // Log level: debug, info, warn, error
	File   string `json:"file,omitempty"`   // Log file path (empty = no file logging)
	Prefix string `json:"prefix,omitempty"` // Log prefix
}

// ModelConfig contains model configuration.
type ModelConfig struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	BaseURL  string `json:"baseUrl"`
	API      string `json:"api"`
}

// ConcurrencyConfig contains concurrency control settings.
type ConcurrencyConfig struct {
	MaxConcurrentTools int `json:"maxConcurrentTools"` // Maximum tools running concurrently
	ToolTimeout        int `json:"toolTimeout"`        // Tool execution timeout in seconds
	QueueTimeout       int `json:"queueTimeout"`       // Queue wait timeout in seconds
}

// DefaultConcurrencyConfig returns default concurrency configuration.
func DefaultConcurrencyConfig() *ConcurrencyConfig {
	return &ConcurrencyConfig{
		MaxConcurrentTools: 3,
		ToolTimeout:        30,
		QueueTimeout:       60,
	}
}

// DefaultLogConfig returns default logging configuration.
func DefaultLogConfig() *LogConfig {
	homeDir, _ := os.UserHomeDir()
	return &LogConfig{
		Level:  "info",
		File:   filepath.Join(homeDir, ".ai", "ai.log"),
		Prefix: "[ai] ",
	}
}

// CreateLogger creates a logger from the log configuration.
func (c *LogConfig) CreateLogger() (*logger.Logger, error) {
	if c == nil {
		c = DefaultLogConfig()
	}

	cfg := &logger.Config{
		Level:    logger.ParseLogLevel(c.Level),
		Prefix:   c.Prefix,
		Console:  true,
		File:     c.File != "",
		FilePath: c.File,
	}

	return logger.NewLogger(cfg)
}

// LoadConfig loads configuration from file and merges with environment variables.
// Environment variables take precedence over config file values.
func LoadConfig(configPath string) (*Config, error) {
	// Start with default config
	cfg := &Config{
		Model: ModelConfig{
			ID:       getEnv("ZAI_MODEL", "glm-4.5-air"),
			Provider: "zai",
			BaseURL:  getEnv("ZAI_BASE_URL", "https://api.z.ai/api/coding/paas/v4"),
			API:      "openai-completions",
		},
		Compactor:   compact.DefaultConfig(),
		Concurrency: DefaultConcurrencyConfig(),
		Log:         DefaultLogConfig(),
	}

	// Load from file if exists
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		// Merge with defaults (file values override defaults)
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Environment variables override config file
	if val := os.Getenv("ZAI_MODEL"); val != "" {
		cfg.Model.ID = val
	}
	if val := os.Getenv("ZAI_BASE_URL"); val != "" {
		cfg.Model.BaseURL = val
	}

	return cfg, nil
}

// SaveConfig saves configuration to file.
func SaveConfig(cfg *Config, configPath string) error {
	// Ensure directory exists
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Marshal with indentation
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// GetLLMModel converts ModelConfig to llm.Model.
func (c *Config) GetLLMModel() llm.Model {
	return llm.Model{
		ID:       c.Model.ID,
		Provider: c.Model.Provider,
		BaseURL:  c.Model.BaseURL,
		API:      c.Model.API,
	}
}

// GetDefaultConfigPath returns the default config file path.
func GetDefaultConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(homeDir, ".ai", "config.json"), nil
}

// getEnv gets an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
