package config

import (
	"fmt"
	"time"
)

// ConcurrencyConfig controls concurrent execution behavior.
type ConcurrencyConfig struct {
	MaxConcurrentTools int           `json:"maxConcurrentTools"` // Maximum tools running concurrently
	ToolTimeout        time.Duration `json:"toolTimeout"`        // Per-tool execution timeout
	QueueTimeout       time.Duration `json:"queueTimeout"`       // Wait timeout for queue slot
}

// DefaultConcurrencyConfig returns a default concurrency configuration.
func DefaultConcurrencyConfig() *ConcurrencyConfig {
	return &ConcurrencyConfig{
		MaxConcurrentTools: 3,
		ToolTimeout:        30 * time.Second,
		QueueTimeout:       60 * time.Second,
	}
}

// ResolveConcurrencyConfig loads concurrency config from environment.
func ResolveConcurrencyConfig() *ConcurrencyConfig {
	cfg := DefaultConcurrencyConfig()

	// Read from environment variables
	if max := GetEnvInt("ZAI_MAX_CONCURRENT_TOOLS", 0); max > 0 {
		cfg.MaxConcurrentTools = max
	}
	if timeout := GetEnvInt("ZAI_TOOL_TIMEOUT", 0); timeout > 0 {
		cfg.ToolTimeout = time.Duration(timeout) * time.Second
	}
	if queue := GetEnvInt("ZAI_QUEUE_TIMEOUT", 0); queue > 0 {
		cfg.QueueTimeout = time.Duration(queue) * time.Second
	}

	return cfg
}

// GetEnvInt gets an integer environment variable or returns a default.
func GetEnvInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}
