package agent

import (
	"context"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/llm"
)

type delayTool struct {
	name  string
	delay time.Duration
}

func (t *delayTool) Name() string {
	return t.name
}

func (t *delayTool) Description() string {
	return "delay test tool"
}

func (t *delayTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
	}
}

func (t *delayTool) Execute(ctx context.Context, _ map[string]any) ([]ContentBlock, error) {
	select {
	case <-time.After(t.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []ContentBlock{TextContent{Type: "text", Text: t.name + " done"}}, nil
}

type contextMutationTool struct {
	name string
}

func (t *contextMutationTool) Name() string {
	return t.name
}

func (t *contextMutationTool) Description() string {
	return "context mutation test tool"
}

func (t *contextMutationTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
	}
}

func (t *contextMutationTool) Execute(ctx context.Context, _ map[string]any) ([]ContentBlock, error) {
	current := ToolExecutionAgentContext(ctx)
	if current != nil {
		current.Messages = append(current.Messages, NewUserMessage("mutated-by-tool"))
	}
	return []ContentBlock{TextContent{Type: "text", Text: "ok"}}, nil
}

func newLoopTestEventStream() *llm.EventStream[AgentEvent, []AgentMessage] {
	return llm.NewEventStream[AgentEvent, []AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []AgentMessage { return e.Messages },
	)
}

func TestExecuteToolCallsParallelFanInFanOut(t *testing.T) {
	assistant := NewAssistantMessage()
	assistant.Content = []ContentBlock{
		ToolCallContent{ID: "call-1", Type: "toolCall", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
		ToolCallContent{ID: "call-2", Type: "toolCall", Name: "grep", Arguments: map[string]any{"pattern": "abc"}},
	}

	tools := []Tool{
		&delayTool{name: "read", delay: 160 * time.Millisecond},
		&delayTool{name: "grep", delay: 160 * time.Millisecond},
	}

	start := time.Now()
	results := executeToolCalls(
		context.Background(),
		&AgentContext{},
		tools,
		nil, // allowedTools - nil means all tools allowed
		&assistant,
		newLoopTestEventStream(),
		nil,
		nil,
		DefaultToolOutputLimits(),
	)
	elapsed := time.Since(start)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ToolCallID != "call-1" || results[1].ToolCallID != "call-2" {
		t.Fatalf("expected results to preserve tool call order, got [%s, %s]", results[0].ToolCallID, results[1].ToolCallID)
	}

	if elapsed >= 300*time.Millisecond {
		t.Fatalf("expected parallel execution faster than serial sum, took %v", elapsed)
	}
}

func TestExecuteToolCallsPreservesOrderWithImmediateError(t *testing.T) {
	assistant := NewAssistantMessage()
	assistant.Content = []ContentBlock{
		ToolCallContent{ID: "call-1", Type: "toolCall", Name: "read", Arguments: map[string]any{}}, // invalid
		ToolCallContent{ID: "call-2", Type: "toolCall", Name: "grep", Arguments: map[string]any{"pattern": "abc"}},
	}

	tools := []Tool{
		&delayTool{name: "grep", delay: 20 * time.Millisecond},
	}

	results := executeToolCalls(
		context.Background(),
		&AgentContext{},
		tools,
		nil, // allowedTools - nil means all tools allowed
		&assistant,
		newLoopTestEventStream(),
		nil,
		nil,
		DefaultToolOutputLimits(),
	)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ToolCallID != "call-1" || results[1].ToolCallID != "call-2" {
		t.Fatalf("expected ordered results, got [%s, %s]", results[0].ToolCallID, results[1].ToolCallID)
	}
	if !results[0].IsError {
		t.Fatal("expected first result to be error for invalid args")
	}
	if results[1].IsError {
		t.Fatal("expected second result to succeed")
	}
}

func TestExecuteToolCallsInjectsCurrentAgentContext(t *testing.T) {
	assistant := NewAssistantMessage()
	assistant.Content = []ContentBlock{
		ToolCallContent{ID: "call-1", Type: "toolCall", Name: "mutate", Arguments: map[string]any{}},
	}
	loopCtx := &AgentContext{
		Messages: []AgentMessage{NewUserMessage("before")},
	}
	staleCtx := &AgentContext{
		Messages: []AgentMessage{NewUserMessage("stale")},
	}

	tools := []Tool{
		&contextMutationTool{name: "mutate"},
	}

	results := executeToolCalls(
		context.Background(),
		loopCtx,
		tools,
		nil,
		&assistant,
		newLoopTestEventStream(),
		nil,
		nil,
		DefaultToolOutputLimits(),
	)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(loopCtx.Messages) != 2 {
		t.Fatalf("expected loop context to be mutated, got %d messages", len(loopCtx.Messages))
	}
	if len(staleCtx.Messages) != 1 {
		t.Fatalf("expected stale context to remain unchanged, got %d messages", len(staleCtx.Messages))
	}
}
