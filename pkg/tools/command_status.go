package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// CommandStatusTool checks the status of running commands.
type CommandStatusTool struct {
	registry *CommandRegistry
}

// NewCommandStatusTool creates a new command status tool.
func NewCommandStatusTool() *CommandStatusTool {
	return &CommandStatusTool{
		registry: globalCommandRegistry,
	}
}

// Name returns the tool name.
func (t *CommandStatusTool) Name() string {
	return "command_status"
}

// Description returns the tool description.
func (t *CommandStatusTool) Description() string {
	return "Check the status of a running background command. Returns command ID, PID, elapsed time, completion status, exit code, and output."
}

// Parameters returns the JSON Schema for tool parameters.
func (t *CommandStatusTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command_id": map[string]any{
				"type":        "string",
				"description": "Command ID returned by the bash tool (e.g., cmd-1234567890)",
			},
		},
		"required": []string{"command_id"},
	}
}

// Execute checks the status of a running command.
func (t *CommandStatusTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	commandID, ok := args["command_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid command_id argument")
	}

	state, ok := t.registry.GetCommand(commandID)
	if !ok {
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Command not found: %s (it may have been cleaned up or never existed)", commandID),
			},
		}, nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	elapsed := time.Since(state.StartTime)

	var status string
	if state.Done {
		if state.ExitCode == 0 {
			status = "✓ Completed successfully"
		} else {
			status = fmt.Sprintf("✗ Completed with error (exit code %d)", state.ExitCode)
		}
	} else {
		status = "○ Running"
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Command Status\n"))
	result.WriteString(fmt.Sprintf("==============\n\n"))
	result.WriteString(fmt.Sprintf("Command ID: %s\n", commandID))
	result.WriteString(fmt.Sprintf("Status: %s\n", status))
	result.WriteString(fmt.Sprintf("PID: %d\n", state.PID))
	result.WriteString(fmt.Sprintf("PGID: %d\n", state.PGID))
	result.WriteString(fmt.Sprintf("Elapsed time: %v\n", elapsed))
	result.WriteString(fmt.Sprintf("Command: %s\n", state.Command))

	if state.Done {
		result.WriteString(fmt.Sprintf("Exit code: %d\n", state.ExitCode))
	}

	if state.Error != "" {
		result.WriteString(fmt.Sprintf("\nError: %s\n", state.Error))
	}

	output := state.Output.String()
	if output != "" {
		result.WriteString(fmt.Sprintf("\nOutput (%d bytes):\n%s\n", len(output), output))
	} else {
		result.WriteString("\nNo output yet.\n")
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: result.String(),
		},
	}, nil
}