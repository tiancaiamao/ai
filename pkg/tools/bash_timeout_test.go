package tools

import (
	"context"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/stretchr/testify/assert"
)

func TestBashToolTimeoutParameter(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	// Test 1: Default timeout (120 seconds)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	args := map[string]any{
		"command": "echo 'test'",
	}

	blocks, err := tool.Execute(ctx, args)
	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)

	// Test 2: Custom timeout parameter
	argsWithTimeout := map[string]any{
		"command": "echo 'test'",
		"timeout": float64(60), // 60 seconds
	}

	blocks, err = tool.Execute(ctx, argsWithTimeout)
	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)

	// Test 3: Timeout parameter with 0 (no timeout)
	argsNoTimeout := map[string]any{
		"command": "echo 'test'",
		"timeout": float64(0),
	}

	blocks, err = tool.Execute(ctx, argsNoTimeout)
	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)
}

func TestBashToolTimeoutDetection(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	// Test 4: Command that times out
	// Use sleep 3 with 1 second timeout for faster test
	argsTimeout := map[string]any{
		"command": "sleep 3",
		"timeout": float64(1), // 1 second timeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	blocks, err := tool.Execute(ctx, argsTimeout)
	assert.NoError(t, err) // Should not error, but return timeout result
	assert.NotEmpty(t, blocks)

	// Check that the result contains timeout information
	result := blocks[0].(agentctx.TextContent)
	assert.Contains(t, result.Text, "timed out")
	assert.Contains(t, result.Text, "Retry") // Capital R in "Retry with a longer timeout"
}