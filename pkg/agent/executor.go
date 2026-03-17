package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"fmt"
	"time"

	"log/slog"
)

// ToolExecutor manages concurrent tool execution with limits.
// Tools are responsible for their own timeout handling.
type ToolExecutor struct {
	semaphore    chan struct{}
	queueTimeout time.Duration
}

// NewToolExecutor creates a new tool executor.
// maxConcurrent: maximum number of tools running concurrently
// queueTimeoutSec: how long to wait for a slot when all slots are busy (0 = wait indefinitely)
func NewToolExecutor(maxConcurrent int, queueTimeoutSec int) *ToolExecutor {
	return &ToolExecutor{
		semaphore:    make(chan struct{}, maxConcurrent),
		queueTimeout: time.Duration(queueTimeoutSec) * time.Second,
	}
}

// Execute runs a tool with concurrency control.
// The tool is responsible for its own timeout handling.
func (e *ToolExecutor) Execute(ctx context.Context, tool agentctx.Tool, args map[string]interface{}) ([]agentctx.ContentBlock, error) {
	// Try to acquire semaphore (slot for execution)
	select {
	case e.semaphore <- struct{}{}:
		// Got slot, execute tool
		defer func() { <-e.semaphore }()

		slog.Info("[Executor] Executing tool",
			"tool", tool.Name(),
			"concurrencyLimit", cap(e.semaphore))

		return tool.Execute(ctx, args)

	case <-time.After(e.queueTimeout):
		// Queue timeout
		return nil, fmt.Errorf("tool queue full, timeout after %v", e.queueTimeout)

	case <-ctx.Done():
		// Context cancelled
		return nil, ctx.Err()
	}
}

// DefaultExecutor creates an executor with default settings.
func DefaultExecutor() *ToolExecutor {
	return NewToolExecutor(10, 60) // 10 concurrent, 60s queue timeout
}

// ExecutorPool manages concurrent tool execution.
// Simplified version - all tools share the same executor.
type ExecutorPool struct {
	executor *ToolExecutor
}

// NewExecutorPool creates a new executor pool.
// config keys:
//   - "maxConcurrentTools": maximum concurrent tool executions
//   - "queueTimeout": how long to wait for a slot when all slots are busy
func NewExecutorPool(config map[string]int) *ExecutorPool {
	maxConcurrent := 10
	queueTimeout := 60

	if v, ok := config["maxConcurrentTools"]; ok {
		maxConcurrent = v
	}
	if v, ok := config["queueTimeout"]; ok {
		queueTimeout = v
	}

	return &ExecutorPool{
		executor: NewToolExecutor(maxConcurrent, queueTimeout),
	}
}

// Execute runs a tool with concurrency control.
func (p *ExecutorPool) Execute(ctx context.Context, tool agentctx.Tool, args map[string]interface{}) ([]agentctx.ContentBlock, error) {
	return p.executor.Execute(ctx, tool, args)
}
