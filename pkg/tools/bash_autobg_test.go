package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
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

	// Capture trace markers: without them the trace shows the tool call
	// ending while the command is still running, which reads as a hang.
	traceevent.EnableEvent("background_op")
	tb := traceevent.NewTraceBuf()
	var traceMu sync.Mutex
	var markers []traceevent.TraceEvent
	tb.AddSink(func(e traceevent.TraceEvent) {
		if e.Name != "background_op" {
			return
		}
		traceMu.Lock()
		markers = append(markers, e)
		traceMu.Unlock()
	})
	traceevent.SetActiveTraceBuf(tb)
	defer traceevent.ClearActiveTraceBuf(tb)

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

	// The operation must be traced from detach to completion, so its lifetime
	// is visible instead of inferred from a finished tool call.
	traceMu.Lock()
	got := append([]traceevent.TraceEvent{}, markers...)
	traceMu.Unlock()
	if len(got) != 2 {
		t.Fatalf("background_op markers = %d, want 2 (start, end)", len(got))
	}
	var phases []string
	for _, m := range got {
		for _, f := range m.Fields {
			if f.Key == "phase" {
				phases = append(phases, fmt.Sprint(f.Value))
			}
		}
	}
	if len(phases) != 2 || phases[0] != "start" || phases[1] != "end" {
		t.Fatalf("background_op phases = %v, want [start end]", phases)
	}
	if !got[1].Timestamp.After(got[0].Timestamp) {
		t.Errorf("end marker %v does not follow start marker %v", got[1].Timestamp, got[0].Timestamp)
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
