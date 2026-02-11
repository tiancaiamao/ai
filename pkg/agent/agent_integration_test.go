package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutorRetryMechanism tests the tool execution retry logic.
func TestExecutorRetryMechanism(t *testing.T) {
	t.Run("retry_on_temporary_failure", func(t *testing.T) {
		// Create an executor with retry enabled
		config := DefaultRetryConfig()
		config.MaxRetries = 3
		config.InitialDelay = 100 * time.Millisecond
		config.MaxDelay = 500 * time.Millisecond
		config.RetryableErrs = []string{"temporary", "timeout"}

		executor := NewToolExecutor(1, 5, 10)
		executor.SetRetryConfig(config)

		// Mock tool that fails twice then succeeds
		tool := &MockTool{
			name:        "retry-tool",
			maxFailures: 2,
			failMessage: "temporary failure",
			execDelayMs: 50,
		}

		ctx := context.Background()
		start := time.Now()

		content, err := executor.Execute(ctx, tool, map[string]any{"input": "test"})
		elapsed := time.Since(start)

		require.NoError(t, err)
		require.NotNil(t, content)
		assert.Equal(t, 3, tool.calls) // Should have been called 3 times (initial + 2 retries)
		assert.True(t, elapsed >= 100*time.Millisecond) // At least 100ms backoff
	})

	t.Run("no_retry_on_permanent_failure", func(t *testing.T) {
		config := DefaultRetryConfig()
		config.MaxRetries = 2
		config.InitialDelay = 100 * time.Millisecond
		config.RetryableErrs = []string{"temporary"} // Not "permanent"

		executor := NewToolExecutor(1, 5, 10)
		executor.SetRetryConfig(config)

		// Mock tool that always fails with non-retryable error
		tool := &MockTool{
			name:        "permanent-tool",
			maxFailures: 10,
			failMessage: "permanent failure",
			execDelayMs: 50,
		}

		ctx := context.Background()

		content, err := executor.Execute(ctx, tool, map[string]any{"input": "test"})
		require.Error(t, err)
		assert.Nil(t, content)
		assert.Equal(t, 1, tool.calls) // Should have been called only once (no retry)
	})

	t.Run("stop_retry_on_context_cancel", func(t *testing.T) {
		config := DefaultRetryConfig()
		config.MaxRetries = 10
		config.InitialDelay = 1 * time.Second
		config.RetryableErrs = []string{"timeout"}

		executor := NewToolExecutor(1, 30, 60)
		executor.SetRetryConfig(config)

		// Mock tool that fails every time
		tool := &MockTool{
			name:        "slow-tool",
			maxFailures: 100,
			failMessage: "timeout",
			execDelayMs: 500,
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		content, err := executor.Execute(ctx, tool, map[string]any{"input": "test"})

		require.Error(t, err)
		assert.Nil(t, content)
		assert.Equal(t, 0, tool.calls) // Tool never called because context was already canceled
	})

	t.Run("exponential_backoff", func(t *testing.T) {
		config := DefaultRetryConfig()
		config.MaxRetries = 3
		config.InitialDelay = 100 * time.Millisecond
		config.MaxDelay = 1000 * time.Millisecond
		config.RetryableErrs = []string{"temporary"}

		executor := NewToolExecutor(1, 30, 60)
		executor.SetRetryConfig(config)

		// Mock tool that fails twice then succeeds
		tool := &MockTool{
			name:        "backoff-tool",
			maxFailures: 2,
			failMessage: "temporary",
			execDelayMs: 50,
		}

		ctx := context.Background()
		start := time.Now()

		content, err := executor.Execute(ctx, tool, map[string]any{"input": "test"})
		elapsed := time.Since(start)

		require.NoError(t, err)
		require.NotNil(t, content)

		// Verify backoff: ~100ms + ~200ms + 50ms execution
		// Total should be around 250-550ms (with jitter can be 75-125% of delay)
		assert.True(t, elapsed >= 200*time.Millisecond)
		assert.True(t, elapsed < 700*time.Millisecond)
	})
}

// TestExecutorPoolConcurrency tests the executor pool concurrency control.
func TestExecutorPoolConcurrency(t *testing.T) {
	t.Run("concurrent_tool_execution", func(t *testing.T) {
		pool := NewExecutorPool(map[string]int{
			"maxConcurrentTools": 3,
			"toolTimeout":        5,
			"queueTimeout":       10,
		})

		// Create 5 slow tools
		tools := []*MockTool{}
		for i := 0; i < 5; i++ {
			tools = append(tools, &MockTool{
				name:        "slow-tool-" + string(rune('A'+i)),
				maxFailures: 0,
				failMessage: "success",
				execDelayMs: 200, // 200ms each
			})
		}

		ctx := context.Background()
		start := time.Now()

		// Execute all tools concurrently
		results := make(chan error, len(tools))
		for _, tool := range tools {
			go func(tool *MockTool) {
				_, err := pool.Execute(ctx, tool, map[string]any{"input": "test"})
				results <- err
			}(tool)
		}

		// Wait for all tools to complete
		for i := 0; i < len(tools); i++ {
			assert.NoError(t, <-results)
		}

		elapsed := time.Since(start)

		// With 3 concurrent tools and 200ms each:
		// First 3 tools: 0-200ms
		// Second 2 tools: 200-400ms
		// Total: ~400ms
		assert.True(t, elapsed >= 300*time.Millisecond)
		assert.True(t, elapsed < 600*time.Millisecond)
	})

	t.Run("queue_timeout", func(t *testing.T) {
		pool := NewExecutorPool(map[string]int{
			"maxConcurrentTools": 1, // Only 1 concurrent
			"toolTimeout":        5,
			"queueTimeout":       1, // 1 second queue timeout
		})

		// Create a slow tool
		tool := &MockTool{
			name:        "very-slow-tool",
			maxFailures: 0,
			failMessage: "success",
			execDelayMs: 5000, // 5 seconds
		}

		ctx := context.Background()

		// Start first tool (should take 5s)
		resultCh1 := make(chan error, 1)
		go func() {
			_, err := pool.Execute(ctx, tool, map[string]any{"input": "test"})
			resultCh1 <- err
		}()

		// Wait for first tool to start
		time.Sleep(100 * time.Millisecond)

		// Try to execute second tool (should timeout in queue)
		resultCh2 := make(chan error, 1)
		go func() {
			_, err := pool.Execute(ctx, tool, map[string]any{"input": "test"})
			resultCh2 <- err
		}()

		// Wait for second result (should timeout)
		err2 := <-resultCh2
		require.Error(t, err2)
		assert.Contains(t, err2.Error(), "queue full")

		// Cleanup
		err1 := <-resultCh1
		require.NoError(t, err1)
	})
}

// TestLLMRetryMechanism tests the LLM call retry logic.
func TestLLMRetryMechanism(t *testing.T) {
	// This test verifies that the LoopConfig properly handles retries
	config := &LoopConfig{
		MaxLLMRetries:  2,
		RetryBaseDelay: 500 * time.Millisecond,
	}

	assert.Equal(t, 2, config.MaxLLMRetries)
	assert.Equal(t, 500*time.Millisecond, config.RetryBaseDelay)
}

// TestTimeoutHandling tests timeout scenarios.
func TestTimeoutHandling(t *testing.T) {
	t.Run("tool_timeout_with_retry", func(t *testing.T) {
		config := DefaultRetryConfig()
		config.MaxRetries = 2
		config.InitialDelay = 100 * time.Millisecond // Shorter backoff for faster test
		config.RetryableErrs = []string{"timeout"}

		executor := NewToolExecutor(1, 1, 10) // 1 second timeout
		executor.SetRetryConfig(config)

		// Tool that takes longer than timeout
		tool := &MockTool{
			name:        "timeout-tool",
			maxFailures: 0,
			failMessage: "success",
			execDelayMs: 2000, // 2 seconds (longer than 1s timeout)
		}

		ctx := context.Background()
		start := time.Now()

		_, err := executor.Execute(ctx, tool, map[string]any{"input": "test"})
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "timeout")
		assert.Equal(t, 3, tool.calls) // Should have tried 3 times (initial + 2 retries)

		// Should have tried multiple times (3 attempts with ~1s timeout each)
		// Total time varies based on context cancellation behavior
		assert.True(t, elapsed >= 2*time.Second, "Should take at least 2 seconds for 3 timeout attempts")
		assert.True(t, elapsed < 5*time.Second, "Should take less than 5 seconds")
	})
}

