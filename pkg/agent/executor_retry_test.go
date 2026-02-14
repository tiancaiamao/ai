package agent

import (
	"context"
	"errors"
	"testing"
	"time"
)

// MockTool for testing retry logic
type MockTool struct {
	name        string
	failCount   int
	maxFailures int
	failMessage string
	calls       int
	execDelayMs int
}

func (m *MockTool) Name() string {
	return m.name
}

func (m *MockTool) Description() string {
	return "Mock tool for testing"
}

func (m *MockTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"input"},
	}
}

func (m *MockTool) Execute(ctx context.Context, args map[string]any) ([]ContentBlock, error) {
	m.calls++

	if m.calls <= m.maxFailures {
		return nil, errors.New(m.failMessage)
	}

	if m.execDelayMs > 0 {
		time.Sleep(time.Duration(m.execDelayMs) * time.Millisecond)
	}

	return []ContentBlock{
		TextContent{
			Type: "text",
			Text: "success",
		},
	}, nil
}

// Reset resets the mock tool state
func (m *MockTool) Reset() {
	m.calls = 0
}

func TestToolExecutorRetryOnFailure(t *testing.T) {
	tests := []struct {
		name          string
		tool          *MockTool
		maxRetries    int
		expectError   bool
		expectedCalls int
	}{
		{
			name: "succeeds on first try",
			tool: &MockTool{
				name:        "test-tool",
				maxFailures: 0,
				failMessage: "temporary failure",
			},
			maxRetries:    3,
			expectError:   false,
			expectedCalls: 1,
		},
		{
			name: "retries once then succeeds",
			tool: &MockTool{
				name:        "test-tool-2",
				maxFailures: 1,
				failMessage: "temporary failure",
			},
			maxRetries:    3,
			expectError:   false,
			expectedCalls: 2,
		},
		{
			name: "fails after all retries",
			tool: &MockTool{
				name:        "test-tool-3",
				maxFailures: 5,
				failMessage: "persistent failure",
			},
			maxRetries:    3,
			expectError:   true,
			expectedCalls: 4, // 1 initial + 3 retries
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.tool.Reset()

			executor := NewToolExecutor(1, 30, 60)
			executor.SetRetryConfig(&RetryConfig{
				MaxRetries:    tt.maxRetries,
				InitialDelay:  1 * time.Second,
				MaxDelay:      5 * time.Second,
				RetryableErrs: []string{"temporary", "persistent", "timeout", "fail"},
			})

			ctx := context.Background()
			args := map[string]any{"input": "test"}

			result, err := executor.Execute(ctx, tt.tool, args)

			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.tool.calls != tt.expectedCalls {
				t.Errorf("expected %d calls, got %d", tt.expectedCalls, tt.tool.calls)
			}
			if !tt.expectError && result == nil {
				t.Errorf("expected result but got nil")
			}
		})
	}
}

func TestToolExecutorRetryWithExponentialBackoff(t *testing.T) {
	tool := &MockTool{
		name:        "backoff-test",
		maxFailures: 2,
		failMessage: "temporary failure",
		execDelayMs: 100,
	}

	executor := NewToolExecutor(1, 30, 60)
	executor.SetRetryConfig(&RetryConfig{
		MaxRetries:    3,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      1000 * time.Millisecond,
		RetryableErrs: []string{"temporary", "timeout"},
	})

	ctx := context.Background()
	args := map[string]any{"input": "test"}

	start := time.Now()
	result, err := executor.Execute(ctx, tool, args)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Errorf("expected result but got nil")
	}

	// Should have taken at least 100ms (first backoff) + some execution time
	// Note: actual time varies due to jitter and execution speed, so we use a more lenient check
	minExpected := 250 * time.Millisecond
	if elapsed < minExpected {
		t.Errorf("expected at least %v, got %v", minExpected, elapsed)
	}

	// Verify retries happened
	if tool.calls != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", tool.calls)
	}

	t.Logf("Execution took %v with %d retries", elapsed, tool.calls-1)
}

func TestToolExecutorNoRetryOnContextCancel(t *testing.T) {
	tool := &MockTool{
		name:        "cancel-test",
		maxFailures: 100,
		failMessage: "will fail forever",
		execDelayMs: 500,
	}

	executor := NewToolExecutor(1, 30, 60)
	executor.SetRetryConfig(&RetryConfig{
		MaxRetries:    3,
		InitialDelay:  5 * time.Second,
		MaxDelay:      10 * time.Second,
		RetryableErrs: []string{"fail"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	args := map[string]any{"input": "test"}

	// Cancel immediately
	cancel()

	start := time.Now()
	result, err := executor.Execute(ctx, tool, args)
	elapsed := time.Since(start)

	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
	if err == nil {
		t.Errorf("expected error on context cancel, got nil")
	}

	// Should fail quickly due to context cancel, not wait for full timeout
	maxExpected := 200 * time.Millisecond
	if elapsed > maxExpected {
		t.Errorf("expected quick cancel (< %v), got %v", maxExpected, elapsed)
	}

	t.Logf("Context canceled in %v after %d calls", elapsed, tool.calls)
}

func TestExecutorPoolRetryPerTool(t *testing.T) {
	pool := NewExecutorPool(map[string]int{
		"maxConcurrentTools": 3,
		"toolTimeout":        30,
		"queueTimeout":       60,
	})

	// Configure different retry policies per tool
	toolA := &MockTool{name: "tool-a", maxFailures: 1, failMessage: "fail"}
	toolB := &MockTool{name: "tool-b", maxFailures: 0, failMessage: "fail"}

	executorA := NewToolExecutor(1, 30, 60)
	executorA.SetRetryConfig(&RetryConfig{
		MaxRetries:    2,
		InitialDelay:  100 * time.Millisecond,
		MaxDelay:      500 * time.Millisecond,
		RetryableErrs: []string{"fail"},
	})

	executorB := NewToolExecutor(1, 30, 60)
	executorB.SetRetryConfig(&RetryConfig{
		MaxRetries:    0,
		InitialDelay:  0,
		MaxDelay:      0,
		RetryableErrs: []string{},
	})

	pool.SetExecutor("tool-a", executorA)
	pool.SetExecutor("tool-b", executorB)

	ctx := context.Background()

	// Tool A should retry and succeed
	resultA, errA := pool.Execute(ctx, toolA, map[string]any{"input": "test"})
	if errA != nil {
		t.Errorf("tool A failed: %v", errA)
	}
	if resultA == nil {
		t.Errorf("tool A result is nil")
	}
	if toolA.calls != 2 {
		t.Errorf("expected tool A to be called 2 times, got %d", toolA.calls)
	}

	// Tool B should succeed immediately
	resultB, errB := pool.Execute(ctx, toolB, map[string]any{"input": "test"})
	if errB != nil {
		t.Errorf("tool B failed: %v", errB)
	}
	if resultB == nil {
		t.Errorf("tool B result is nil")
	}
	if toolB.calls != 1 {
		t.Errorf("expected tool B to be called 1 time, got %d", toolB.calls)
	}

	t.Logf("Tool A: %d calls, Tool B: %d calls", toolA.calls, toolB.calls)
}
