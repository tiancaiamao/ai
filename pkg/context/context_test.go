package context

import (
	"context"
	"testing"

	"github.com/tiancaiamao/ai/pkg/skill"
)

// AgentContext convenience methods — all 0% covered without tests.

func TestNewAgentContext(t *testing.T) {
	ctx := NewAgentContext("sys")
	if ctx.SystemPrompt != "sys" {
		t.Fatalf("expected 'sys', got %q", ctx.SystemPrompt)
	}
	if ctx.RecentMessages == nil || len(ctx.RecentMessages) != 0 {
		t.Fatalf("expected non-nil empty slice, got %v", ctx.RecentMessages)
	}
	if ctx.AgentState == nil {
		t.Fatal("expected non-nil AgentState")
	}
}

func TestNewAgentContextWithSessionID(t *testing.T) {
	ctx := NewAgentContextWithSessionID("sys", "session-1", "/cwd")
	if ctx.AgentState.SessionID != "session-1" {
		t.Fatalf("expected session-1, got %q", ctx.AgentState.SessionID)
	}
	if ctx.AgentState.CurrentWorkingDir != "/cwd" {
		t.Fatalf("expected /cwd, got %q", ctx.AgentState.CurrentWorkingDir)
	}
}

func TestNewAgentContextWithSkills(t *testing.T) {
	t.Run("no skills", func(t *testing.T) {
		ctx := NewAgentContextWithSkills("sys", nil)
		if ctx.SystemPrompt != "sys" {
			t.Fatalf("expected unchanged prompt, got %q", ctx.SystemPrompt)
		}
	})

	t.Run("with skills appends formatted text", func(t *testing.T) {
		skills := []skill.Skill{
			{
				Name:        "test-skill",
				Description: "a skill for testing",
			},
		}
		ctx := NewAgentContextWithSkills("sys\n", skills)
		if ctx.SystemPrompt == "sys\n" {
			t.Fatal("expected system prompt to be extended with skill text")
		}
		// The skill should be mentioned somewhere in the extended prompt.
		// (The exact format depends on skill.FormatForPrompt — we just sanity check.)
		if len(ctx.Skills) != 1 {
			t.Fatalf("expected 1 skill, got %d", len(ctx.Skills))
		}
	})
}

func TestAddRecentAndAddMessage(t *testing.T) {
	ctx := NewAgentContext("")
	ctx.AddRecentMessage(NewUserMessage("a"))
	ctx.AddMessage(NewUserMessage("b"))
	if len(ctx.RecentMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(ctx.RecentMessages))
	}
}

func TestAgentContextEstimateTokensAndPercent(t *testing.T) {
	ctx := NewAgentContext("system")
	ctx.Tools = []Tool{&fakeTool{name: "x"}}
	ctx.RecentMessages = []AgentMessage{NewUserMessage("hi")}
	ctx.AgentState.TokensLimit = 100000

	tokens := ctx.EstimateTokens()
	if tokens <= 0 {
		t.Fatalf("expected positive tokens, got %d", tokens)
	}

	pct := ctx.EstimateTokenPercent()
	if pct <= 0 || pct >= 1 {
		t.Fatalf("expected fraction in (0,1), got %v", pct)
	}

	tools := ctx.EstimateToolsTokens()
	if tools <= 0 {
		t.Fatalf("expected positive tool tokens, got %d", tools)
	}
}

func TestCountStaleOutputs(t *testing.T) {
	ctx := NewAgentContext("")
	ctx.AgentState.TotalTurns = 10
	ctx.RecentMessages = []AgentMessage{
		{Role: "toolResult", TruncatedAt: 9}, // currentTurn - 9 = 1, not stale for maxAge=0
		{Role: "toolResult", TruncatedAt: 5}, // 10 - 5 = 5, stale for maxAge=2
		{Role: "toolResult", TruncatedAt: 1}, // 10 - 1 = 9, stale
		{Role: "user", TruncatedAt: 0},       // not a tool result
	}

	if got := ctx.CountStaleOutputs(2); got != 2 {
		t.Fatalf("expected 2 stale outputs, got %d", got)
	}
	if got := ctx.CountStaleOutputs(0); got != 3 {
		t.Fatalf("expected 3 stale outputs with maxAge=0, got %d", got)
	}
}

func TestLockUnlockContextManagement(t *testing.T) {
	t.Run("non-nil receiver", func(t *testing.T) {
		ctx := NewAgentContext("")
		ctx.LockContextManagement()
		ctx.UnlockContextManagement()
		// Should not deadlock — re-acquire to confirm.
		ctx.LockContextManagement()
		ctx.UnlockContextManagement()
	})

	t.Run("nil receiver is safe", func(t *testing.T) {
		var ctx *AgentContext
		ctx.LockContextManagement()   // should not panic
		ctx.UnlockContextManagement() // should not panic
	})
}

