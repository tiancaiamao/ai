package tools

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestBashToolAutoBackground verifies that a command still running after the
// background threshold is detached into a background operation: Execute
// returns a handle promptly (~threshold) instead of blocking for the whole
// command, and the completion notifier fires with the full output once the
// command finishes.
func TestBashToolAutoBackground(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	notified := make(chan string, 1)
	tool.SetBackgroundNotifier(func(opID, result string) {
		notified <- opID + "\x00" + result
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	blocks, err := tool.Execute(ctx, map[string]any{
		"command": "sleep 7 && echo bg-finished",
		"timeout": float64(30),
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("Execute() returned no blocks")
	}
	handle, ok := blocks[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("result[0] should be TextContent, got %T", blocks[0])
	}
	if !strings.Contains(handle.Text, "background operation") {
		t.Fatalf("expected a background handle, got: %s", handle.Text)
	}
	if elapsed < 4*time.Second {
		t.Fatalf("returned after %s; expected to wait until the ~5s threshold", elapsed)
	}
	if elapsed > 6500*time.Millisecond {
		t.Fatalf("Execute blocked for %s; should detach at the ~5s threshold", elapsed)
	}

	// The completion notification must arrive once the command actually
	// finishes, carrying the command's stdout.
	select {
	case n := <-notified:
		parts := strings.SplitN(n, "\x00", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "op-") {
			t.Fatalf("unexpected notification shape: %q", n)
		}
		if !strings.Contains(parts[1], "bg-finished") {
			t.Fatalf("notification missing command output: %q", parts[1])
		}
	case <-time.After(10 * time.Second):
		t.Fatal("background completion notification not received")
	}
}

// TestBashToolFastCommandStaysSync verifies the common case is unchanged: a
// command that finishes well within the threshold returns its output directly
// and never fires the background notifier.
func TestBashToolFastCommandStaysSync(t *testing.T) {
	ws, _ := NewWorkspace("/tmp")
	tool := NewBashTool(ws)

	var notified atomic.Int32
	tool.SetBackgroundNotifier(func(opID, result string) {
		notified.Add(1)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	blocks, err := tool.Execute(ctx, map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	text := blocks[0].(agentctx.TextContent).Text
	if !strings.Contains(text, "hi") {
		t.Fatalf("expected command output, got: %s", text)
	}
	if strings.Contains(text, "background operation") {
		t.Fatalf("fast command must not be backgrounded: %s", text)
	}
	// Give any stray background goroutine a moment; it must never fire.
	time.Sleep(200 * time.Millisecond)
	if n := notified.Load(); n != 0 {
		t.Fatalf("background notifier fired %d time(s) for a fast command", n)
	}
}
