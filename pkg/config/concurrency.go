package config

import (
	"os"
	"strconv"
)

// ResolveConcurrencyConfig loads concurrency config from environment.
func ResolveConcurrencyConfig() *ConcurrencyConfig {
	cfg := DefaultConcurrencyConfig()

	// Read from environment variables
	if max := GetEnvInt("ZAI_MAX_CONCURRENT_TOOLS", 0); max > 0 {
		cfg.MaxConcurrentTools = max
	}
	if timeout := GetEnvInt("ZAI_TOOL_TIMEOUT", 0); timeout > 0 {
		cfg.ToolTimeout = timeout
	}
	if queue := GetEnvInt("ZAI_QUEUE_TIMEOUT", 0); queue > 0 {
		cfg.QueueTimeout = queue
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
