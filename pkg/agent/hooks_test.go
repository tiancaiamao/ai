package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// ---- L2-1: HookRegistry nil — all Run* methods return zero-value, no panic ----

func TestHookRegistryNil(t *testing.T) {
	var r *HookRegistry // nil

	hctx := HookContext{
		Ctx:      context.Background(),
		AgentCtx: &agentctx.AgentContext{},
		Config:   &LoopConfig{},
	}

	// RunBeforeModel on nil should return 0
	n := r.RunBeforeModel(hctx, nil)
	if n != 0 {
		t.Fatalf("RunBeforeModel on nil HookRegistry returned %d, want 0", n)
	}

	// RunAfterTool on nil should return input unchanged
	input := agentctx.AgentMessage{Role: "toolResult", ToolName: "bash"}
	out := r.RunAfterTool(hctx, "bash", input)
	if out.ToolName != "bash" {
		t.Fatalf("RunAfterTool on nil HookRegistry modified input")
	}

	// RunAfterAgent on nil should be no-op (no panic)
	r.RunAfterAgent(hctx)
}

// ---- L2-2: Hook returns error → slog.Warn, loop continues ----

func TestHookError(t *testing.T) {
	t.Run("BeforeModel", func(t *testing.T) {
		var called atomic.Int32
		r := &HookRegistry{
			BeforeModelHooks: []BeforeModelHook{
				// First hook errors
				func(hctx HookContext, msgs []agentctx.AgentMessage) ([]agentctx.AgentMessage, error) {
					called.Add(1)
					return nil, errors.New("boom")
				},
				// Second hook should still be called
				func(hctx HookContext, msgs []agentctx.AgentMessage) ([]agentctx.AgentMessage, error) {
					called.Add(1)
					return []agentctx.AgentMessage{{Role: "framework"}}, nil
				},
			},
		}
		agentCtx := &agentctx.AgentContext{}
		hctx := HookContext{
			Ctx:      context.Background(),
			AgentCtx: agentCtx,
			Config:   &LoopConfig{},
		}
		n := r.RunBeforeModel(hctx, nil)
		if called.Load() != 2 {
			t.Fatalf("expected both hooks called, got %d", called.Load())
		}
		if n != 1 {
			t.Fatalf("expected 1 injected message from second hook, got %d", n)
		}
	})

	t.Run("AfterTool", func(t *testing.T) {
		r := &HookRegistry{
			AfterToolHooks: []AfterToolHook{
				func(hctx HookContext, toolName string, result agentctx.AgentMessage) (agentctx.AgentMessage, error) {
					return agentctx.AgentMessage{}, errors.New("boom")
				},
				// Second hook should still be called, but receives ORIGINAL result
				// because first hook errored (chain skips errored hook)
				func(hctx HookContext, toolName string, result agentctx.AgentMessage) (agentctx.AgentMessage, error) {
					result.ToolName = "modified"
					return result, nil
				},
			},
		}
		agentCtx := &agentctx.AgentContext{}
		hctx := HookContext{
			Ctx:      context.Background(),
			AgentCtx: agentCtx,
			Config:   &LoopConfig{},
		}
		input := agentctx.AgentMessage{Role: "toolResult", ToolName: "bash"}
		out := r.RunAfterTool(hctx, "bash", input)
		if out.ToolName != "modified" {
			t.Fatalf("expected second hook to modify result, got ToolName=%q", out.ToolName)
		}
	})

	t.Run("AfterAgent", func(t *testing.T) {
		var called atomic.Int32
		r := &HookRegistry{
			AfterAgentHooks: []AfterAgentHook{
				func(hctx HookContext) { called.Add(1) },
				func(hctx HookContext) { called.Add(1) },
			},
		}
		hctx := HookContext{
			Ctx:      context.Background(),
			AgentCtx: &agentctx.AgentContext{},
			Config:   &LoopConfig{},
		}
		r.RunAfterAgent(hctx)
		if called.Load() != 2 {
			t.Fatalf("expected both AfterAgent hooks called, got %d", called.Load())
		}
	})
}

// ---- L2-3: BeforeModel hook returned messages appended to RecentMessages ----

func TestBeforeModelInjection(t *testing.T) {
	r := &HookRegistry{
		BeforeModelHooks: []BeforeModelHook{
			func(hctx HookContext, msgs []agentctx.AgentMessage) ([]agentctx.AgentMessage, error) {
				return []agentctx.AgentMessage{
					{Role: "framework", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "injected"}}},
				}, nil
			},
		},
	}
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{{Role: "user"}},
	}
	hctx := HookContext{
		Ctx:      context.Background(),
		AgentCtx: agentCtx,
		Config:   &LoopConfig{},
	}

	n := r.RunBeforeModel(hctx, agentCtx.RecentMessages)
	if n != 1 {
		t.Fatalf("expected 1 injected message, got %d", n)
	}
	if len(agentCtx.RecentMessages) != 2 {
		t.Fatalf("expected 2 messages in RecentMessages, got %d", len(agentCtx.RecentMessages))
	}
	last := agentCtx.RecentMessages[1]
	if last.Role != "framework" {
		t.Fatalf("expected injected message Role=framework, got %q", last.Role)
	}
}

