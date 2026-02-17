package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// mockRunSubagent is a mock implementation of RunSubagentFunc
func mockRunSubagent(ctx context.Context, task string, allowedTools []string, maxTurns int) (string, error) {
	// Simulate some work
	time.Sleep(10 * time.Millisecond)
	return "Mock result for: " + task, nil
}

func TestSubagentTool_Name(t *testing.T) {
	tool := NewSubagentTool("/tmp", nil, nil, mockRunSubagent)
	if tool.Name() != "subagent" {
		t.Errorf("Expected name 'subagent', got '%s'", tool.Name())
	}
}

func TestSubagentTool_Description(t *testing.T) {
	tool := NewSubagentTool("/tmp", nil, nil, mockRunSubagent)
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
	// Should mention key capabilities (case-insensitive)
	descLower := strings.ToLower(desc)
	if !containsAll(descLower, []string{"subagent", "task", "parallel"}) {
		t.Errorf("Description should mention subagent, task, and parallel capabilities")
	}
}

func TestSubagentTool_Parameters(t *testing.T) {
	tool := NewSubagentTool("/tmp", nil, nil, mockRunSubagent)
	params := tool.Parameters()

	// params is map[string]any
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters should have properties")
	}

	// Should have 'task' property
	if _, ok := props["task"]; !ok {
		t.Error("Parameters should have 'task' property")
	}

	// Should have 'tasks' property for parallel execution
	if _, ok := props["tasks"]; !ok {
		t.Error("Parameters should have 'tasks' property")
	}

	// Should have 'config' property
	if _, ok := props["config"]; !ok {
		t.Error("Parameters should have 'config' property")
	}
}

func TestSubagentTool_Execute_SingleTask(t *testing.T) {
	tool := NewSubagentTool("/tmp", nil, nil, mockRunSubagent)
	ctx := context.Background()

	args := map[string]any{
		"task": "Say hello",
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("Result should not be empty")
	}

	textContent, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("Result should be TextContent")
	}

	if textContent.Text == "" {
		t.Error("Result text should not be empty")
	}
}

func TestSubagentTool_Execute_ParallelTasks(t *testing.T) {
	tool := NewSubagentTool("/tmp", nil, nil, mockRunSubagent)
	ctx := context.Background()

	args := map[string]any{
		"tasks": []any{"Task 1", "Task 2", "Task 3"},
	}

	start := time.Now()
	result, err := tool.Execute(ctx, args)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("Result should not be empty")
	}

	textContent, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("Result should be TextContent")
	}

	// Should contain results for all tasks
	if !containsAll(textContent.Text, []string{"Task 1", "Task 2", "Task 3"}) {
		t.Errorf("Result should contain all task results, got: %s", textContent.Text)
	}

	// Parallel execution should be faster than sequential
	// 3 tasks * 10ms = 30ms sequential, but parallel should be ~10-30ms
	if elapsed > 100*time.Millisecond {
		t.Errorf("Parallel execution took too long: %v", elapsed)
	}
}

func TestSubagentTool_Execute_NoTask(t *testing.T) {
	tool := NewSubagentTool("/tmp", nil, nil, mockRunSubagent)
	ctx := context.Background()

	args := map[string]any{}

	_, err := tool.Execute(ctx, args)
	if err == nil {
		t.Error("Should error when no task provided")
	}
}

func TestSubagentTool_Execute_BothTaskAndTasks(t *testing.T) {
	tool := NewSubagentTool("/tmp", nil, nil, mockRunSubagent)
	ctx := context.Background()

	args := map[string]any{
		"task":  "Single task",
		"tasks": []any{"Task 1", "Task 2"},
	}

	_, err := tool.Execute(ctx, args)
	if err == nil {
		t.Error("Should error when both task and tasks provided")
	}
}

func TestSubagentTool_Execute_WithConfig(t *testing.T) {
	tool := NewSubagentTool("/tmp", nil, nil, mockRunSubagent)
	ctx := context.Background()

	args := map[string]any{
		"task": "Say hello",
		"config": map[string]any{
			"tools":     []any{"read", "write"},
			"max_turns": 5,
			"timeout":   60,
		},
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("Result should not be empty")
	}
}

func TestSubagentTool_Execute_NilRunner(t *testing.T) {
	tool := NewSubagentTool("/tmp", nil, nil, nil) // nil runner
	ctx := context.Background()

	args := map[string]any{
		"task": "Say hello",
	}

	result, err := tool.Execute(ctx, args)
	// Execute succeeds, but result contains error message
	if err != nil {
		t.Fatalf("Execute should not return error, got: %v", err)
	}

	textContent, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("Result should be TextContent")
	}

	// Result should contain error message about nil runner
	if !containsAll(textContent.Text, []string{"failed", "not configured"}) {
		t.Errorf("Result should indicate failure, got: %s", textContent.Text)
	}
}

func TestSubagentTool_Execute_ContextCancellation(t *testing.T) {
	slowRunner := func(ctx context.Context, task string, allowedTools []string, maxTurns int) (string, error) {
		select {
		case <-time.After(5 * time.Second):
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	tool := NewSubagentTool("/tmp", nil, nil, slowRunner)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	args := map[string]any{
		"task": "Slow task",
	}

	start := time.Now()
	result, err := tool.Execute(ctx, args)
	elapsed := time.Since(start)

	// Execute should succeed (returns result even on failure)
	if err != nil {
		t.Fatalf("Execute should not return error, got: %v", err)
	}

	// Should timeout quickly, not wait 5 seconds
	if elapsed > 200*time.Millisecond {
		t.Errorf("Should have timed out quickly, took %v", elapsed)
	}

	// Result should contain error
	textContent, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("Result should be TextContent")
	}
	if !containsAll(textContent.Text, []string{"failed"}) {
		t.Errorf("Result should indicate failure, got: %s", textContent.Text)
	}
}

// Helper function
func containsAll(s string, substrs []string) bool {
	for _, substr := range substrs {
		if !contains(s, substr) {
			return false
		}
	}
	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}