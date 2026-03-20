package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

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
		timeout:     120 * time.Second, // Default 120s for overall timeout
		execTimeout: 120 * time.Second, // Default 120s for individual commands (LLM can override)
	}
}

// Name returns the tool name.
func (t *BashTool) Name() string {
	return "bash"
}

// Description returns the tool description.
func (t *BashTool) Description() string {
	return `Execute bash commands in the current working directory.

Best for quick commands (<2 minutes). For long-running tasks (builds, large test suites, servers), use the /tmux skill instead.

Timeout behavior:
  • Default: 120 seconds
  • Override: Set timeout parameter (in seconds)
  • No limit: Set timeout=0 to wait indefinitely
  • On timeout: Command is killed (hard termination)

When a command times out:
  • Command is terminated (process group killed)
  • Partial output is returned
  • For long tasks, use /tmux skill for proper background management

Examples:
  • Normal: {"command": "ls -la"}
  • Custom timeout: {"command": "sleep 30", "timeout": 60}
  • Long task with tmux: Use /tmux skill instead (e.g., builds, servers, large tests)`
}

// Parameters returns the JSON Schema for tool parameters.
func (t *BashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Bash command to execute",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds (default: 120, 0 for no timeout). On timeout, command is killed.",
				"minimum":     0,
			},
		},
		"required": []string{"command"},
	}
}

// Execute executes the bash command synchronously with timeout.
func (t *BashTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	command, ok := args["command"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid command argument")
	}

	// Get current working directory from workspace
	cwd := t.workspace.GetCWD()

	// Handle interrupt file for subagent_wait
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

	// Handle timeout parameter (default: 120 seconds)
	execTimeout := t.execTimeout
	if timeoutArg, ok := args["timeout"].(float64); ok {
		if timeoutArg > 0 {
			execTimeout = time.Duration(timeoutArg) * time.Second
			slog.Info("[Bash] Custom timeout set", "timeout", execTimeout.Seconds(), "command", command)
		} else {
			// timeout=0 means no timeout - wait for command to complete
			execTimeout = 0
			slog.Info("[Bash] No timeout (will wait indefinitely)", "command", command)
		}
	}

	// Create context with timeout
	cmdCtx := context.Background()
	var cancel context.CancelFunc
	if execTimeout > 0 {
		cmdCtx, cancel = context.WithTimeout(cmdCtx, execTimeout)
	} else {
		cmdCtx, cancel = context.WithCancel(cmdCtx)
	}
	defer cancel()

	// Also cancel on parent context done
	if ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			cancel()
		}()
	}

	// Create command
	cmd := exec.CommandContext(cmdCtx, "/bin/sh", "-c", command)
	cmd.Dir = cwd

	// Set process group to enable cleanup of entire process tree
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Setup pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Failed to start command: %v", err),
			},
		}, nil
	}

	// Stream stdout/stderr concurrently to avoid pipe backpressure deadlocks
	var output strings.Builder
	var outputWG sync.WaitGroup
	streamPipe := func(reader io.Reader) {
		defer outputWG.Done()
		scanner := bufio.NewScanner(reader)
		// Increase scanner token limit to avoid dropping large lines
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line + "\n")
		}
		if scanErr := scanner.Err(); scanErr != nil {
			output.WriteString(fmt.Sprintf("stream read error: %v\n", scanErr))
		}
	}

	outputWG.Add(2)
	go streamPipe(stdout)
	go streamPipe(stderr)

	// Wait for command to finish
	err = cmd.Wait()

	// Wait for output streaming to complete
	outputWG.Wait()

	elapsed := time.Since(startTime)

	// Check result
	if cmdCtx.Err() == context.DeadlineExceeded {
		slog.Warn("[Bash] Command timed out and was killed",
			"command", command,
			"timeout", execTimeout.Seconds(),
			"elapsed", elapsed.Seconds(),
			"outputSize", output.Len())

		resultText := fmt.Sprintf(
			"Command timed out after %v and was terminated.\n"+
				"Partial output (%d bytes):\n%s\n\n"+
				"For long-running tasks, use the /tmux skill for proper background management.",
			execTimeout, output.Len(), output.String())

		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: resultText,
			},
		}, nil
	}

	if cmdCtx.Err() == context.Canceled {
		slog.Info("[Bash] Command canceled",
			"command", command,
			"elapsed", elapsed.Seconds(),
			"outputSize", output.Len())

		resultText := fmt.Sprintf("Command canceled.\n\nOutput:\n%s", output.String())
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: resultText,
			},
		}, nil
	}

	slog.Info("[Bash] Command completed",
		"command", command,
		"exitCode", cmd.ProcessState.ExitCode(),
		"elapsed", elapsed.Seconds(),
		"outputSize", output.Len())

	// Build result
	var result strings.Builder
	result.WriteString(output.String())
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if result.Len() > 0 {
				result.WriteString("\n")
			}
			result.WriteString(fmt.Sprintf("Command exited with error (exit code %d)", exitErr.ExitCode()))
		} else {
			if result.Len() > 0 {
				result.WriteString("\n")
			}
			result.WriteString(fmt.Sprintf("Command error: %v", err))
		}
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: result.String(),
		},
	}, nil
}

// SetTimeout sets the timeout for command execution.
func (t *BashTool) SetTimeout(timeout time.Duration) {
	t.execTimeout = timeout
}

// isSubagentWaitCommand checks if command is a subagent_wait call.
// Uses robust parsing to avoid false positives from echo, grep, comments, etc.
func isSubagentWaitCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}

	// Parse the first token (the command itself) using shell-like rules
	firstToken := extractFirstCommandToken(command)
	if firstToken == "" {
		return false
	}

	// Check if the command name (without path) is subagent_wait.sh or subagent_wait
	baseName := firstToken
	if idx := strings.LastIndex(baseName, "/"); idx >= 0 {
		baseName = baseName[idx+1:]
	}

	// Exact match on the basename
	return baseName == "subagent_wait.sh" || baseName == "subagent_wait"
}

// extractFirstCommandToken extracts the first token from a shell command.
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
			found := false
			for i := 1; i < len(rest); i++ {
				if rest[i] == quote {
					command = strings.TrimSpace(rest[i+1:])
					found = true
					break
				}
			}
			if !found {
				return ""
			}
		} else {
			// Unquoted value - skip until space
			spaceIdx := strings.IndexByte(rest, ' ')
			if spaceIdx < 0 {
				return ""
			}
			command = strings.TrimSpace(rest[spaceIdx+1:])
		}
	}

	if command == "" {
		return ""
	}

	// Extract the actual command token
	var result strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(command); i++ {
		ch := command[i]

		switch {
		case !inQuote && (ch == '"' || ch == '\''):
			inQuote = true
			quoteChar = ch
		case inQuote && ch == quoteChar:
			inQuote = false
			quoteChar = 0
		case !inQuote && (ch == ' ' || ch == '\t' || ch == '|' || ch == '&' || ch == ';' || ch == '<' || ch == '>' || ch == '#'):
			if result.Len() > 0 {
				return result.String()
			}
			if ch == '|' || ch == ';' || ch == '#' {
				return ""
			}
		case !inQuote && ch == '$' && i+1 < len(command) && command[i+1] == '(':
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
