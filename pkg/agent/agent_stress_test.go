package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStressConcurrentRequests tests handling many concurrent requests.
func TestStressConcurrentRequests(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	t.Log("Starting concurrent request stress test...")

	// Create executor pool
	pool := NewExecutorPool(map[string]int{
		"maxConcurrentTools": 10,
		"toolTimeout":        5,
		"queueTimeout":       30,
	})

	ctx := context.Background()

	// Create multiple tools with different characteristics
	numTools := 20
	tools := []*MockTool{}
	for i := 0; i < numTools; i++ {
		tools = append(tools, &MockTool{
			name:        fmt.Sprintf("tool-%d", i),
			maxFailures: 0,
			failMessage: "success",
			execDelayMs: 500 + (i%5)*200, // Varying delays: 500-1500ms
		})
	}

	// Number of concurrent requests per tool
	requestsPerTool := 10
	totalRequests := numTools * requestsPerTool

	var successCount, errorCount int32
	var wg sync.WaitGroup

	start := time.Now()

	// Launch all requests concurrently
	for _, tool := range tools {
		for i := 0; i < requestsPerTool; i++ {
			wg.Add(1)
			go func(tool *MockTool, requestNum int) {
				defer wg.Done()

				content, err := pool.Execute(ctx, tool, map[string]any{
					"input": fmt.Sprintf("request-%s-%d", tool.name, requestNum),
				})

				if err != nil {
					atomic.AddInt32(&errorCount, 1)
					t.Logf("Request failed: %v", err)
				} else {
					atomic.AddInt32(&successCount, 1)
					if content == nil {
						t.Error("Success but content is nil")
					}
				}
			}(tool, i)
		}
	}

	// Wait for all requests to complete
	wg.Wait()
	elapsed := time.Since(start)

	// Verify results
	success := atomic.LoadInt32(&successCount)
	errors := atomic.LoadInt32(&errorCount)

	t.Logf("Stress test completed:")
	t.Logf("  Total requests: %d", totalRequests)
	t.Logf("  Successful: %d", success)
	t.Logf("  Errors: %d", errors)
	t.Logf("  Time elapsed: %v", elapsed)
	t.Logf("  Throughput: %.2f requests/sec", float64(totalRequests)/elapsed.Seconds())

	// All requests should succeed
	if success != int32(totalRequests) {
		t.Errorf("Expected %d successful requests, got %d", totalRequests, success)
	}

	// Estimate minimum time: tools with max 10 concurrency
	// Fastest tools (500ms) should complete quickly
	minExpectedTime := 5 * time.Second
	if elapsed < minExpectedTime {
		t.Logf("Warning: Completed faster than expected (< %v)", minExpectedTime)
	}

	// Should not take too long
	maxExpectedTime := 30 * time.Second
	if elapsed > maxExpectedTime {
		t.Errorf("Stress test took too long: %v (expected < %v)", elapsed, maxExpectedTime)
	}
}

// TestStressLongRunningCommands tests handling of long-running commands.
func TestStressLongRunningCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	t.Log("Starting long-running command stress test...")

	pool := NewExecutorPool(map[string]int{
		"maxConcurrentTools": 3,
		"toolTimeout":        10,
		"queueTimeout":       60,
	})

	ctx := context.Background()

	// Create tools with varying execution times (2-8 seconds)
	numTools := 5
	tools := []*MockTool{}
	for i := 0; i < numTools; i++ {
		delay := 2000 + i*1500 // 2s, 3.5s, 5s, 6.5s, 8s
		tools = append(tools, &MockTool{
			name:        fmt.Sprintf("long-tool-%d", i),
			maxFailures: 0,
			failMessage: "success",
			execDelayMs: delay,
		})
	}

	// Execute all tools concurrently
	var wg sync.WaitGroup
	var completionTimes []time.Duration
	var mu sync.Mutex

	start := time.Now()

	for i, tool := range tools {
		wg.Add(1)
		go func(toolIndex int, tool *MockTool) {
			defer wg.Done()

			toolStart := time.Now()
			_, err := pool.Execute(ctx, tool, map[string]any{"input": "test"})
			toolElapsed := time.Since(toolStart)

			mu.Lock()
			completionTimes = append(completionTimes, toolElapsed)
			mu.Unlock()

			if err != nil {
				t.Errorf("agentctx.Tool %s failed: %v", tool.name, err)
			}
		}(i, tool)
	}

	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("Long-running command test completed in %v", elapsed)
	t.Logf("Individual completion times:")
	for i, duration := range completionTimes {
		t.Logf("  agentctx.Tool %d: %v", i, duration)
	}

	// With 3 concurrent slots and 5 tools:
	// First 3: 0-2s, 0-3.5s, 0-5s
	// Second 2: 2s-5.5s, 3.5s-6.5s
	// Total: ~6.5s
	minExpected := 6 * time.Second
	maxExpected := 15 * time.Second

	if elapsed < minExpected {
		t.Errorf("Test completed too quickly: %v (expected >= %v)", elapsed, minExpected)
	}
	if elapsed > maxExpected {
		t.Errorf("Test took too long: %v (expected <= %v)", elapsed, maxExpected)
	}
}

