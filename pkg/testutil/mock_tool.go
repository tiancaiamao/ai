// Package testutil provides shared test infrastructure for the ai project.
//
// It consolidates mock types, event collection helpers, and SSE server builders
// that were previously duplicated across individual test files.
package testutil

import (
	"context"
	"fmt"
	"sync"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// MockTool is a configurable mock implementing agentctx.Tool.
// Each method can be overridden by setting the corresponding Func field.
// If a Func is nil, a sensible default is used.
type MockTool struct {
	NameFunc    func() string
	DescFunc    func() string
	ParamsFunc  func() map[string]any
	ExecuteFunc func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error)

	mu        sync.Mutex
	callCount int
}

// Name returns the tool name.
func (m *MockTool) Name() string {
	if m.NameFunc != nil {
		return m.NameFunc()
	}
	return "mock_tool"
}

// Description returns the tool description.
func (m *MockTool) Description() string {
	if m.DescFunc != nil {
		return m.DescFunc()
	}
	return "mock tool for testing"
}

// Parameters returns the tool parameter schema.
func (m *MockTool) Parameters() map[string]any {
	if m.ParamsFunc != nil {
		return m.ParamsFunc()
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type":        "string",
				"description": "Input parameter",
			},
		},
	}
}

// Execute runs the mock tool and increments the call counter.
func (m *MockTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()

	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(ctx, args)
	}
	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "mock result"},
	}, nil
}

// CallCount returns the number of times Execute was called.
func (m *MockTool) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// EchoTool returns a mock tool that echoes its "input" argument as text.
func EchoTool(name string) *MockTool {
	return &MockTool{
		NameFunc: func() string { return name },
		ExecuteFunc: func(_ context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
			text := fmt.Sprintf("%v", args["input"])
			return []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: text},
			}, nil
		},
	}
}

// SlowTool returns a mock tool that sleeps for the given duration before responding.
func SlowTool(name string, delay time.Duration) *MockTool {
	return &MockTool{
		NameFunc: func() string { return name },
		ExecuteFunc: func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("%s: slow result", name)},
			}, nil
		},
	}
}

// FailingTool returns a mock tool that always returns the given error.
func FailingTool(name string, err error) *MockTool {
	return &MockTool{
		NameFunc:    func() string { return name },
		ExecuteFunc: func(_ context.Context, _ map[string]any) ([]agentctx.ContentBlock, error) { return nil, err },
	}
}

// CountingTool returns a mock tool that succeeds for the first succeedCount calls,
// then fails with the given error. Returns total call count via CallCount().
func CountingTool(name string, succeedCount int, err error) *MockTool {
	var mu sync.Mutex
	count := 0
	return &MockTool{
		NameFunc: func() string { return name },
		ExecuteFunc: func(_ context.Context, _ map[string]any) ([]agentctx.ContentBlock, error) {
			mu.Lock()
			count++
			shouldFail := count > succeedCount
			mu.Unlock()

			if shouldFail {
				return nil, err
			}
			return []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("%s: call %d", name, count)},
			}, nil
		},
	}
}