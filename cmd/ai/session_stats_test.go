package main

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/tools"
)

func TestComputeSessionStats_BasicCounts(t *testing.T) {
	// Create a workspace
	ws, err := tools.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	// Create an agent with messages
	ctx := agentctx.NewAgentContext("system prompt")
	// Add user message
	ctx.AddMessage(agentctx.AgentMessage{
		Role:      "user",
		Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hello"}},
	})
	// Add assistant message with usage
	assistantMsg := agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "hi there"},
		},
		Usage: &agentctx.Usage{
			InputTokens:  10,
			OutputTokens: 20,
			CacheRead:    5,
			Cost:         agentctx.Cost{Total: 0.01},
		},
	}
	ctx.AddMessage(assistantMsg)
	// Add tool result
	ctx.AddMessage(agentctx.AgentMessage{
		Role:       "toolResult",
		ToolCallID: "call_1",
		ToolName:   "bash",
		Content:    []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "output"}},
	})

	ag := agent.NewAgentFromConfigWithContext(
		llm.Model{ID: "test", ContextWindow: 200000},
		"test-key",
		ctx,
		agent.DefaultLoopConfig(),
	)

	// Create session
	sess := session.NewSession("")

	// Create compactor
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{ID: "test"}, "", "system prompt", 200000)

	// Create registry
	registry := tools.NewRegistry()

	stats := computeSessionStats(ag, sess, compactor, registry, 200000, "system prompt", ws)

	// Verify message counts
	if stats.UserMessages != 1 {
		t.Errorf("expected UserMessages=1, got %d", stats.UserMessages)
	}
	if stats.AssistantMessages != 1 {
		t.Errorf("expected AssistantMessages=1, got %d", stats.AssistantMessages)
	}
	if stats.ToolResults != 1 {
		t.Errorf("expected ToolResults=1, got %d", stats.ToolResults)
	}
	if stats.TotalMessages != 3 {
		t.Errorf("expected TotalMessages=3, got %d", stats.TotalMessages)
	}

	// Verify token stats
	if stats.Tokens.Input != 10 {
		t.Errorf("expected Input=10, got %d", stats.Tokens.Input)
	}
	if stats.Tokens.Output != 20 {
		t.Errorf("expected Output=20, got %d", stats.Tokens.Output)
	}
	if stats.Tokens.CacheRead != 5 {
		t.Errorf("expected CacheRead=5, got %d", stats.Tokens.CacheRead)
	}
	if stats.Tokens.Total != 30 {
		t.Errorf("expected Total=30, got %d", stats.Tokens.Total)
	}

	// Verify cost
	if stats.Cost != 0.01 {
		t.Errorf("expected Cost=0.01, got %f", stats.Cost)
	}

	// Verify active window tokens are > 0
	if stats.Tokens.ActiveWindowTokens <= 0 {
		t.Errorf("expected ActiveWindowTokens > 0, got %d", stats.Tokens.ActiveWindowTokens)
	}

	// Verify system prompt tokens
	if stats.Tokens.SystemPromptTokens <= 0 {
		t.Errorf("expected SystemPromptTokens > 0, got %d", stats.Tokens.SystemPromptTokens)
	}
}

func TestComputeSessionStats_EmptySession(t *testing.T) {
	ws, err := tools.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	ctx := agentctx.NewAgentContext("system prompt")
	ag := agent.NewAgentFromConfigWithContext(
		llm.Model{ID: "test", ContextWindow: 200000},
		"test-key",
		ctx,
		agent.DefaultLoopConfig(),
	)
	sess := session.NewSession("")
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{ID: "test"}, "", "system prompt", 200000)
	registry := tools.NewRegistry()

	stats := computeSessionStats(ag, sess, compactor, registry, 200000, "system prompt", ws)

	if stats.UserMessages != 0 {
		t.Errorf("expected UserMessages=0, got %d", stats.UserMessages)
	}
	if stats.AssistantMessages != 0 {
		t.Errorf("expected AssistantMessages=0, got %d", stats.AssistantMessages)
	}
	if stats.TotalMessages != 0 {
		t.Errorf("expected TotalMessages=0, got %d", stats.TotalMessages)
	}
	if stats.Tokens.Input != 0 {
		t.Errorf("expected Input=0, got %d", stats.Tokens.Input)
	}
	if stats.Tokens.Output != 0 {
		t.Errorf("expected Output=0, got %d", stats.Tokens.Output)
	}
	if stats.Tokens.Total != 0 {
		t.Errorf("expected Total=0, got %d", stats.Tokens.Total)
	}
}

func TestComputeSessionStats_ToolCallsCounted(t *testing.T) {
	ws, err := tools.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	ctx := agentctx.NewAgentContext("system prompt")

	// Add an assistant message with 2 tool calls
	ctx.AddMessage(agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.ToolCallContent{Type: "tool_call", ID: "call_1", Name: "bash", Arguments: map[string]any{"command": "ls"}},
			agentctx.ToolCallContent{Type: "tool_call", ID: "call_2", Name: "read", Arguments: map[string]any{"path": "foo.go"}},
		},
	})

	ag := agent.NewAgentFromConfigWithContext(
		llm.Model{ID: "test", ContextWindow: 200000},
		"test-key",
		ctx,
		agent.DefaultLoopConfig(),
	)
	sess := session.NewSession("")
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{ID: "test"}, "", "system prompt", 200000)
	registry := tools.NewRegistry()

	stats := computeSessionStats(ag, sess, compactor, registry, 200000, "system prompt", ws)

	if stats.ToolCalls != 2 {
		t.Errorf("expected ToolCalls=2, got %d", stats.ToolCalls)
	}
}

func TestComputeSessionStats_ReturnsSessionStatsType(t *testing.T) {
	// This test verifies the function returns a *rpc.SessionStats,
	// not a *rpc.SessionState (the bug being fixed).
	ws, err := tools.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	ctx := agentctx.NewAgentContext("system prompt")
	ag := agent.NewAgentFromConfigWithContext(
		llm.Model{ID: "test", ContextWindow: 200000},
		"test-key",
		ctx,
		agent.DefaultLoopConfig(),
	)
	sess := session.NewSession("")
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{ID: "test"}, "", "system prompt", 200000)
	registry := tools.NewRegistry()

	stats := computeSessionStats(ag, sess, compactor, registry, 200000, "system prompt", ws)

	// Verify it's the correct type with expected fields
	var _ *rpc.SessionStats = stats

	// Ensure the key fields exist and are not pointing to wrong type
	if stats.Tokens.ActiveWindowTokens == 0 && stats.Tokens.SystemPromptTokens == 0 {
		// Even empty sessions should have system prompt tokens
		t.Error("SessionStats should have SystemPromptTokens > 0")
	}
}