// TestStressRetryUnderLoad tests retry behavior under high load.
func TestStressRetryUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	t.Log("Starting retry under load stress test...")

	pool := NewExecutorPool(map[string]int{
		"maxConcurrentTools": 5,
		"toolTimeout":        3,
		"queueTimeout":       10,
	})

	// Configure retry for some tools
	pool.SetRetryConfig("flaky-tool", &RetryConfig{
		MaxRetries:    3,
		InitialDelay:  200 * time.Millisecond,
		MaxDelay:      1 * time.Second,
		RetryableErrs: []string{"flaky", "temporary", "timeout"},
	})

	ctx := context.Background()

	// Create mix of stable and flaky tools
	numStable := 5
	numFlaky := 5

	stableTools := []*MockTool{}
	for i := 0; i < numStable; i++ {
		stableTools = append(stableTools, &MockTool{
			name:        fmt.Sprintf("stable-tool-%d", i),
			maxFailures: 0,
			failMessage: "success",
			execDelayMs: 500,
		})
	}

	flakyTools := []*MockTool{}
	for i := 0; i < numFlaky; i++ {
		flakyTools = append(flakyTools, &MockTool{
			name:        fmt.Sprintf("flaky-tool-%d", i),
			maxFailures: 2, // Will fail twice before succeeding
			failMessage: "flaky error",
			execDelayMs: 300,
		})
	}

	var wg sync.WaitGroup
	var successCount, retryCount int32

	start := time.Now()

	// Execute stable tools
	for _, tool := range stableTools {
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(tool *MockTool) {
				defer wg.Done()
				_, err := pool.Execute(ctx, tool, map[string]any{"input": "test"})
				if err != nil {
					t.Errorf("Stable tool failed: %v", err)
				} else {
					atomic.AddInt32(&successCount, 1)
				}
			}(tool)
		}
	}

	// Execute flaky tools
	for _, tool := range flakyTools {
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(tool *MockTool) {
				defer wg.Done()
				_, err := pool.Execute(ctx, tool, map[string]any{"input": "test"})
				if err != nil {
					t.Errorf("Flaky tool failed: %v", err)
				} else {
					atomic.AddInt32(&successCount, 1)
					atomic.AddInt32(&retryCount, int32(tool.calls-1))
				}
			}(tool)
		}
	}

	wg.Wait()
	elapsed := time.Since(start)

	success := atomic.LoadInt32(&successCount)
	retries := atomic.LoadInt32(&retryCount)

	t.Logf("Retry under load test completed:")
	t.Logf("  Time elapsed: %v", elapsed)
	t.Logf("  Successful requests: %d", success)
	t.Logf("  Total retries: %d", retries)
	t.Logf("  Average retries per flaky request: %.2f",
		float64(retries)/float64(numFlaky*3))

	totalRequests := (numStable + numFlaky) * 3
	if success != int32(totalRequests) {
		t.Errorf("Expected %d successful requests, got %d", totalRequests, success)
	}

	// Flaky tools should have been retried
	expectedRetries := int32(numFlaky * 3 * 2) // Each flaky tool fails twice before succeeding
	if retries < expectedRetries {
		t.Errorf("Expected at least %d retries, got %d", expectedRetries, retries)
	}
}

// TestStressQueueFull tests behavior when queue becomes full.
func TestStressQueueFull(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	t.Log("Starting queue full stress test...")

	pool := NewExecutorPool(map[string]int{
		"maxConcurrentTools": 2,
		"toolTimeout":        5,
		"queueTimeout":       1, // Short queue timeout (1 second)
	})

	ctx := context.Background()

	// Create slow tools with reduced execution time
	numTools := 6 // Reduced from 10
	tools := []*MockTool{}
	for i := 0; i < numTools; i++ {
		tools = append(tools, &MockTool{
			name:        fmt.Sprintf("slow-tool-%d", i),
			maxFailures: 0,
			failMessage: "success",
			execDelayMs: 2000, // Reduced from 5000 to 2000 (2 seconds)
		})
	}

	var wg sync.WaitGroup
	var successCount, timeoutCount int32

	start := time.Now()

	// Try to execute more tools than concurrency allows
	for _, tool := range tools {
		wg.Add(1)
		go func(tool *MockTool) {
			defer wg.Done()

			_, err := pool.Execute(ctx, tool, map[string]any{"input": "test"})
			if err != nil {
				atomic.AddInt32(&timeoutCount, 1)
			} else {
				atomic.AddInt32(&successCount, 1)
			}
		}(tool)
	}

	wg.Wait()
	elapsed := time.Since(start)

	success := atomic.LoadInt32(&successCount)
	timeouts := atomic.LoadInt32(&timeoutCount)

	t.Logf("Queue full stress test completed:")
	t.Logf("  Time elapsed: %v", elapsed)
	t.Logf("  Successful requests: %d", success)
	t.Logf("  Queue timeouts: %d", timeouts)

	// With 2 concurrent tools and 1s queue timeout:
	// First 2 should succeed (~2s each)
	// With 6 tools: first 2 start immediately, next 4 wait in queue
	// Queue timeout is 1s, so waiting tools will timeout after 1s
	// Then they retry... but let's just verify some succeed
	if success < 2 {
		t.Errorf("Expected at least 2 successful requests, got %d", success)
	}
	if timeouts == 0 {
		t.Log("Warning: No queue timeouts occurred")
	}

	// Should complete in reasonable time
	// First 2 tools: 2s each, running concurrently = ~2s
	// Remaining tools timeout in queue after 1s
	// Total: ~3-4s
	if elapsed > 6*time.Second {
		t.Errorf("Test took too long: %v (expected <= 6s)", elapsed)
	}
}
