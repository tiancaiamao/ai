package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
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
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"baseUrl"`
	API       string `json:"api"`
	MaxTokens int    `json:"maxTokens,omitempty"`
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
	cfg := DefaultConfig()

	// Load from file if exists
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Environment variables override config file
	cfg.Model.ID = getEnv("ZAI_MODEL", cfg.Model.ID)
	cfg.Model.BaseURL = getEnv("ZAI_BASE_URL", cfg.Model.BaseURL)
	cfg.Model.MaxTokens = getEnvInt("ZAI_MAX_TOKENS", cfg.Model.MaxTokens)

	cfg.ToolOutput = normalizeToolOutputConfig(cfg.ToolOutput)

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
		ID:        c.Model.ID,
		Provider:  c.Model.Provider,
		BaseURL:   c.Model.BaseURL,
		API:       c.Model.API,
		MaxTokens: c.Model.MaxTokens,
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

func getEnvInt(key string, defaultValue int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return parsed
}

// DefaultConfig returns a default Config with sensible values.
// This is the base configuration that can be overridden by LoadConfig.
func DefaultConfig() *Config {
	return &Config{
		Model: ModelConfig{
			ID:       "glm-4.5-air",
			Provider: "zai",
			BaseURL:  "https://api.z.ai/api/coding/paas/v4",
			API:      "openai-completions",
		},
		Compactor:         compact.DefaultConfig(),
		Concurrency:       DefaultConcurrencyConfig(),
		ToolOutput:        DefaultToolOutputConfig(),
		Log:               DefaultLogConfig(),
	}
}

// ToLoopConfig converts Config to agent.LoopConfig.
// This establishes the relationship between application config and agent config.
//
// Usage:
//
//	cfg := config.LoadConfig(path)
//	loopCfg := cfg.ToLoopConfig(
//	    config.WithCompactor(myCompactor),
//	    config.WithContextWindow(204800),
//	)
//	agent := agent.NewAgentFromConfig(model, apiKey, prompt, loopCfg)
func (c *Config) ToLoopConfig(opts ...LoopConfigOption) *agent.LoopConfig {
	loopCfg := agent.DefaultLoopConfig()

	// Override with config file values if present
	if c.Concurrency != nil {
		loopCfg.Executor = agent.NewExecutorPool(map[string]int{
			"maxConcurrentTools": c.Concurrency.MaxConcurrentTools,
			"queueTimeout":       c.Concurrency.QueueTimeout,
		})
	}

	if c.ToolOutput != nil {
		loopCfg.ToolOutput = agent.ToolOutputLimits{
			MaxChars: c.ToolOutput.MaxChars,
		}
	}

	// Apply options last (they can override config values)
	for _, opt := range opts {
		opt(loopCfg)
	}

	return loopCfg
}

// LoopConfigOption is a functional option for configuring LoopConfig.
type LoopConfigOption func(*agent.LoopConfig)

// WithCompactor sets a single compactor for context compression.
// Deprecated: Use WithCompactors([]agent.Compactor{...}) for multiple compactors.
func WithCompactor(compactor agent.Compactor) LoopConfigOption {
	return func(cfg *agent.LoopConfig) {
		if compactor != nil {
			cfg.Compactors = []agent.Compactor{compactor}
		}
	}
}

// WithCompactors sets multiple compactors for context compression.
// Array order determines execution priority (first trigger wins).
func WithCompactors(compactors []agent.Compactor) LoopConfigOption {
	return func(cfg *agent.LoopConfig) {
		cfg.Compactors = compactors
	}
}

// WithContextWindow sets the model context window size.
func WithContextWindow(window int) LoopConfigOption {
	return func(cfg *agent.LoopConfig) {
		cfg.ContextWindow = window
	}
}

// WithThinkingLevel sets the thinking level.
func WithThinkingLevel(level string) LoopConfigOption {
	return func(cfg *agent.LoopConfig) {
		cfg.ThinkingLevel = level
	}
}

// WithToolCallCutoff sets the tool call cutoff.
func WithToolCallCutoff(cutoff int) LoopConfigOption {
	return func(cfg *agent.LoopConfig) {
		cfg.ToolCallCutoff = cutoff
	}
}

// WithExecutor sets the executor for the loop config.
func WithExecutor(executor *agent.ExecutorPool) LoopConfigOption {
	return func(cfg *agent.LoopConfig) {
		cfg.Executor = executor
	}
}

// WithToolOutputLimits sets the tool output limits for the loop config.
func WithToolOutputLimits(limits agent.ToolOutputLimits) LoopConfigOption {
	return func(cfg *agent.LoopConfig) {
		cfg.ToolOutput = limits
	}
}