func TestAddAndGetTool(t *testing.T) {
	ctx := NewAgentContext("")

	t.Run("nil tool is ignored", func(t *testing.T) {
		ctx.AddTool(nil)
		if len(ctx.Tools) != 0 {
			t.Fatalf("expected 0 tools after nil add, got %d", len(ctx.Tools))
		}
	})

	t.Run("add and retrieve by name", func(t *testing.T) {
		t1 := &fakeTool{name: "read"}
		ctx.AddTool(t1)
		if got := ctx.GetTool("read"); got != t1 {
			t.Fatalf("expected to retrieve t1, got %v", got)
		}
	})

	t.Run("duplicate add is ignored", func(t *testing.T) {
		ctx.AddTool(&fakeTool{name: "read", desc: "duplicate"})
		if len(ctx.Tools) != 1 {
			t.Fatalf("expected 1 tool (duplicate ignored), got %d", len(ctx.Tools))
		}
	})

	t.Run("unknown name returns nil", func(t *testing.T) {
		if got := ctx.GetTool("nonexistent"); got != nil {
			t.Fatalf("expected nil for missing tool, got %v", got)
		}
	})

	t.Run("skip nil tools in slice during duplicate check", func(t *testing.T) {
		// Inject a nil tool into the slice (bypassing AddTool's nil check)
		// to exercise the `existing == nil` branch. AddTool must not panic
		// and must still skip the nil entry when scanning for duplicates.
		// Don't use GetTool to verify (it doesn't tolerate nils in slice).
		ctx.Tools = append(ctx.Tools, nil) // index 1
		before := len(ctx.Tools)
		ctx.AddTool(&fakeTool{name: "write"}) // new name, should append
		if len(ctx.Tools) != before+1 {
			t.Fatalf("expected append, got len %d (before=%d)", len(ctx.Tools), before)
		}
		// Adding "read" again must still be detected as duplicate (skip the nil).
		ctx.AddTool(&fakeTool{name: "read"})
		if len(ctx.Tools) != before+1 {
			t.Fatalf("expected duplicate detection to still work despite nil, got len %d", len(ctx.Tools))
		}
	})
}

func TestSetAllowedTools(t *testing.T) {
	ctx := NewAgentContext("")

	// nil → all allowed
	ctx.SetAllowedTools(nil)
	if !ctx.IsToolAllowed("anything") {
		t.Fatal("expected all allowed when whitelist is nil")
	}
	if got := ctx.GetAllowedTools(); got != nil {
		t.Fatalf("expected nil for nil whitelist, got %v", got)
	}
	if got := ctx.GetAllowedToolsMap(); got != nil {
		t.Fatalf("expected nil map, got %v", got)
	}

	// Specific whitelist
	ctx.SetAllowedTools([]string{"read", "write"})
	if !ctx.IsToolAllowed("read") {
		t.Fatal("expected read to be allowed")
	}
	if ctx.IsToolAllowed("nuke") {
		t.Fatal("expected nuke to NOT be allowed")
	}
	got := ctx.GetAllowedTools()
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d (%v)", len(got), got)
	}
	if ctx.GetAllowedToolsMap() == nil {
		t.Fatal("expected non-nil map")
	}
}

func TestWithToolExecutionAgentContext(t *testing.T) {
	t.Run("nil ctx becomes Background", func(t *testing.T) {
		agent := NewAgentContext("")
		out := WithToolExecutionAgentContext(nil, agent)
		if ToolExecutionAgentContext(out) != agent {
			t.Fatal("expected to retrieve agent")
		}
	})

	t.Run("non-nil ctx preserves values", func(t *testing.T) {
		agent := NewAgentContext("")
		out := WithToolExecutionAgentContext(context.Background(), agent)
		if ToolExecutionAgentContext(out) != agent {
			t.Fatal("expected to retrieve agent")
		}
	})

	t.Run("missing value returns nil", func(t *testing.T) {
		if ToolExecutionAgentContext(context.Background()) != nil {
			t.Fatal("expected nil for plain ctx")
		}
		if ToolExecutionAgentContext(nil) != nil {
			t.Fatal("expected nil for nil ctx")
		}
	})
}

func TestWithToolExecutionCallID(t *testing.T) {
	t.Run("nil ctx becomes Background", func(t *testing.T) {
		out := WithToolExecutionCallID(nil, "tc1")
		if ToolExecutionCallID(out) != "tc1" {
			t.Fatalf("expected tc1, got %q", ToolExecutionCallID(out))
		}
	})

	t.Run("non-nil ctx preserves value", func(t *testing.T) {
		out := WithToolExecutionCallID(context.Background(), "tc2")
		if ToolExecutionCallID(out) != "tc2" {
			t.Fatalf("expected tc2, got %q", ToolExecutionCallID(out))
		}
	})

	t.Run("missing value returns empty", func(t *testing.T) {
		if ToolExecutionCallID(context.Background()) != "" {
			t.Fatal("expected empty for plain ctx")
		}
		if ToolExecutionCallID(nil) != "" {
			t.Fatal("expected empty for nil ctx")
		}
	})
}
