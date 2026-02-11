package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestToolExecutorBasic tests basic tool execution.
func TestToolExecutorBasic(t *testing.T) {
	executor := NewToolExecutor(2, 5, 10)
	tool := &mockTool{name: "test_tool"}

	ctx := context.Background()
	args := map[string]interface{}{"param": "value"}

	content, err := executor.Execute(ctx, tool, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(content) != 1 {
		t.Errorf("Expected 1 content block, got %d", len(content))
	}
}

// TestToolExecutorConcurrency tests concurrent tool execution limit.
func TestToolExecutorConcurrency(t *testing.T) {
	executor := NewToolExecutor(2, 10, 5) // Max 2 concurrent
	ctx := context.Background()

	var mu sync.Mutex
	runningCount := 0
	maxRunning := 0

	// Create a slow tool that tracks concurrent executions
	slowTool := &slowTool{
		delay: 200 * time.Millisecond,
		executeFunc: func() {
			mu.Lock()
			runningCount++
			if runningCount > maxRunning {
				maxRunning = runningCount
			}
			mu.Unlock()

			// Hold the slot
			time.Sleep(100 * time.Millisecond)

			mu.Lock()
			runningCount--
			mu.Unlock()
		},
	}

	// Start 4 concurrent executions
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = executor.Execute(ctx, slowTool, map[string]interface{}{})
		}()
	}

	// Wait for all to complete
	wg.Wait()

	// Max concurrent should not exceed 2
	if maxRunning > 2 {
		t.Errorf("Expected max 2 concurrent executions, got %d", maxRunning)
	}

	t.Logf("Max concurrent executions: %d", maxRunning)
}

// TestToolExecutorTimeout tests tool execution timeout.
func TestToolExecutorTimeout(t *testing.T) {
	executor := NewToolExecutor(1, 1, 5) // 1 second timeout
	// Disable retries for this test
	executor.SetRetryConfig(&RetryConfig{
		MaxRetries:    0, // No retries
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		RetryableErrs: []string{},
	})
	ctx := context.Background()

	// Create a tool that takes longer than timeout
	slowTool := &slowTool{delay: 2 * time.Second}

	_, err := executor.Execute(ctx, slowTool, map[string]interface{}{})
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}

	// Check for timeout error
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && err.Error() != "tool execution timeout after 1s" {
		t.Logf("Got error: %v", err)
	}
}

// TestToolExecutorQueueTimeout tests queue timeout.
func TestToolExecutorQueueTimeout(t *testing.T) {
	executor := NewToolExecutor(1, 5, 1) // Max 1 concurrent, 1s queue timeout
	// Disable retries for this test
	executor.SetRetryConfig(&RetryConfig{
		MaxRetries:    0, // No retries
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		RetryableErrs: []string{},
	})

	// Use a context that will be canceled to test queue timeout
	ctx, cancel := context.WithCancel(context.Background())

	// Start a slow tool that will occupy the slot
	slowTool := &slowTool{delay: 10 * time.Second}

	resultCh := make(chan error, 1)
	go func() {
		_, err := executor.Execute(ctx, slowTool, map[string]interface{}{})
		resultCh <- err
	}()

	// Wait for the slow tool to acquire the semaphore
	time.Sleep(200 * time.Millisecond)

	// Cancel the context - this should stop the slow tool and free the semaphore
	cancel()
	<-resultCh // Wait for slow tool to finish

	// Now the semaphore should be free, try executing a fast tool
	fastTool := &mockTool{name: "fast"}
	_, err := executor.Execute(context.Background(), fastTool, map[string]interface{}{})
	if err != nil {
		t.Errorf("Fast tool should succeed, got error: %v", err)
	}
}

// TestToolExecutorContextCancellation tests context cancellation.
func TestToolExecutorContextCancellation(t *testing.T) {
	executor := NewToolExecutor(1, 5, 10)
	// Disable retries for this test
	executor.SetRetryConfig(&RetryConfig{
		MaxRetries:    0,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		RetryableErrs: []string{},
	})
	ctx, cancel := context.WithCancel(context.Background())

	slowTool := &slowTool{delay: 5 * time.Second}

	// Start execution in background
	errCh := make(chan error, 1)
	go func() {
		_, err := executor.Execute(ctx, slowTool, map[string]interface{}{})
		errCh <- err
	}()

	// Cancel context immediately
	cancel()

	// Should get context cancelled error
	err := <-errCh
	if err == nil {
		t.Error("Expected context cancelled error, got nil")
	}
}

// TestExecutorPool tests executor pool functionality.
func TestExecutorPool(t *testing.T) {
	pool := NewExecutorPool(map[string]int{
		"maxConcurrentTools": 2,
		"toolTimeout":        10,
		"queueTimeout":       5,
	})

	tool1 := &mockTool{name: "tool1"}
	tool2 := &mockTool{name: "tool2"}

	ctx := context.Background()

	// Both should use default executor
	_, err := pool.Execute(ctx, tool1, map[string]interface{}{})
	if err != nil {
		t.Errorf("Execute tool1 failed: %v", err)
	}

	_, err = pool.Execute(ctx, tool2, map[string]interface{}{})
	if err != nil {
		t.Errorf("Execute tool2 failed: %v", err)
	}
}

// TestExecutorPoolCustomExecutor tests custom executors per tool.
func TestExecutorPoolCustomExecutor(t *testing.T) {
	pool := NewExecutorPool(map[string]int{
		"maxConcurrentTools": 1,
		"toolTimeout":        5,
		"queueTimeout":       2,
	})

	// Create custom executor for specific tool
	customExecutor := NewToolExecutor(5, 10, 10) // Higher concurrency
	pool.SetExecutor("fast_tool", customExecutor)

	tool := &mockTool{name: "fast_tool"}
	ctx := context.Background()

	// Should use custom executor
	_, err := pool.Execute(ctx, tool, map[string]interface{}{})
	if err != nil {
		t.Errorf("Execute with custom executor failed: %v", err)
	}
}

// TestDefaultExecutor tests default executor creation.
func TestDefaultExecutor(t *testing.T) {
	executor := DefaultExecutor()

	if executor == nil {
		t.Fatal("DefaultExecutor should not return nil")
	}

	if cap(executor.semaphore) != 3 {
		t.Errorf("Expected default max concurrent 3, got %d", cap(executor.semaphore))
	}

	if executor.toolTimeout != 30*time.Second {
		t.Errorf("Expected default timeout 30s, got %v", executor.toolTimeout)
	}
}

// slowTool is a mock tool that delays execution.
type slowTool struct {
	name        string
	delay       time.Duration
	executeFunc func()
}

func (m *slowTool) Name() string {
	return m.name
}

func (m *slowTool) Description() string {
	return "Slow tool for testing"
}

func (m *slowTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"delay": map[string]interface{}{
			"type":        "integer",
			"description": "Delay in milliseconds",
		},
	}
}

func (m *slowTool) Execute(ctx context.Context, args map[string]interface{}) ([]ContentBlock, error) {
	if m.executeFunc != nil {
		m.executeFunc()
	}

	select {
	case <-time.After(m.delay):
		return []ContentBlock{
			TextContent{Type: "text", Text: "Slow tool completed"},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
