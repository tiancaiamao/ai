package agent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// ToolExecutor manages concurrent tool execution with limits.
type ToolExecutor struct {
	semaphore    chan struct{}
	toolTimeout  time.Duration
	queueTimeout time.Duration
}

// NewToolExecutor creates a new tool executor.
func NewToolExecutor(maxConcurrent int, toolTimeoutSec, queueTimeoutSec int) *ToolExecutor {
	return &ToolExecutor{
		semaphore:    make(chan struct{}, maxConcurrent),
		toolTimeout:  time.Duration(toolTimeoutSec) * time.Second,
		queueTimeout: time.Duration(queueTimeoutSec) * time.Second,
	}
}

// Execute runs a tool with concurrency control and timeout.
func (e *ToolExecutor) Execute(ctx context.Context, tool Tool, args map[string]interface{}) ([]ContentBlock, error) {
	// Try to acquire semaphore (slot for execution)
	select {
	case e.semaphore <- struct{}{}:
		// Got slot, execute tool
		defer func() { <-e.semaphore }()

		// Create timeout context
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
			return result.content, result.err
		case <-timeoutCtx.Done():
			return nil, fmt.Errorf("tool execution timeout after %v", e.toolTimeout)
		}

	case <-time.After(e.queueTimeout):
		// Queue timeout
		return nil, fmt.Errorf("tool queue full, timeout after %v", e.queueTimeout)

	case <-ctx.Done():
		// Context cancelled
		return nil, ctx.Err()
	}
}

// toolResult wraps tool execution result.
type toolResult struct {
	content []ContentBlock
	err     error
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

// Execute runs a tool using the appropriate executor.
func (p *ExecutorPool) Execute(ctx context.Context, tool Tool, args map[string]interface{}) ([]ContentBlock, error) {
	executor := p.GetExecutor(tool.Name())
	log.Printf("[Executor] Executing tool '%s' (concurrency limit: %d)",
		tool.Name(), cap(executor.semaphore))

	return executor.Execute(ctx, tool, args)
}
