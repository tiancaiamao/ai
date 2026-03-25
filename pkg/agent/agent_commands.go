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
		// Send error feedback to user
		errorMsg := agentctx.NewAssistantMessage()
		errorMsg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "Command registry not initialized"}}
		a.emitEvent(NewMessageStartEvent(errorMsg))
		a.emitEvent(NewMessageEndEvent(errorMsg))
		a.emitEvent(NewTurnEndEvent(nil, nil))
		a.emitEvent(NewAgentEndEvent(nil)) // Nil means don't replace session history
		return true, fmt.Errorf("command registry not initialized")
	}

	// Parse command: /name args
	parts := strings.Fields(strings.TrimPrefix(trimmed, "/"))
	if len(parts) == 0 {
		// Send error feedback to user
		errorMsg := agentctx.NewAssistantMessage()
		errorMsg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "Invalid command format"}}
		a.emitEvent(NewMessageStartEvent(errorMsg))
		a.emitEvent(NewMessageEndEvent(errorMsg))
		a.emitEvent(NewTurnEndEvent(nil, nil))
		a.emitEvent(NewAgentEndEvent(nil)) // Nil means don't replace session history
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
		// Send error feedback to user instead of silent failure
		errorMsg := agentctx.NewAssistantMessage()
		errorMsg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: fmt.Sprintf("Command error: %v", err)}}
		a.emitEvent(NewMessageStartEvent(errorMsg))
		a.emitEvent(NewMessageEndEvent(errorMsg))
		a.emitEvent(NewTurnEndEvent(nil, nil))
		a.emitEvent(NewAgentEndEvent(nil)) // Nil means don't replace session history
		return true, err
	}

	// Emit command response as assistant message (not user message)
	cmdMsg := agentctx.NewAssistantMessage()
	cmdMsg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: response}}
	a.emitEvent(NewMessageStartEvent(cmdMsg))
	a.emitEvent(NewMessageEndEvent(cmdMsg))
	a.emitEvent(NewTurnEndEvent(nil, nil))
	a.emitEvent(NewAgentEndEvent(nil)) // Nil means don't replace session history

	return true, nil
}