package agent

import (
	"context"
	"fmt"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// processCommand checks if the message is a command and handles it.
// Returns true if the message was a command (and was handled), false otherwise.
func (a *Agent) processCommand(ctx context.Context, message string) (bool, error) {
	trimmed := strings.TrimSpace(message)
	if !strings.HasPrefix(trimmed, "/") {
		return false, nil // Not a command
	}

	// Handle command
	if a.commands == nil {
		return true, fmt.Errorf("command registry not initialized")
	}

	// Parse command: /name args
	parts := strings.Fields(strings.TrimPrefix(trimmed, "/"))
	if len(parts) == 0 {
		return true, fmt.Errorf("invalid command format")
	}
	name := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.Join(parts[1:], " ")
	}

	response, err := a.commands.HandleCommand(
		ctx,
		name,
		args,
		a,
		"", // sessionKey - to be added in Task 7
	)
	if err != nil {
		return true, err
	}

	// Emit command response as a system message
	cmdMsg := agentctx.NewUserMessage(response)
	a.emitEvent(NewMessageStartEvent(cmdMsg))
	a.emitEvent(NewMessageEndEvent(cmdMsg))
	a.emitEvent(NewTurnEndEvent(nil, nil))
	a.emitEvent(NewAgentEndEvent([]agentctx.AgentMessage{cmdMsg}))

	return true, nil
}