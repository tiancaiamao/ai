package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/stretchr/testify/assert"
)

func TestBashToolAsyncExecution(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	// Test 1: Fast command (completes before timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	args := map[string]any{
		"command": "echo 'hello world'",
		"timeout": float64(60), // 60 seconds timeout
	}

	blocks, err := tool.Execute(ctx, args)
	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)
	result := blocks[0].(agentctx.TextContent)
	assert.Contains(t, result.Text, "hello world")
}

func TestBashToolTimeoutContinuesRunning(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	// Test 2: Long command that times out
	// Use sh -c 'echo start; sleep 5; echo done' with 1s timeout
	args := map[string]any{
		"command": "sh -c 'echo start; sleep 5; echo done'",
		"timeout": float64(1), // 1 second timeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	blocks, err := tool.Execute(ctx, args)
	assert.NoError(t, err) // Should not error, but return timeout result
	assert.NotEmpty(t, blocks)
	result := blocks[0].(agentctx.TextContent)

	// Check that we got timeout info
	assert.Contains(t, result.Text, "timed out")
	assert.Contains(t, result.Text, "still running in background")
	assert.Contains(t, result.Text, "PGID:")

	// Extract command ID from the new format: "command_status id=cmd-..."
	cmdID := ""
	text := result.Text
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.Contains(line, "command_status id=") {
			// Extract ID from format like "  • command_status id=cmd-123456  - Check current status"
			parts := strings.Fields(line)
			for _, part := range parts {
				if strings.HasPrefix(part, "id=cmd-") {
					cmdID = strings.TrimPrefix(part, "id=")
					break
				}
			}
			break
		}
	}
	assert.NotEmpty(t, cmdID)

	// Check if command is still running (should still be running since sleep 5 > timeout 1s)
	statusTool := NewCommandStatusTool()
	statusArgs := map[string]any{
		"command_id": cmdID,
	}
	statusBlocks, err := statusTool.Execute(ctx, statusArgs)
	assert.NoError(t, err)
	assert.NotEmpty(t, statusBlocks)
	statusResult := statusBlocks[0].(agentctx.TextContent)
	assert.Contains(t, statusResult.Text, "○ Running") // Should still be running
}

func TestBashToolNoTimeout(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	// Test 3: timeout=0 means wait indefinitely (command completes instantly)
	args := map[string]any{
		"command": "echo 'test'",
		"timeout": float64(0), // No timeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	blocks, err := tool.Execute(ctx, args)
	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)
	result := blocks[0].(agentctx.TextContent)
	assert.Contains(t, result.Text, "test")
}

func TestCommandStatusTool(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)
	statusTool := NewCommandStatusTool()

	// Start a command that will timeout
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	args := map[string]any{
		"command": "echo 'async test'; sleep 3",
		"timeout": float64(1), // 1 second timeout
	}

	blocks, err := tool.Execute(ctx, args)
	assert.NoError(t, err)

	// Extract command ID from the new format
	cmdID := ""
	text := blocks[0].(agentctx.TextContent).Text
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if strings.Contains(line, "command_status id=") {
			parts := strings.Fields(line)
			for _, part := range parts {
				if strings.HasPrefix(part, "id=cmd-") {
					cmdID = strings.TrimPrefix(part, "id=")
					break
				}
			}
			break
		}
	}
	assert.NotEmpty(t, cmdID)

	// Check status
	statusArgs := map[string]any{
		"command_id": cmdID,
	}
	statusBlocks, err := statusTool.Execute(ctx, statusArgs)
	assert.NoError(t, err)
	assert.NotEmpty(t, statusBlocks)
	statusResult := statusBlocks[0].(agentctx.TextContent)

	// Check that status contains expected fields
	assert.Contains(t, statusResult.Text, "Command Status")
	assert.Contains(t, statusResult.Text, "Status:")
	assert.Contains(t, statusResult.Text, "PID:")
	assert.Contains(t, statusResult.Text, "PGID:")
	assert.Contains(t, statusResult.Text, "Elapsed time:")
}

func TestBashToolHandlesLargeStderrOutput(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	// This writes >64KB to stderr quickly; bash tool must still complete.
	args := map[string]any{
		"command": `i=0; while [ $i -lt 20000 ]; do echo "err-$i" 1>&2; i=$((i+1)); done; echo done`,
		"timeout": float64(5),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	blocks, err := tool.Execute(ctx, args)
	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)

	result := blocks[0].(agentctx.TextContent)
	assert.Contains(t, result.Text, "done")
	assert.NotContains(t, result.Text, "timed out")
}