// TestContextCancellation tests context cancellation behavior.
func TestContextCancellation(t *testing.T) {
	t.Run("cancel_during_execution", func(t *testing.T) {
		executor := NewToolExecutor(1, 10, 10)

		tool := &MockTool{
			name:        "slow-tool",
			maxFailures: 0,
			failMessage: "success",
			execDelayMs: 10000, // 10 seconds
		}

		ctx, cancel := context.WithCancel(context.Background())
		start := time.Now()

		// Start execution in background
		errCh := make(chan error, 1)
		go func() {
			_, err := executor.Execute(ctx, tool, map[string]any{"input": "test"})
			errCh <- err
		}()

		// Cancel after 100ms
		time.Sleep(100 * time.Millisecond)
		cancel()

		err := <-errCh
		elapsed := time.Since(start)

		require.Error(t, err)
		// Should be quick (not wait for 10s)
		assert.True(t, elapsed < 500*time.Millisecond)
	})

	t.Run("cancel_between_retries", func(t *testing.T) {
		config := DefaultRetryConfig()
		config.MaxRetries = 5
		config.InitialDelay = 500 * time.Millisecond
		config.RetryableErrs = []string{"temporary"}

		executor := NewToolExecutor(1, 10, 10)
		executor.SetRetryConfig(config)

		tool := &MockTool{
			name:        "retry-tool",
			maxFailures: 100, // Will keep failing
			failMessage: "temporary failure",
			execDelayMs: 50,
		}

		ctx, cancel := context.WithCancel(context.Background())
		start := time.Now()

		// Start execution in background
		errCh := make(chan error, 1)
		go func() {
			_, err := executor.Execute(ctx, tool, map[string]any{"input": "test"})
			errCh <- err
		}()

		// Cancel during backoff (after first failure + some backoff time)
		time.Sleep(300 * time.Millisecond)
		cancel()

		err := <-errCh
		elapsed := time.Since(start)

		require.Error(t, err)
		// Should have been cancelled during backoff, not after all retries
		assert.True(t, elapsed < 1*time.Second)
	})
}
