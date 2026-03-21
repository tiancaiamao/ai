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
	assert.Contains(t, result.Text, "tmux") // Points to tmux skill for long-running tasks
}

func TestBashToolLargeSingleLineOutput(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	// Create a file with a very large single line (larger than Scanner's default 64KB limit)
	// This tests that bufio.Reader.ReadString handles large lines without hanging
	cmd := `dd if=/dev/zero bs=1M count=10 2>/dev/null | head -c 10485760 | tr '\0' 'x' > /tmp/large_single_line.txt && wc -c /tmp/large_single_line.txt`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// First create the file
	_, err := tool.Execute(ctx, map[string]any{"command": cmd, "timeout": float64(5)})
	assert.NoError(t, err)

	// Now cat the large single-line file - should complete quickly without timeout
	args := map[string]any{
		"command": "cat /tmp/large_single_line.txt | head -1",
		"timeout": float64(5),
	}

	start := time.Now()
	blocks, err := tool.Execute(ctx, args)
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)

	result := blocks[0].(agentctx.TextContent)
	// Should NOT contain timeout message
	assert.NotContains(t, result.Text, "timed out")
	assert.NotContains(t, result.Text, "was terminated")
	// Should contain the large output
	assert.Greater(t, len(result.Text), 1000000)

	// Should complete in reasonable time (< 2 seconds for 10MB)
	assert.Less(t, elapsed.Milliseconds(), int64(2000), "Large file processing should be fast")

	// Cleanup
	tool.Execute(ctx, map[string]any{"command": "rm -f /tmp/large_single_line.txt", "timeout": float64(1)})
}