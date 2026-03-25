package agent

import (
	"context"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"strings"
	"testing"
)

func TestBuiltinCommands_Help(t *testing.T) {
	// Create an agent with command registry
	ctx := context.Background()
	agentCtx := agentctx.NewAgentContext("test")
	model := llm.Model{ID: "test-model"}
	agent := NewAgentWithContext(model, "test-api-key", agentCtx)

	// Test /help command
	result, err := agent.commands.HandleCommand(ctx, "help", "", agent, "test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Available commands") {
		t.Fatalf("expected 'Available commands' in result, got: %s", result)
	}

	if !strings.Contains(result, "/help") {
		t.Fatalf("expected '/help' in result, got: %s", result)
	}

	if !strings.Contains(result, "/commands") {
		t.Fatalf("expected '/commands' in result, got: %s", result)
	}

	// Check for descriptions
	if !strings.Contains(result, "Display help information") {
		t.Fatalf("expected description in result, got: %s", result)
	}
}

func TestBuiltinCommands_Commands(t *testing.T) {
	ctx := context.Background()
	agentCtx := agentctx.NewAgentContext("test")
	model := llm.Model{ID: "test-model"}
	agent := NewAgentWithContext(model, "test-api-key", agentCtx)

	// Test /commands command
	result, err := agent.commands.HandleCommand(ctx, "commands", "", agent, "test-session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Available commands") {
		t.Fatalf("expected 'Available commands' in result, got: %s", result)
	}

	if !strings.Contains(result, "help") {
		t.Fatalf("expected 'help' in result, got: %s", result)
	}

	if !strings.Contains(result, "commands") {
		t.Fatalf("expected 'commands' in result, got: %s", result)
	}
}