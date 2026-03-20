package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"log/slog"

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

	// Tool output configuration
	ToolOutput *ToolOutputConfig `json:"toolOutput,omitempty"`

	// Edit tool configuration
	Edit *EditConfig `json:"edit,omitempty"`

	// Logging configuration
	Log *LogConfig `json:"log,omitempty"`

	// Task tracking configuration (llm_context_update)
	TaskTracking *TaskTrackingConfig `json:"taskTracking,omitempty"`

	// Context management configuration (llm_context_decision)
	ContextManagement *ContextManagementConfig `json:"contextManagement,omitempty"`

	// Workspace is the working directory path (the git repo path bound at startup)
	Workspace string `json:"workspace,omitempty"`
}

// LogConfig contains logging configuration.
type LogConfig struct {
	Level   string `json:"level,omitempty"`   // Log level: debug, info, warn, error
	File    string `json:"file,omitempty"`    // Log file path (empty = no file logging)
	Console bool   `json:"console,omitempty"` // Enable console output (default: false)
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

// ToolOutputConfig contains tool output truncation settings.
type ToolOutputConfig struct {
	MaxChars  int  `json:"maxChars,omitempty"`  // Maximum characters to keep (0 = default)
	HashLines bool `json:"hashLines,omitempty"` // Enable hashline mode for read tool
}

// EditConfig contains edit tool settings.
type EditConfig struct {
	Mode string `json:"mode,omitempty"` // Edit mode: "replace" (default) or "hashline"
}

// TaskTrackingConfig contains task tracking (llm_context_update) settings.
type TaskTrackingConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // Enable task tracking prompt and reminders. nil = default (true)
}

// ContextManagementConfig contains context management (llm_context_decision) settings.
type ContextManagementConfig struct {
	Enabled *bool `json:"enabled,omitempty"` // Enable context management prompt and reminders. nil = default (true)
}

const (
	defaultToolOutputMaxChars = 10_000
	maxToolOutputMaxChars     = defaultToolOutputMaxChars
)

// DefaultConcurrencyConfig returns default concurrency configuration.
func DefaultConcurrencyConfig() *ConcurrencyConfig {
	return &ConcurrencyConfig{
		MaxConcurrentTools: 5,
		ToolTimeout:        30,
		QueueTimeout:       60,
	}
}

// DefaultToolOutputConfig returns default tool output truncation configuration.
func DefaultToolOutputConfig() *ToolOutputConfig {
	return &ToolOutputConfig{
		MaxChars:  defaultToolOutputMaxChars,
		HashLines: false,
	}
}

// DefaultEditConfig returns default edit tool configuration.
func DefaultEditConfig() *EditConfig {
	return &EditConfig{
		Mode: "replace", // Default to replace mode
	}
}

// DefaultTaskTrackingConfig returns default task tracking configuration.
func DefaultTaskTrackingConfig() *TaskTrackingConfig {
	return &TaskTrackingConfig{
		Enabled: ptrBool(true),
	}
}

// DefaultContextManagementConfig returns default context management configuration.
func DefaultContextManagementConfig() *ContextManagementConfig {
	return &ContextManagementConfig{
		Enabled: ptrBool(true),
	}
}

func normalizeToolOutputConfig(cfg *ToolOutputConfig) *ToolOutputConfig {
	if cfg == nil {
		return DefaultToolOutputConfig()
	}
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = defaultToolOutputMaxChars
	}
	if cfg.MaxChars > maxToolOutputMaxChars {
		cfg.MaxChars = maxToolOutputMaxChars
	}
	return cfg
}

func normalizeTaskTrackingConfig(cfg *TaskTrackingConfig) *TaskTrackingConfig {
	if cfg == nil {
		return DefaultTaskTrackingConfig()
	}
	if cfg.Enabled == nil {
		cfg.Enabled = ptrBool(true)
	}
	return cfg
}

func normalizeContextManagementConfig(cfg *ContextManagementConfig) *ContextManagementConfig {
	if cfg == nil {
		return DefaultContextManagementConfig()
	}
	if cfg.Enabled == nil {
		cfg.Enabled = ptrBool(true)
	}
	return cfg
}

func ptrBool(v bool) *bool {
	return &v
}

// DefaultLogConfig returns default logging configuration.
func DefaultLogConfig() *LogConfig {
	homeDir, _ := os.UserHomeDir()
	return &LogConfig{
		Level:   "info",
		File:    filepath.Join(homeDir, ".ai", "ai-{pid}.log"),
		Console: false,
	}
}

// CreateLogger creates a logger from the log configuration.
func (c *LogConfig) CreateLogger() (*slog.Logger, error) {
	if c == nil {
		c = DefaultLogConfig()
	}

	cfg := &logger.Config{}

	return logger.NewLogger(cfg)
}

// ResolveLogPath returns the resolved log file path with PID expansion.
func ResolveLogPath(c *LogConfig) string {
	return resolveLogPath(c)
}

func resolveLogPath(c *LogConfig) string {
	if c == nil {
		c = DefaultLogConfig()
	}
	path := strings.TrimSpace(c.File)
	if path == "" {
		return ""
	}
	return expandLogPath(path)
}

func expandLogPath(path string) string {
	pid := strconv.Itoa(os.Getpid())
	path = strings.ReplaceAll(path, "{pid}", pid)
	path = strings.ReplaceAll(path, "{PID}", pid)
	return path
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
		ToolOutput:  DefaultToolOutputConfig(),
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
	cfg.ToolOutput = normalizeToolOutputConfig(cfg.ToolOutput)
	cfg.TaskTracking = normalizeTaskTrackingConfig(cfg.TaskTracking)
	cfg.ContextManagement = normalizeContextManagementConfig(cfg.ContextManagement)

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
