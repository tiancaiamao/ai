package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// BashResult represents the result of a bash command execution.
type BashResult struct {
	ExitCode int    `json:"exitCode"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// BashTool executes bash commands.
type BashTool struct {
	cwd         string
	timeout     time.Duration
	execTimeout time.Duration
}

// NewBashTool creates a new Bash tool.
func NewBashTool(cwd string) *BashTool {
	return &BashTool{
		cwd:         cwd,
		timeout:     60 * time.Second,    // Increased from 30s
		execTimeout: 30 * time.Second,    // Increased from 5s to allow longer commands
	}
}

// Name returns the tool name.
func (t *BashTool) Name() string {
	return "bash"
}

// Description returns the tool description.
func (t *BashTool) Description() string {
	return "Execute a bash command in the current working directory."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *BashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Bash command to execute",
			},
		},
		"required": []string{"command"},
	}
}

// Execute executes the bash command.
func (t *BashTool) Execute(ctx context.Context, args map[string]any) ([]agent.ContentBlock, error) {
	command, ok := args["command"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid command argument")
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, t.execTimeout)
	defer cancel()

	// Execute command using /bin/sh -c
	cmd := exec.CommandContext(execCtx, "/bin/sh", "-c", command)
	cmd.Dir = t.cwd

	output, err := cmd.CombinedOutput()

	result := BashResult{
		Output: string(output),
	}

	// Check for timeout
	if execCtx.Err() == context.DeadlineExceeded {
		result.Error = "command timed out"
		result.ExitCode = -1
	} else if err != nil {
		// Command failed
		result.Error = err.Error()
		if exitError, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitError.ExitCode()
		} else {
			result.ExitCode = -1
		}
	} else {
		result.ExitCode = 0
	}

	// Format output
	var outputBuilder strings.Builder
	if result.Output != "" {
		outputBuilder.WriteString(result.Output)
	}
	if result.Error != "" {
		if outputBuilder.Len() > 0 {
			outputBuilder.WriteString("\n")
		}
		outputBuilder.WriteString(fmt.Sprintf("Command exited with error: %s (exit code %d)", result.Error, result.ExitCode))
	}

	return []agent.ContentBlock{
		agent.TextContent{
			Type: "text",
			Text: outputBuilder.String(),
		},
	}, nil
}

// SetTimeout sets the timeout for command execution.
func (t *BashTool) SetTimeout(timeout time.Duration) {
	t.execTimeout = timeout
}
