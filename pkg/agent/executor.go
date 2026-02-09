package agent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	defaultMaxRetries     = 3                  // Maximum retry attempts
	defaultInitialDelay  = 1 * time.Second   // Initial delay before retry
	defaultMaxDelay       = 10 * time.Second   // Maximum delay between retries
)

// RetryConfig configures retry behavior.
type RetryConfig struct {
	MaxRetries    int           // Maximum number of retry attempts
	InitialDelay  time.Duration // Initial delay before first retry
	MaxDelay      time.Duration // Maximum delay between retries
	RetryableErrs []string       // Error messages that are retryable
}

// DefaultRetryConfig creates a default retry configuration.
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries:    defaultMaxRetries,
		InitialDelay:  defaultInitialDelay,
		MaxDelay:      defaultMaxDelay,
		RetryableErrs: []string{
			"timeout",
			"connection refused",
			"connection reset",
			"EOF",
			"broken pipe",
			"temporarily unavailable",
			"rate limit",
		},
	}
}

// ToolExecutor manages concurrent tool execution with limits and retries.
type ToolExecutor struct {
	semaphore    chan struct{}
	toolTimeout  time.Duration
	queueTimeout time.Duration
	retryConfig  *RetryConfig
}

// NewToolExecutor creates a new tool executor.
func NewToolExecutor(maxConcurrent int, toolTimeoutSec, queueTimeoutSec int) *ToolExecutor {
	return &ToolExecutor{
		semaphore:    make(chan struct{}, maxConcurrent),
		toolTimeout:  time.Duration(toolTimeoutSec) * time.Second,
		queueTimeout: time.Duration(queueTimeoutSec) * time.Second,
		retryConfig:  DefaultRetryConfig(),
	}
}

// SetRetryConfig sets the retry configuration for this executor.
func (e *ToolExecutor) SetRetryConfig(config *RetryConfig) {
	e.retryConfig = config
}

// Execute runs a tool with concurrency control, timeout, and automatic retries.
func (e *ToolExecutor) Execute(ctx context.Context, tool Tool, args map[string]interface{}) ([]ContentBlock, error) {
	// Try to acquire semaphore (slot for execution)
	select {
	case e.semaphore <- struct{}{}:
		// Got slot, execute tool with retries
		defer func() { <-e.semaphore }()

		return e.executeWithRetries(ctx, tool, args)

	case <-time.After(e.queueTimeout):
		// Queue timeout
		return nil, fmt.Errorf("tool queue full, timeout after %v", e.queueTimeout)

	case <-ctx.Done():
		// Context cancelled
		return nil, ctx.Err()
	}
}

// executeWithRetries executes a tool with retry logic.
func (e *ToolExecutor) executeWithRetries(ctx context.Context, tool Tool, args map[string]interface{}) ([]ContentBlock, error) {
	var lastErr error
	delay := e.retryConfig.InitialDelay

	for attempt := 0; attempt <= e.retryConfig.MaxRetries; attempt++ {
		// Check for context cancellation before each attempt
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if attempt > 0 {
			log.Printf("[Executor] Retry attempt %d/%d for tool '%s' after %v delay",
				attempt, e.retryConfig.MaxRetries, tool.Name(), delay)

			// Wait before retry
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			// Exponential backoff with jitter
			delay = min(delay*2, e.retryConfig.MaxDelay)
			// Add jitter (±25%)
			jitter := time.Duration(float64(delay) * (0.75 + 0.5*(time.Now().UnixNano()%100)/100.0))
			delay = jitter
		}

		// Create timeout context for this attempt
		timeoutCtx, cancel := context.WithTimeout(ctx, e.toolTimeout)
		defer cancel()

		// Execute tool with timeout
		resultCh := make(chan toolResult, 1)
		go func() {
			content, err := tool.Execute(timeoutCtx, args)
			resultCh <- toolResult{content, err}
		}()

		select {
		case result := <-resultCh:
			if result.err == nil {
				// Success
				if attempt > 0 {
					log.Printf("[Executor] Tool '%s' succeeded on attempt %d", tool.Name(), attempt+1)
				}
				return result.content, nil
			}

			// Check if error is retryable
			if !e.isRetryable(result.err) {
				log.Printf("[Executor] Tool '%s' failed with non-retryable error: %v", tool.Name(), result.err)
				return nil, result.err
			}

			// Error is retryable, will retry
			lastErr = result.err
			log.Printf("[Executor] Tool '%s' failed (attempt %d/%d): %v",
				tool.Name(), attempt+1, e.retryConfig.MaxRetries+1, result.err)

		case <-timeoutCtx.Done():
			// Timeout is always retryable
			lastErr = fmt.Errorf("tool execution timeout after %v", e.toolTimeout)
			log.Printf("[Executor] Tool '%s' timed out (attempt %d/%d)",
				tool.Name(), attempt+1, e.retryConfig.MaxRetries+1)
		}
	}

	// All retries exhausted
	return nil, fmt.Errorf("tool '%s' failed after %d attempts: %w",
		tool.Name(), e.retryConfig.MaxRetries+1, lastErr)
}

