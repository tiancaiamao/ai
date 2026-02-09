package agent

import (
	"context"
	"fmt"
	"log"
	"time"
)

// RetryConfig holds retry configuration.
type RetryConfig struct {
	MaxAttempts int           // Maximum number of attempts (including initial)
	InitialDelay time.Duration // Initial delay before retry
	MaxDelay    time.Duration // Maximum delay between retries
	Multiplier  float64       // Delay multiplier for exponential backoff
}

// DefaultRetryConfig returns a sensible default retry configuration.
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxAttempts: 3,
		InitialDelay: 1 * time.Second,
		MaxDelay:    4 * time.Second,
		Multiplier:  2.0,
	}
}

// RetryPolicy determines whether a tool execution should be retried.
type RetryPolicy func(error) bool

// DefaultRetryPolicy returns a policy that retries on transient errors.
func DefaultRetryPolicy() RetryPolicy {
	return func(err error) bool {
		if err == nil {
			return false
		}
		errStr := err.Error()
		// Retry on common transient errors
		transientErrors := []string{
			"timeout",
			"connection refused",
			"connection reset",
			"temporary failure",
			"try again",
			"rate limit",
			"too many requests",
			"service unavailable",
			"503",
			"502",
			"504",
		}
		for _, pattern := range transientErrors {
			if containsIgnoreCase(errStr, pattern) {
				log.Printf("[Retry] Retrying due to transient error: %v", err)
				return true
			}
		}
		return false
	}
}

// ToolExecutorWithRetry wraps a ToolExecutor with retry logic.
type ToolExecutorWithRetry struct {
	inner *ToolExecutor
	config *RetryConfig
	policy RetryPolicy
}

// NewToolExecutorWithRetry creates a new executor with retry support.
func NewToolExecutorWithRetry(
	inner *ToolExecutor,
	config *RetryConfig,
	policy RetryPolicy,
) *ToolExecutorWithRetry {
	if config == nil {
		config = DefaultRetryConfig()
	}
	if policy == nil {
		policy = DefaultRetryPolicy()
	}
	return &ToolExecutorWithRetry{
		inner:  inner,
		config: config,
		policy: policy,
	}
}

// Execute runs a tool with retry logic.
func (r *ToolExecutorWithRetry) Execute(
	ctx context.Context,
	tool Tool,
	args map[string]interface{},
) ([]ContentBlock, error) {
	var lastErr error
	delay := r.config.InitialDelay

	for attempt := 1; attempt <= r.config.MaxAttempts; attempt++ {
		if attempt > 1 {
			log.Printf("[Retry] Attempt %d/%d for tool '%s' after %v delay",
				attempt, r.config.MaxAttempts, tool.Name(), delay)
			// Wait before retry, but respect context cancellation
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, fmt.Errorf("retry cancelled: %w", ctx.Err())
			}
			// Exponential backoff
			delay = time.Duration(float64(delay) * r.config.Multiplier)
			if delay > r.config.MaxDelay {
				delay = r.config.MaxDelay
			}
		}

		content, err := r.inner.Execute(ctx, tool, args)
		if err == nil {
			if attempt > 1 {
				log.Printf("[Retry] Tool '%s' succeeded on attempt %d", tool.Name(), attempt)
			}
			return content, nil
		}

		lastErr = err
		log.Printf("[Retry] Tool '%s' failed on attempt %d: %v", tool.Name(), attempt, err)

		// Check if error is retryable
		if !r.policy(err) || attempt == r.config.MaxAttempts {
			break
		}
	}

	return nil, fmt.Errorf("tool '%s' failed after %d attempts: %w",
		tool.Name(), r.config.MaxAttempts, lastErr)
}

// containsIgnoreCase checks if a string contains a substring (case-insensitive).
func containsIgnoreCase(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			(s[:len(substr)] == substr) ||
			(s[len(s)-len(substr):] == substr) ||
			findIgnoreCase(s, substr))
}

func findIgnoreCase(s, substr string) bool {
	s = toLower(s)
	substr = toLower(substr)
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + ('a' - 'A')
		} else {
			result[i] = c
		}
	}
	return string(result)
}
