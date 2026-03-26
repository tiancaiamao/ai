package command

import (
	"context"
	"fmt"
	"strings"
)

// RegisterBuiltinCommands registers all standard commands to registry.
// These commands work with the agent.Agent interface from the ai project.
func RegisterBuiltinCommands(r *Registry) {
	// Handlers capture the registry to get command list
	r.Register("help", "Display help information for all available commands", func(ctx context.Context, cmdCtx CommandContext, args string) (string, error) {
		return handleHelp(ctx, cmdCtx, args, r)
	})

	r.Register("commands", "List all available commands", func(ctx context.Context, cmdCtx CommandContext, args string) (string, error) {
		return handleCommands(ctx, cmdCtx, args, r)
	})

	r.Register("session", "Display current session information", handleSession)
	r.Register("clear", "Clear the conversation context", handleClear)
	r.Register("model", "Display or set the current model", handleModel)
	r.Register("set_thinking_level", "Set the thinking level (off, minimal, low, medium, high, xhigh)", handleSetThinkingLevel)
}

// agentAPI is the interface we expect from the agent.
// Commands use type assertion: agent := cmdCtx.GetAgent().(agentAPI)
type agentAPI interface {
	GetModel() modelAPI
	GetMessages() messagesAPI
	GetContext() contextAPI
	SetThinkingLevel(level string)
}

type modelAPI interface {
	GetID() string
}

type messagesAPI interface {
	Len() int
}

type contextAPI interface {
	Messages
}

type Messages interface {
	Len() int
}

// handleHelp displays help information for all available commands.
func handleHelp(ctx context.Context, cmdCtx CommandContext, args string, r *Registry) (string, error) {
	descriptors := r.ListDescriptors()

	if len(descriptors) == 0 {
		return "No commands available.", nil
	}

	var sb strings.Builder
	sb.WriteString("Available commands:\n\n")
	for _, desc := range descriptors {
		sb.WriteString(fmt.Sprintf("  /%s - %s\n", desc.Name, desc.Description))
	}
	sb.WriteString("\nType /commands for a compact list.")
	return sb.String(), nil
}

// handleCommands lists all available commands.
func handleCommands(ctx context.Context, cmdCtx CommandContext, args string, r *Registry) (string, error) {
	commands := r.List()

	if len(commands) == 0 {
		return "No commands available.", nil
	}
	return fmt.Sprintf("Available commands: %s", strings.Join(commands, ", ")), nil
}

// handleSession displays current session information.
func handleSession(ctx context.Context, cmdCtx CommandContext, args string) (string, error) {
	agent, ok := cmdCtx.GetAgent().(agentAPI)
	if !ok {
		return "Error: agent does not support command interface", nil
	}

	msgCount := 0
	if m := agent.GetMessages(); m != nil {
		msgCount = m.Len()
	}

	model := "unknown"
	if m := agent.GetModel(); m != nil {
		model = m.GetID()
	}

	return fmt.Sprintf("Session: %d messages in context\nModel: %s", msgCount, model), nil
}

// handleClear clears the conversation context.
func handleClear(ctx context.Context, cmdCtx CommandContext, args string) (string, error) {
	// This requires access to session to clear messages
	// For now, return a message indicating not implemented
	return "Clear command not yet implemented (requires session access)", nil
}

// handleModel displays or sets the current model.
func handleModel(ctx context.Context, cmdCtx CommandContext, args string) (string, error) {
	agent, ok := cmdCtx.GetAgent().(agentAPI)
	if !ok {
		return "Error: agent does not support command interface", nil
	}

	if args == "" {
		model := "unknown"
		if m := agent.GetModel(); m != nil {
			model = m.GetID()
		}
		return fmt.Sprintf("Current model: %s", model), nil
	}
	// Set model logic would require more implementation
	// For now, just display current model
	return fmt.Sprintf("Model setting not yet implemented. Current model: %s", agent.GetModel().GetID()), nil
}

// handleSetThinkingLevel sets the thinking level.
func handleSetThinkingLevel(ctx context.Context, cmdCtx CommandContext, args string) (string, error) {
	agent, ok := cmdCtx.GetAgent().(agentAPI)
	if !ok {
		return "Error: agent does not support command interface", nil
	}

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

	agent.SetThinkingLevel(level)
	return fmt.Sprintf("Thinking level set to: %s", level), nil
}