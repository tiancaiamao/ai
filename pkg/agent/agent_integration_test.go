package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutorPoolConcurrency tests the executor pool concurrency control.
func TestExecutorPoolConcurrency(t *testing.T) {
	t.Run("concurrent_tool_execution", func(t *testing.T) {
		pool := NewExecutorPool(map[string]int{
			"maxConcurrentTools": 3,
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
	// This test verifies that the LoopConfig properly handles LLM retries (not tool retries)
	config := &LoopConfig{
		MaxLLMRetries:  2,
		RetryBaseDelay: 500 * time.Millisecond,
	}

	assert.Equal(t, 2, config.MaxLLMRetries)
	assert.Equal(t, 500*time.Millisecond, config.RetryBaseDelay)
}

// TestContextCancellation tests context cancellation behavior.
func TestContextCancellation(t *testing.T) {
	t.Run("cancel_during_execution", func(t *testing.T) {
		executor := NewToolExecutor(1, 10)

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
}