// isRetryable checks if an error should trigger a retry.
func (e *ToolExecutor) isRetryable(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()
	if errMsg == "" {
		return false
	}

	// Check against retryable error patterns
	for _, pattern := range e.retryConfig.RetryableErrs {
		if contains(errMsg, pattern) {
			return true
		}
	}

	return false
}

// toolResult wraps tool execution result.
type toolResult struct {
	content []ContentBlock
	err     error
}

// min returns the minimum of two durations.
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// contains checks if a string contains a substring (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s[:len(substr)] == substr ||
		 containsIgnoreCase(s, substr))
}

func containsIgnoreCase(s, substr string) bool {
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			sc := s[i+j]
			sb := substr[j]
			if sc >= 'A' && sc <= 'Z' {
				sc += 32
			}
			if sb >= 'A' && sb <= 'Z' {
				sb += 32
			}
			if sc != sb {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// DefaultExecutor creates an executor with default settings.
func DefaultExecutor() *ToolExecutor {
	return NewToolExecutor(3, 30, 60)
}

// ExecutorPool manages a pool of executors for different tool types.
type ExecutorPool struct {
	mu              sync.RWMutex
	executors       map[string]*ToolExecutor
	defaultExecutor *ToolExecutor
}

// NewExecutorPool creates a new executor pool.
func NewExecutorPool(defaultConfig map[string]int) *ExecutorPool {
	maxConcurrent := defaultConfig["maxConcurrentTools"]
	toolTimeout := defaultConfig["toolTimeout"]
	queueTimeout := defaultConfig["queueTimeout"]

	return &ExecutorPool{
		executors: make(map[string]*ToolExecutor),
		defaultExecutor: NewToolExecutor(
			maxConcurrent,
			toolTimeout,
			queueTimeout,
		),
	}
}

// GetExecutor returns an executor for the given tool name.
func (p *ExecutorPool) GetExecutor(toolName string) *ToolExecutor {
	p.mu.RLock()
	executor, ok := p.executors[toolName]
	p.mu.RUnlock()

	if !ok {
		return p.defaultExecutor
	}

	return executor
}

// SetExecutor sets a custom executor for a specific tool.
func (p *ExecutorPool) SetExecutor(toolName string, executor *ToolExecutor) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.executors[toolName] = executor
}

// SetRetryConfig sets the retry configuration for a specific tool.
func (p *ExecutorPool) SetRetryConfig(toolName string, config *RetryConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()

	executor := p.executors[toolName]
	if executor == nil {
		executor = p.defaultExecutor
		p.executors[toolName] = executor
	}
	executor.SetRetryConfig(config)
}

// Execute runs a tool using the appropriate executor.
func (p *ExecutorPool) Execute(ctx context.Context, tool Tool, args map[string]interface{}) ([]ContentBlock, error) {
	executor := p.GetExecutor(tool.Name())
	log.Printf("[Executor] Executing tool '%s' (concurrency limit: %d, retries: %d)",
		tool.Name(), cap(executor.semaphore), executor.retryConfig.MaxRetries)

	return executor.Execute(ctx, tool, args)
}
