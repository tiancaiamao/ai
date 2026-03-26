package tools

import (
	"context"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestTaskTrackingSkipAcceptsStringTrue(t *testing.T) {
	tool := NewTaskTrackingTool()
	agentContext := &agentctx.AgentContext{
		TaskTrackingState: agentctx.NewTaskTrackingState(t.TempDir()),
	}
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentContext)

	blocks, err := tool.Execute(ctx, map[string]any{
		"skip":      "true",
		"reasoning": "no state change",
	})
	if err != nil {
		t.Fatalf("expected no error for skip=\"true\", got %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected non-empty result blocks")
	}
	text, ok := blocks[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", blocks[0])
	}
	if !strings.Contains(text.Text, "Task tracking skipped") {
		t.Fatalf("unexpected response: %q", text.Text)
	}
}

func TestTaskTrackingSkipInvalidStringReturnsError(t *testing.T) {
	tool := NewTaskTrackingTool()
	agentContext := &agentctx.AgentContext{
		TaskTrackingState: agentctx.NewTaskTrackingState(t.TempDir()),
	}
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentContext)

	_, err := tool.Execute(ctx, map[string]any{
		"skip":      "maybe",
		"reasoning": "no state change",
	})
	if err == nil {
		t.Fatal("expected error for invalid skip string")
	}
	if !strings.Contains(err.Error(), "skip parameter") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTaskTrackingSkipFalseRequiresContent(t *testing.T) {
	tool := NewTaskTrackingTool()
	agentContext := &agentctx.AgentContext{
		TaskTrackingState: agentctx.NewTaskTrackingState(t.TempDir()),
	}
	ctx := agentctx.WithToolExecutionAgentContext(context.Background(), agentContext)

	_, err := tool.Execute(ctx, map[string]any{
		"skip": false,
	})
	if err == nil {
		t.Fatal("expected error when skip=false without content")
	}
	if !strings.Contains(err.Error(), "content parameter is required when not skipping") {
		t.Fatalf("unexpected error: %v", err)
	}
}