// ---- L2-4: AfterTool hook chain execution ----

func TestAfterToolChain(t *testing.T) {
	var order []int
	r := &HookRegistry{
		AfterToolHooks: []AfterToolHook{
			func(hctx HookContext, toolName string, result agentctx.AgentMessage) (agentctx.AgentMessage, error) {
				order = append(order, 1)
				result.ToolName = result.ToolName + "_first"
				return result, nil
			},
			func(hctx HookContext, toolName string, result agentctx.AgentMessage) (agentctx.AgentMessage, error) {
				order = append(order, 2)
				// Verify we receive the output of the first hook
				if !strings.HasSuffix(result.ToolName, "_first") {
					t.Fatalf("second hook did not receive first hook's output, got %q", result.ToolName)
				}
				result.ToolName = result.ToolName + "_second"
				return result, nil
			},
		},
	}
	hctx := HookContext{
		Ctx:      context.Background(),
		AgentCtx: &agentctx.AgentContext{},
		Config:   &LoopConfig{},
	}

	input := agentctx.AgentMessage{Role: "toolResult", ToolName: "bash"}
	out := r.RunAfterTool(hctx, "bash", input)

	if out.ToolName != "bash_first_second" {
		t.Fatalf("expected chain output 'bash_first_second', got %q", out.ToolName)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("expected execution order [1,2], got %v", order)
	}
}

// ---- L2-5: AfterAgent hook sequential execution ----

func TestAfterAgentOrder(t *testing.T) {
	var order []int
	r := &HookRegistry{
		AfterAgentHooks: []AfterAgentHook{
			func(hctx HookContext) { order = append(order, 1) },
			func(hctx HookContext) { order = append(order, 2) },
			func(hctx HookContext) { order = append(order, 3) },
		},
	}
	hctx := HookContext{
		Ctx:      context.Background(),
		AgentCtx: &agentctx.AgentContext{},
		Config:   &LoopConfig{},
	}

	r.RunAfterAgent(hctx)

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("expected sequential order [1,2,3], got %v", order)
	}
}

// ---- Additional: slog.Warn verification on hook error ----

func TestHookErrorSlogWarn(t *testing.T) {
	// Capture slog output to verify warning is emitted
	var buf strings.Builder
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	original := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(original)

	r := &HookRegistry{
		BeforeModelHooks: []BeforeModelHook{
			func(hctx HookContext, msgs []agentctx.AgentMessage) ([]agentctx.AgentMessage, error) {
				return nil, errors.New("test error")
			},
		},
	}
	agentCtx := &agentctx.AgentContext{}
	hctx := HookContext{
		Ctx:      context.Background(),
		AgentCtx: agentCtx,
		Config:   &LoopConfig{},
	}
	r.RunBeforeModel(hctx, nil)

	output := buf.String()
	if !strings.Contains(output, "[Hook]") || !strings.Contains(output, "test error") {
		t.Fatalf("expected slog.Warn output containing [Hook] and 'test error', got:\n%s", output)
	}
}

// ---- Fan-out verification: BeforeModel hooks each receive same input ----

func TestBeforeModelFanOut(t *testing.T) {
	var received [][]agentctx.AgentMessage
	r := &HookRegistry{
		BeforeModelHooks: []BeforeModelHook{
			func(hctx HookContext, msgs []agentctx.AgentMessage) ([]agentctx.AgentMessage, error) {
				cp := make([]agentctx.AgentMessage, len(msgs))
				copy(cp, msgs)
				received = append(received, cp)
				return []agentctx.AgentMessage{{Role: "framework", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "a"}}}}, nil
			},
			func(hctx HookContext, msgs []agentctx.AgentMessage) ([]agentctx.AgentMessage, error) {
				cp := make([]agentctx.AgentMessage, len(msgs))
				copy(cp, msgs)
				received = append(received, cp)
				return []agentctx.AgentMessage{{Role: "framework", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "b"}}}}, nil
			},
		},
	}
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{{Role: "user"}},
	}
	hctx := HookContext{
		Ctx:      context.Background(),
		AgentCtx: agentCtx,
		Config:   &LoopConfig{},
	}

	input := []agentctx.AgentMessage{{Role: "user"}}
	n := r.RunBeforeModel(hctx, input)

	if n != 2 {
		t.Fatalf("expected 2 injected messages, got %d", n)
	}
	if len(received) != 2 {
		t.Fatalf("expected 2 hook invocations, got %d", len(received))
	}
	// Both hooks should receive the same input
	if len(received[0]) != 1 || len(received[1]) != 1 {
		t.Fatalf("expected both hooks to receive 1 message, got %d and %d", len(received[0]), len(received[1]))
	}
	if received[0][0].Role != "user" || received[1][0].Role != "user" {
		t.Fatalf("fan-out: both hooks should receive same input messages")
	}
	// Total messages in RecentMessages should be original(1) + injected(2) = 3
	if len(agentCtx.RecentMessages) != 3 {
		t.Fatalf("expected 3 messages total in RecentMessages, got %d", len(agentCtx.RecentMessages))
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
