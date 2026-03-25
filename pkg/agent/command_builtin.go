package agent

import (
	"context"
	"fmt"
	"strings"
)

func registerBuiltinCommands(a *Agent) {
	// /help - Display help information
	a.commands.Register("help", "Display help information for all available commands",
		func(ctx context.Context, agent *Agent, sessionKey string, args string) (string, error) {
			return handleHelpCommand(agent, args)
		})

	// /commands - List all available commands
	a.commands.Register("commands", "List all available commands",
		func(ctx context.Context, agent *Agent, sessionKey string, args string) (string, error) {
			return handleCommandsCommand(agent, args)
		})
}

func handleHelpCommand(agent *Agent, args string) (string, error) {
	descriptors := agent.commands.ListDescriptors()
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

func handleCommandsCommand(agent *Agent, args string) (string, error) {
	commands := agent.commands.List()
	if len(commands) == 0 {
		return "No commands available.", nil
	}
	return fmt.Sprintf("Available commands: %s", strings.Join(commands, ", ")), nil
}