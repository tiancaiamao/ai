package tools

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// BashResult represents the result of a bash command execution.
type BashResult struct {
	ExitCode int    `json:"exitCode"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// isSubagentWaitCommand checks if the command is a subagent_wait call.
// Uses robust parsing to avoid false positives from echo, grep, comments, etc.
func isSubagentWaitCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}

	// Parse the first token (the command itself) using shell-like rules
	// Handle: ~/.ai/skills/subagent/bin/subagent_wait.sh, ./subagent_wait.sh, etc.
	firstToken := extractFirstCommandToken(command)
	if firstToken == "" {
		return false
	}

	// Check if the command name (without path) is subagent_wait.sh or subagent_wait
	// Handle paths like:
	//   ~/.ai/skills/subagent/bin/subagent_wait.sh -> subagent_wait.sh
	//   ./subagent_wait.sh -> subagent_wait.sh
	//   /usr/local/bin/subagent_wait -> subagent_wait
	baseName := firstToken
	if idx := strings.LastIndex(baseName, "/"); idx >= 0 {
		baseName = baseName[idx+1:]
	}

	// Exact match on the basename
	return baseName == "subagent_wait.sh" || baseName == "subagent_wait"
}

// extractFirstCommandToken extracts the first token from a shell command.
// This handles quoted strings, environment variables, and common shell constructs.
func extractFirstCommandToken(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}

	// Skip environment variable assignments (VAR=value cmd)
	for {
		command = strings.TrimSpace(command)
		if command == "" {
			return ""
		}
		
		// Check if command starts with VAR=value pattern
		eqIdx := strings.IndexByte(command, '=')
		if eqIdx <= 0 {
			break
		}
		
		varName := command[:eqIdx]
		if !isValidEnvVarName(varName) {
			break
		}
		
		// Skip past the = and the value
		rest := command[eqIdx+1:]
		if len(rest) == 0 {
			break
		}
		
		// Handle quoted values
		if rest[0] == '"' || rest[0] == '\'' {
			quote := rest[0]
			// Find matching end quote
			for i := 1; i < len(rest); i++ {
				if rest[i] == quote {
					command = strings.TrimSpace(rest[i+1:])
					break
				}
			}
		} else {
			// Unquoted value - skip until space
			spaceIdx := strings.IndexByte(rest, ' ')
			if spaceIdx < 0 {
				// No space after value, nothing left
				return ""
			}
			command = strings.TrimSpace(rest[spaceIdx+1:])
		}
	}

	if command == "" {
		return ""
	}

	// Now extract the actual command token
	var result strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(command); i++ {
		ch := command[i]

		switch {
		case !inQuote && (ch == '"' || ch == '\''):
			// Start of quoted section - the command itself might be quoted
			inQuote = true
			quoteChar = ch
		case inQuote && ch == quoteChar:
			// End of quoted section
			inQuote = false
			quoteChar = 0
		case !inQuote && (ch == ' ' || ch == '\t' || ch == '|' || ch == '&' || ch == ';' || ch == '<' || ch == '>' || ch == '#'):
			// End of token
			if result.Len() > 0 {
				return result.String()
			}
			// If we hit a pipe or semicolon before any command, there's no valid token
			if ch == '|' || ch == ';' || ch == '#' {
				return ""
			}
		case !inQuote && ch == '$' && i+1 < len(command) && command[i+1] == '(':
			// Command substitution at start - not a simple command
			return ""
		default:
			result.WriteByte(ch)
		}
	}

	return result.String()
}

// isValidEnvVarName checks if s is a valid environment variable name.
func isValidEnvVarName(s string) bool {
	if len(s) == 0 {
		return false
	}
	// First char must be letter or underscore
	if !((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z') || s[0] == '_') {
		return false
	}
	// Rest can be alphanumeric or underscore
	for i := 1; i < len(s); i++ {
		ch := s[i]
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}

// BashTool executes bash commands with dynamic workspace support.
type BashTool struct {
	workspace   *Workspace
	timeout     time.Duration
	execTimeout time.Duration
}

// NewBashTool creates a new Bash tool with dynamic workspace support.
func NewBashTool(ws *Workspace) *BashTool {
	return &BashTool{
		workspace:   ws,
		timeout:     60 * time.Second, // Increased from 30s
		execTimeout: 30 * time.Second, // Increased from 5s to allow longer commands
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

// Execute executes the bash command with dynamic workspace support.
func (t *BashTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	command, ok := args["command"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid command argument")
	}

	// Get current working directory from workspace
	cwd := t.workspace.GetCWD()
	
	// Check if this is a subagent_wait command (precise match)
	im := getGlobalInterruptManager()
	interruptFile := ""
	interruptID := ""
	if im != nil && isSubagentWaitCommand(command) {
		// Generate interrupt file path
		interruptFile = GenerateInterruptFilePath()
		interruptID = im.RegisterInterruptFile(interruptFile)
		defer im.UnregisterInterruptFile(interruptID)
		
		// Append interrupt file to command
		command = command + " " + interruptFile
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, t.execTimeout)
	defer cancel()

	// Execute command using /bin/sh -c with current workspace directory
	cmd := exec.CommandContext(execCtx, "/bin/sh", "-c", command)
	cmd.Dir = cwd

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

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: outputBuilder.String(),
		},
	}, nil
}

// SetTimeout sets the timeout for command execution.
func (t *BashTool) SetTimeout(timeout time.Duration) {
	t.execTimeout = timeout
}
