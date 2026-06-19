package config

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
)

func TestToLoopConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg := &Config{}
		lc := cfg.ToLoopConfig()
		if lc == nil {
			t.Fatal("expected non-nil LoopConfig")
		}
	})

	t.Run("with concurrency", func(t *testing.T) {
		cfg := &Config{
			Concurrency: &ConcurrencyConfig{MaxConcurrentTools: 4, QueueTimeout: 30},
		}
		lc := cfg.ToLoopConfig()
		if lc.Executor == nil {
			t.Error("expected non-nil Executor")
		}
	})

	t.Run("with tool output", func(t *testing.T) {
		cfg := &Config{
			ToolOutput: &ToolOutputConfig{MaxChars: 5000},
		}
		lc := cfg.ToLoopConfig()
		if lc.ToolOutput.MaxChars != 5000 {
			t.Errorf("MaxChars = %d, want 5000", lc.ToolOutput.MaxChars)
		}
	})

	t.Run("options applied", func(t *testing.T) {
		cfg := &Config{}
		lc := cfg.ToLoopConfig(
			WithContextWindow(128000),
			WithToolCallCutoff(10),
			WithToolOutputLimits(agent.ToolOutputLimits{MaxChars: 3000}),
		)
		if lc.ContextWindow != 128000 {
			t.Errorf("ContextWindow = %d, want 128000", lc.ContextWindow)
		}
		if lc.ToolCallCutoff != 10 {
			t.Errorf("ToolCallCutoff = %d, want 10", lc.ToolCallCutoff)
		}
		if lc.ToolOutput.MaxChars != 3000 {
			t.Errorf("ToolOutput.MaxChars = %d, want 3000", lc.ToolOutput.MaxChars)
		}
	})
}

func TestWithCompactors(t *testing.T) {
	cfg := &Config{}
	lc := cfg.ToLoopConfig(WithCompactors(nil))
	if lc.Compactors != nil {
		t.Error("expected nil compactors")
	}
}

func TestWithExecutor(t *testing.T) {
	exec := agent.NewToolExecutor(2, 10)
	cfg := &Config{}
	lc := cfg.ToLoopConfig(WithExecutor(exec))
	if lc.Executor == nil {
		t.Error("expected non-nil Executor")
	}
}
