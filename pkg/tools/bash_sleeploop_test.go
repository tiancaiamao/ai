package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestHasSleepLoop covers the exact attack pattern from the 2026-07-17
// deadlock: `while kill -0 <pid>; do sleep 2; done` waited for an `ai serve`
// process that never exits, bypassing the single-sleep guard.
func TestHasSleepLoop(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{
			name:    "incident command: while kill -0 loop with sleep",
			command: "while kill -0 30281 2>/dev/null; do sleep 2; done",
			want:    true,
		},
		{
			name:    "while true with sleep",
			command: "while true; do sleep 1; done",
			want:    true,
		},
		{
			name:    "until loop with sleep",
			command: "until pgrep -x myproc; do sleep 5; done",
			want:    true,
		},
		{
			name:    "multiline while loop with sleep",
			command: "while kill -0 123 2>/dev/null\ndo\n  sleep 3\ndone",
			want:    true,
		},
		{
			name:    "for loop with sleep",
			command: "for i in 1 2 3; do sleep 2; done",
			want:    true,
		},
		{
			name:    "while loop with variable sleep duration",
			command: `while kill -0 "$pid" 2>/dev/null; do sleep "$interval"; done`,
			want:    true,
		},
		{
			name:    "while loop with decimal sleep",
			command: "while true; do sleep .5; done",
			want:    true,
		},
		{
			name:    "sleep after loop end is not a loop",
			command: "while read line; do echo \"$line\"; done < input; sleep 2",
			want:    false,
		},
		{
			name:    "plain bounded sleep allowed",
			command: "sleep 2",
			want:    false,
		},
		{
			name:    "compound with sleep but no loop",
			command: "echo done && sleep 5",
			want:    false,
		},
		{
			name:    "loop without sleep",
			command: "while true; do echo hi; done",
			want:    false,
		},
		{
			name:    "no loop no sleep",
			command: "ls -la",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasSleepLoop(tt.command); got != tt.want {
				t.Errorf("hasSleepLoop(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

func TestBashTool_SleepLoopBlocked(t *testing.T) {
	tool := NewBashTool(&Workspace{})
	args := map[string]any{"command": "while kill -0 30281 2>/dev/null; do sleep 2; done", "timeout": float64(10)}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute() returned unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("Execute() should return a result for a blocked sleep loop")
	}
	text, ok := result[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("Execute() result[0] should be TextContent, got %T", result[0])
	}
	if !strings.Contains(text.Text, "sleep inside a while/until loop") {
		t.Errorf("Execute() should block the sleep loop:\ngot: %s", text.Text)
	}
	if !strings.Contains(text.Text, "ai serve") {
		t.Errorf("block message should explain that ai serve processes never exit:\ngot: %s", text.Text)
	}
}

// TestBashTool_TimeoutClamped guards the millisecond-confusion bug: the agent
// passed timeout=120000 (meaning 120s) but the tool interprets seconds,
// yielding a 33-hour cap. The clamp must kick in and the warning must reach
// the model.
func TestBashTool_TimeoutClamped(t *testing.T) {
	old := maxBashTimeout
	maxBashTimeout = 2 * time.Second
	defer func() { maxBashTimeout = old }()

	tool := NewBashTool(&Workspace{})
	// timeout must be a float64: real tool calls parse JSON numbers as
	// float64 (this is exactly how the incident's 120000 ms-arg arrived).
	args := map[string]any{"command": "sleep 8", "timeout": float64(120000)}

	start := time.Now()
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute() returned unexpected error: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed > 20*time.Second {
		t.Fatalf("clamped timeout not applied: command ran %s (expected ~2s)", elapsed)
	}
	if len(result) == 0 {
		t.Fatal("Execute() should return a result")
	}
	text, ok := result[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("Execute() result[0] should be TextContent, got %T", result[0])
	}
	if !strings.Contains(text.Text, "timeout clamped") {
		t.Errorf("result should contain the clamp warning:\ngot: %s", text.Text)
	}
	if !strings.Contains(text.Text, "SECONDS") {
		t.Errorf("warning should mention that the unit is seconds:\ngot: %s", text.Text)
	}
}

// TestBashTool_TimeoutOverflowClamped guards the int64 overflow bypass:
// timeout values whose nanosecond conversion overflows time.Duration (e.g.
// 10^10 s) would wrap negative and skip the cap, falling through to the
// no-timeout branch.
func TestBashTool_TimeoutOverflowClamped(t *testing.T) {
	old := maxBashTimeout
	maxBashTimeout = 2 * time.Second
	defer func() { maxBashTimeout = old }()

	tool := NewBashTool(&Workspace{})
	args := map[string]any{"command": "sleep 8", "timeout": float64(10000000000)}

	start := time.Now()
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute() returned unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("overflowed timeout bypassed the cap: command ran %s (expected ~2s)", elapsed)
	}
	if len(result) == 0 {
		t.Fatal("Execute() should return a result")
	}
	text, ok := result[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("Execute() result[0] should be TextContent, got %T", result[0])
	}
	if !strings.Contains(text.Text, "timeout clamped") {
		t.Errorf("result should contain the clamp warning:\ngot: %s", text.Text)
	}
}

// TestBashTool_TimeoutWithinCapUnchanged verifies a normal timeout still
// applies without the clamp warning.
func TestBashTool_TimeoutWithinCapUnchanged(t *testing.T) {
	tool := NewBashTool(&Workspace{})
	args := map[string]any{"command": "sleep 5", "timeout": float64(1)}

	start := time.Now()
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute() returned unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("1s timeout not applied: ran %s", elapsed)
	}
	if len(result) == 0 {
		t.Fatal("Execute() should return a result")
	}
	text, ok := result[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("Execute() result[0] should be TextContent, got %T", result[0])
	}
	if strings.Contains(text.Text, "timeout clamped") {
		t.Errorf("no clamp warning expected for a timeout within the cap:\ngot: %s", text.Text)
	}
}
