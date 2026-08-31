package tools

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestBashToolBackgroundProcessDoesNotHang verifies that a command which
// backgrounds a process holding the stdout/stderr pipe returns promptly
// after the main shell exits, instead of blocking until the background
// process releases the pipe.
//
// Regression test for the 20.6h tool hang: `sleep 5 & echo done` leaves a
// descendant holding the pipe write ends for 5s after the shell exits, so
// EOF never arrives. Before the idle-grace drain fix, outputWG.Wait()
// blocked until the background process exited (~5s) and the 120s default
// timeout was never reached because the deadline check runs after the drain.
func TestBashToolBackgroundProcessDoesNotHang(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	blocks, err := tool.Execute(ctx, map[string]any{"command": "sleep 5 & echo done"})
	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)
	result := blocks[0].(agentctx.TextContent)
	assert.Contains(t, result.Text, "done")
	assert.NotContains(t, result.Text, "timed out")
	// Drain grace is 250ms; allow generous CI margin but stay well below the
	// 5s the background sleep would otherwise hold the pipe open.
	assert.Less(t, elapsed, 4*time.Second,
		"tool should return once the main shell exits, not wait for the background process")
}

// TestBashToolBackgroundOutputNotTruncated verifies that output still being
// written shortly after the main process exits is not truncated by the
// idle-grace drain: the grace timer re-arms on each chunk. The tail write
// must land within the 250ms grace window (a quieter background process is
// best-effort by design, matching the pi project's behavior).
func TestBashToolBackgroundOutputNotTruncated(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The background job writes a line 100ms after the main shell exits; the
	// re-armed idle timer must keep reading until it lands.
	cmd := "( sleep 0.1; echo tail-line ) & echo head-line"
	blocks, err := tool.Execute(ctx, map[string]any{"command": cmd})
	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)
	result := blocks[0].(agentctx.TextContent)
	assert.Contains(t, result.Text, "head-line")
	assert.Contains(t, result.Text, "tail-line")
}
