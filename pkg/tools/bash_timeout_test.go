package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
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

func TestBashToolRejectsBareCD(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := tool.Execute(ctx, map[string]any{"command": "cd /tmp"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not persist workspace")
	assert.Contains(t, err.Error(), "change_workspace")
}

func TestBashToolAllowsCommandLocalCD(t *testing.T) {
	ws, _ := NewWorkspace("/")
	tool := NewBashTool(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	blocks, err := tool.Execute(ctx, map[string]any{"command": "cd /tmp && pwd"})
	assert.NoError(t, err)
	assert.NotEmpty(t, blocks)
	result := blocks[0].(agentctx.TextContent)
	assert.Contains(t, result.Text, "/tmp")
}

func TestBashToolBlocksTmuxKillServer(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tests := []struct {
		name    string
		command string
		blocked bool
	}{
		{"kill-server basic", "tmux kill-server", true},
		{"kill-server with flags", "tmux kill-server 2>/dev/null || true", true},
		{"kill-server with comment", "# Kill any remaining tmux sessions\ntmux kill-server 2>/dev/null || true", true},
		{"kill-server with semicolon", "echo hi; tmux kill-server", true},
		{"kill-server with &&", "echo hi && tmux kill-server", true},
		{"kill-session specific", "tmux kill-session -t gen-001", false},
		{"kill-session multi", "tmux kill-session -t gen-001\ntmux kill-session -t gen-002", false},
		{"normal tmux", "tmux list-sessions", false},
		{"no tmux", "echo hello", false},
		{"kill-server in grep", "grep -r 'kill-server' file.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks, err := tool.Execute(ctx, map[string]any{"command": tt.command})
			if tt.blocked {
				assert.NoError(t, err)
				assert.NotEmpty(t, blocks)
				result := blocks[0].(agentctx.TextContent)
				assert.Contains(t, result.Text, "⛔ Blocked")
				assert.Contains(t, result.Text, "kill-server")
			} else if err == nil {
				// Either got a normal result or was allowed through
				for _, b := range blocks {
					if tc, ok := b.(agentctx.TextContent); ok {
						assert.NotContains(t, tc.Text, "⛔ Blocked")
					}
				}
			}
		})
	}
}

// TestBashToolIgnoresParentDeadline is a regression test for the bug
// introduced when Concurrency.ToolTimeout was wired up as an executor-level
// hard cap (PR #361): with toolTimeout set in config, every bash command was
// canceled at that deadline regardless of the per-call timeout parameter,
// because bash treated the parent deadline as a cancellation.
//
// The executor's toolTimeout is a safety net for tools without their own
// timeout handling; it must not override bash's own timeout (default 120s or
// the LLM's timeout: N override). Bash only reacts to real parent
// cancellation (session abort), not to a parent deadline.
func TestBashToolIgnoresParentDeadline(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	// Parent deadline (2s) fires before the command (4s) finishes and before
	// bash's own timeout (10s). Only real cancellation may kill the command.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	blocks, err := tool.Execute(ctx, map[string]any{
		"command": "sleep 4",
		"timeout": float64(10),
	})
	elapsed := time.Since(start)

	assert.NoError(t, err)
	if elapsed < 3*time.Second {
		t.Fatalf("command canceled by parent deadline after %v; want it to run its full 4s", elapsed)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("command took too long: %v", elapsed)
	}
	for _, b := range blocks {
		text, ok := b.(agentctx.TextContent)
		if !ok {
			continue
		}
		assert.NotContains(t, text.Text, "canceled", "command should not be canceled by parent deadline")
	}
}

// TestBashToolParentCancellationStillAborts pins the abort semantics that
// TestBashToolIgnoresParentDeadline relies on: a real parent cancellation
// (session abort) must still kill the command promptly, unlike a parent
// deadline which is ignored.
func TestBashToolParentCancellationStillAborts(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	done := make(chan struct{})
	var elapsed time.Duration
	var blocks []agentctx.ContentBlock
	var err error
	go func() {
		defer close(done)
		blocks, err = tool.Execute(ctx, map[string]any{
			"command": "sleep 4",
			"timeout": float64(10),
		})
		elapsed = time.Since(start)
	}()
	time.Sleep(1 * time.Second)
	cancel() // real abort
	<-done

	assert.NoError(t, err)
	if elapsed > 3*time.Second {
		t.Fatalf("command was not aborted promptly after parent cancel: %v", elapsed)
	}
	found := false
	for _, b := range blocks {
		text, ok := b.(agentctx.TextContent)
		if !ok {
			continue
		}
		if strings.Contains(text.Text, "canceled") {
			found = true
		}
	}
	assert.True(t, found, "expected a 'Command canceled' result")
}
