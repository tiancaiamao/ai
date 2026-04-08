package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestToolExecutorBasic tests basic tool execution.
func TestToolExecutorBasic(t *testing.T) {
	executor := NewToolExecutor(2, 10) // maxConcurrent=2, queueTimeout=10s
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
	executor := NewToolExecutor(2, 5) // Max 2 concurrent, 5s queue timeout
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

// TestToolExecutorQueueTimeout tests queue timeout.
func TestToolExecutorQueueTimeout(t *testing.T) {
	executor := NewToolExecutor(1, 1) // Max 1 concurrent, 1s queue timeout

	// Start a slow tool that will occupy the slot
	slowTool := &slowTool{delay: 5 * time.Second}

	ctx := context.Background()
	resultCh := make(chan error, 1)
	go func() {
		_, err := executor.Execute(ctx, slowTool, map[string]interface{}{})
		resultCh <- err
	}()

	// Wait for the slow tool to acquire the semaphore
	time.Sleep(200 * time.Millisecond)

	// Try to execute another tool - should timeout waiting for slot
	fastTool := &mockTool{name: "fast"}
	_, err := executor.Execute(context.Background(), fastTool, map[string]interface{}{})
	if err == nil {
		t.Error("Expected queue timeout error, got nil")
	}

	t.Logf("Got expected error: %v", err)
}

// TestToolExecutorContextCancellation tests context cancellation.
func TestToolExecutorContextCancellation(t *testing.T) {
	executor := NewToolExecutor(1, 5)
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
		"queueTimeout":       5,
	})

	tool1 := &mockTool{name: "tool1"}
	tool2 := &mockTool{name: "tool2"}

	ctx := context.Background()

	// Both should use the executor
	_, err := pool.Execute(ctx, tool1, map[string]interface{}{})
	if err != nil {
		t.Errorf("Execute tool1 failed: %v", err)
	}

	_, err = pool.Execute(ctx, tool2, map[string]interface{}{})
	if err != nil {
		t.Errorf("Execute tool2 failed: %v", err)
	}
}

// TestDefaultExecutor tests default executor creation.
func TestDefaultExecutor(t *testing.T) {
	executor := DefaultExecutor()

	if executor == nil {
		t.Fatal("DefaultExecutor should not return nil")
	}

	if cap(executor.semaphore) != 10 {
		t.Errorf("Expected default max concurrent 10, got %d", cap(executor.semaphore))
	}

	if executor.queueTimeout != 60*time.Second {
		t.Errorf("Expected default queue timeout 60s, got %v", executor.queueTimeout)
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

func (m *slowTool) Execute(ctx context.Context, args map[string]interface{}) ([]agentctx.ContentBlock, error) {
	if m.executeFunc != nil {
		m.executeFunc()
	}

	select {
	case <-time.After(m.delay):
		return []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "Slow tool completed"},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// MockTool is a mock tool for testing with configurable behavior.
type MockTool struct {
	name        string
	maxFailures int
	failMessage string
	execDelayMs int
	callCount   int
	mu          sync.Mutex
}

func (m *MockTool) Name() string {
	return m.name
}

func (m *MockTool) Description() string {
	return "Mock tool for testing"
}

func (m *MockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"input": map[string]interface{}{
			"type":        "string",
			"description": "Input parameter",
		},
	}
}

func (m *MockTool) Execute(ctx context.Context, args map[string]interface{}) ([]agentctx.ContentBlock, error) {
	m.mu.Lock()
	m.callCount++
	callNum := m.callCount
	shouldFail := m.maxFailures > 0 && callNum <= m.maxFailures
	m.mu.Unlock()

	// Simulate execution delay
	if m.execDelayMs > 0 {
		select {
		case <-time.After(time.Duration(m.execDelayMs) * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if shouldFail {
		return nil, fmt.Errorf("%s", m.failMessage)
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: m.failMessage},
	}, nil
}

func (m *MockTool) GetCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}
