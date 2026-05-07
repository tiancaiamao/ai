package agent

import (
	"context"
	"fmt"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"

	"log/slog"
)

// ToolExecutor is the interface for executing tools with concurrency control.
// All callers should depend on this interface, not the concrete type.
type ToolExecutor interface {
	Execute(ctx context.Context, tool agentctx.Tool, args map[string]interface{}) ([]agentctx.ContentBlock, error)
}

// concurrentToolExecutor manages concurrent tool execution with limits.
// Tools are responsible for their own timeout handling.
type concurrentToolExecutor struct {
	semaphore    chan struct{}
	queueTimeout time.Duration
}

// NewToolExecutor creates a new tool executor and returns it as the ToolExecutor interface.
// maxConcurrent: maximum number of tools running concurrently
// queueTimeoutSec: how long to wait for a slot when all slots are busy (0 = wait indefinitely)
func NewToolExecutor(maxConcurrent int, queueTimeoutSec int) ToolExecutor {
	return &concurrentToolExecutor{
		semaphore:    make(chan struct{}, maxConcurrent),
		queueTimeout: time.Duration(queueTimeoutSec) * time.Second,
	}
}

// Execute runs a tool with concurrency control.
// The tool is responsible for its own timeout handling.
func (e *concurrentToolExecutor) Execute(ctx context.Context, tool agentctx.Tool, args map[string]interface{}) ([]agentctx.ContentBlock, error) {
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
func DefaultExecutor() ToolExecutor {
	return NewToolExecutor(10, 60) // 10 concurrent, 60s queue timeout
}
