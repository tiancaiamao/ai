package agent

import (
	"context"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/stretchr/testify/require"
)

func TestAdditionalCommands(t *testing.T) {
	agent := setupTestAgent(t)
	agent.commands = NewCommandRegistry()
	registerAdditionalCommands(agent)

	t.Run("SessionCommand", func(t *testing.T) {
		ctx := context.Background()
		result, err := agent.commands.HandleCommand(ctx, "session", "", agent, "")
		require.NoError(t, err)
		require.Contains(t, result, "Session:")
		require.Contains(t, result, "Model:")
		require.Contains(t, result, "Thinking level:")
	})

	t.Run("ClearCommand", func(t *testing.T) {
		ctx := context.Background()
		result, err := agent.commands.HandleCommand(ctx, "clear", "", agent, "")
		require.NoError(t, err)
		require.Contains(t, result, "Conversation context cleared")
	})

	t.Run("ModelCommand", func(t *testing.T) {
		ctx := context.Background()
		result, err := agent.commands.HandleCommand(ctx, "model", "", agent, "")
		require.NoError(t, err)
		require.Contains(t, result, "Current model:")
	})

	t.Run("SetThinkingLevelCommand", func(t *testing.T) {
		ctx := context.Background()

		// Valid level
		result, err := agent.commands.HandleCommand(ctx, "set_thinking_level", "high", agent, "")
		require.NoError(t, err)
		require.Contains(t, result, "Thinking level set to: high")
		require.Equal(t, "high", agent.LoopConfig.ThinkingLevel)

		// Invalid level
		result, err = agent.commands.HandleCommand(ctx, "set_thinking_level", "invalid", agent, "")
		require.NoError(t, err)
		require.Contains(t, result, "Invalid thinking level")
	})
}

func setupTestAgent(t *testing.T) *Agent {
	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
	}
	return &Agent{
		model:        model,
		systemPrompt: "Test",
		commands:     NewCommandRegistry(),
		LoopConfig: LoopConfig{
			ThinkingLevel: "high",
		},
		context: agentctx.NewAgentContext("Test"),
	}
}