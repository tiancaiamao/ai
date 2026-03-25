package agent

import (
	"context"
	"fmt"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// registerAdditionalCommands registers more built-in commands.
func registerAdditionalCommands(a *Agent) {
	// /session - Display session information
	a.commands.Register("session", "Display current session information",
		func(ctx context.Context, agent *Agent, sessionKey string, args string) (string, error) {
			return handleSessionCommand(agent, args)
		})

	// /clear - Clear conversation context
	a.commands.Register("clear", "Clear the conversation context",
		func(ctx context.Context, agent *Agent, sessionKey string, args string) (string, error) {
			return handleClearCommand(agent, args)
		})

	// /model - Display or set the current model
	a.commands.Register("model", "Display or set the current model",
		func(ctx context.Context, agent *Agent, sessionKey string, args string) (string, error) {
			return handleModelCommand(agent, args)
		})

	// /set_thinking_level - Set the thinking level
	a.commands.Register("set_thinking_level", "Set the thinking level (off, minimal, low, medium, high, xhigh)",
		func(ctx context.Context, agent *Agent, sessionKey string, args string) (string, error) {
			return handleSetThinkingLevelCommand(agent, args)
		})
}

func handleSessionCommand(agent *Agent, args string) (string, error) {
	msgCount := len(agent.GetMessages())
	model := agent.GetModel()
	return fmt.Sprintf("Session: %d messages in context\nModel: %s\nThinking level: %s",
		msgCount, model.ID, agent.LoopConfig.ThinkingLevel), nil
}

func handleClearCommand(agent *Agent, args string) (string, error) {
	agentCtx := agent.GetContext()
	if agentCtx == nil {
		return "Error: no agent context", nil
	}
	// Clear the messages slice
	agentCtx.Messages = make([]agentctx.AgentMessage, 0)
	return "Conversation context cleared.", nil
}

func handleModelCommand(agent *Agent, args string) (string, error) {
	if args == "" {
		return fmt.Sprintf("Current model: %s", agent.GetModel().ID), nil
	}
	// Set model logic would require more implementation
	// For now, just display current model
	return fmt.Sprintf("Model setting not yet implemented. Current model: %s", agent.GetModel().ID), nil
}

func handleSetThinkingLevelCommand(agent *Agent, args string) (string, error) {
	level := strings.TrimSpace(args)
	validLevels := map[string]bool{
		"off":     true,
		"minimal": true,
		"low":     true,
		"medium":  true,
		"high":    true,
		"xhigh":   true,
	}
	if !validLevels[level] {
		return fmt.Sprintf("Invalid thinking level. Valid levels: off, minimal, low, medium, high, xhigh"), nil
	}
	agent.LoopConfig.ThinkingLevel = level
	return fmt.Sprintf("Thinking level set to: %s", level), nil
}