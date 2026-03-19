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

// CommandState tracks the state of a running command.
type CommandState struct {
	Command    string
	PID        int
	PGID       int // Process group ID for cleanup
	StartTime  time.Time
	Output     strings.Builder
	Done       bool
	ExitCode   int
	Error      string
	mu         sync.Mutex
	cancel     context.CancelFunc
}

// CommandRegistry manages running commands.
type CommandRegistry struct {
	commands sync.Map // map[string]*CommandState
}

var globalCommandRegistry = &CommandRegistry{}

// isSubagentWaitCommand checks if command is a subagent_wait call.
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
			found := false
			for i := 1; i < len(rest); i++ {
				if rest[i] == quote {
					command = strings.TrimSpace(rest[i+1:])
					found = true
					break
				}
			}
			// If no closing quote found, malformed input - stop parsing
			if !found {
				return ""
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

// RegisterCommand registers a command and returns its ID.
func (r *CommandRegistry) RegisterCommand(command string) string {
	id := fmt.Sprintf("cmd-%d", time.Now().UnixNano())
	state := &CommandState{
		Command:   command,
		StartTime: time.Now(),
	}
	r.commands.Store(id, state)
	return id
}

// GetCommand retrieves a command by ID.
func (r *CommandRegistry) GetCommand(id string) (*CommandState, bool) {
	if v, ok := r.commands.Load(id); ok {
		return v.(*CommandState), true
	}
	return nil, false
}

// Execute runs a command in background and streams output.
func (r *CommandRegistry) Execute(ctx context.Context, cmdID string, command string, cwd string) {
	state, ok := r.GetCommand(cmdID)
	if !ok {
		return
	}

	// Create command with process group for cleanup
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = cwd

	// Set process group to enable cleanup of entire process tree
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Setup pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		state.mu.Lock()
		state.Error = fmt.Sprintf("failed to create stdout pipe: %v", err)
		state.Done = true
		state.mu.Unlock()
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		state.mu.Lock()
		state.Error = fmt.Sprintf("failed to create stderr pipe: %v", err)
		state.Done = true
		state.mu.Unlock()
		return
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		state.mu.Lock()
		state.Error = fmt.Sprintf("failed to start command: %v", err)
		state.Done = true
		state.mu.Unlock()
		return
	}

	state.mu.Lock()
	state.PID = cmd.Process.Pid
	state.PGID = cmd.Process.Pid // Process group ID equals process ID when Setpgid is true
	state.mu.Unlock()

	// Stream stdout/stderr concurrently to avoid pipe backpressure deadlocks.
	var outputWG sync.WaitGroup
	streamPipe := func(reader io.Reader) {
		defer outputWG.Done()
		scanner := bufio.NewScanner(reader)
		// Increase scanner token limit to avoid dropping large lines.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			state.mu.Lock()
			state.Output.WriteString(line + "\n")
			state.mu.Unlock()
		}
		if scanErr := scanner.Err(); scanErr != nil {
			state.mu.Lock()
			state.Output.WriteString(fmt.Sprintf("stream read error: %v\n", scanErr))
			state.mu.Unlock()
		}
	}

	outputWG.Add(2)
	go streamPipe(stdout)
	go streamPipe(stderr)

	// Wait for command to finish
	err = cmd.Wait()

	// Wait for output streaming to complete before marking done.
	outputWG.Wait()

	state.mu.Lock()
	state.Done = true
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			state.ExitCode = exitErr.ExitCode()
		} else {
			state.ExitCode = -1
			state.Error = err.Error()
		}
	} else {
		state.ExitCode = 0
	}
	state.mu.Unlock()
}

// BashTool executes bash commands with dynamic workspace support and async execution.
type BashTool struct {
	workspace   *Workspace
	timeout     time.Duration
	execTimeout time.Duration
	registry    *CommandRegistry
}

// NewBashTool creates a new Bash tool with dynamic workspace support.
func NewBashTool(ws *Workspace) *BashTool {
	return &BashTool{
		workspace:   ws,
		timeout:     120 * time.Second, // Default 120s for overall timeout
		execTimeout: 120 * time.Second, // Default 120s for individual commands (LLM can override)
		registry:    globalCommandRegistry,
	}
}

// Name returns the tool name.
func (t *BashTool) Name() string {
	return "bash"
}

// Description returns the tool description.
func (t *BashTool) Description() string {
	return `Execute bash commands in the current working directory.

Timeout behavior:
  • Default: 120 seconds
  • Override: Set timeout parameter (in seconds)
  • No limit: Set timeout=0 to wait indefinitely
  • On timeout: Command continues running in background, partial output returned

When a command times out:
  • Command continues running in background (use command_status to check)
  • Partial output is returned
  • Retry with longer timeout or use timeout=0 for long-running commands

Examples:
  • Normal: {"command": "make build"}
  • Custom timeout: {"command": "make build", "timeout": 300}
  • No timeout: {"command": "make build", "timeout": 0}`
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
				"description": "Timeout in seconds (default: 120, 0 for no timeout). If command times out, it continues running in background.",
				"minimum":     0,
			},
		},
		"required": []string{"command"},
	}
}

// Execute executes the bash command with async execution support.
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

	// Register command
	cmdID := t.registry.RegisterCommand(command)
	slog.Info("[Bash] Command registered", "cmdID", cmdID, "command", command, "timeout", execTimeout.Seconds())

	// Start command in background
	go t.registry.Execute(ctx, cmdID, command, cwd)

	// Wait for timeout or command completion
	timeoutChan := make(chan struct{})
	if execTimeout > 0 {
		go func() {
			time.Sleep(execTimeout)
			close(timeoutChan)
		}()
	}

	// Poll command state
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutChan:
			// Timeout expired - return partial output and let LLM decide
			state, _ := t.registry.GetCommand(cmdID)
			state.mu.Lock()
			partialOutput := state.Output.String()
			elapsed := time.Since(state.StartTime)
			pgid := state.PGID
			state.mu.Unlock()

			slog.Warn("[Bash] Command timed out",
				"cmdID", cmdID,
				"command", command,
				"timeout", execTimeout.Seconds(),
				"elapsed", elapsed.Seconds(),
				"outputSize", len(partialOutput),
				"pgid", pgid)

			resultText := fmt.Sprintf(
				"Command timed out after %v (configured timeout was %v).\n"+
					"Command is still running in background (PID: %d, PGID: %d).\n\n"+
					"Partial output (%d bytes):\n%s\n\n"+
					"Options:\n"+
					"  • command_status id=%s  - Check current status\n"+
					"  • Wait and check later  - Command may complete\n"+
					"  • Retry with timeout=%d - Use longer timeout\n"+
					"  • timeout=0             - Wait indefinitely\n",
				elapsed, execTimeout, state.PID, pgid,
				len(partialOutput), partialOutput, cmdID,
				int(execTimeout.Seconds())*2,
			)

			return []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: resultText,
				},
			}, nil

		case <-ctx.Done():
			// Context canceled
			state, _ := t.registry.GetCommand(cmdID)
			state.mu.Lock()
			output := state.Output.String()
			elapsed := time.Since(state.StartTime)
			state.mu.Unlock()

			slog.Info("[Bash] Command context canceled",
				"cmdID", cmdID,
				"command", command,
				"elapsed", elapsed.Seconds(),
				"outputSize", len(output))

			return []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: fmt.Sprintf("Command execution canceled.\n\nOutput:\n%s", output),
				},
			}, nil

		case <-ticker.C:
			// Check if command completed
			state, ok := t.registry.GetCommand(cmdID)
			if ok {
				state.mu.Lock()
				done := state.Done
				if done {
					output := state.Output.String()
					exitCode := state.ExitCode
					errorMsg := state.Error
					elapsed := time.Since(state.StartTime)
					state.mu.Unlock()

					slog.Info("[Bash] Command completed",
						"cmdID", cmdID,
						"command", command,
						"exitCode", exitCode,
						"elapsed", elapsed.Seconds(),
						"outputSize", len(output))

					var result strings.Builder
					result.WriteString(output)
					if errorMsg != "" {
						if result.Len() > 0 {
							result.WriteString("\n")
						}
						result.WriteString(fmt.Sprintf("Command exited with error: %s (exit code %d)", errorMsg, exitCode))
					}

					return []agentctx.ContentBlock{
						agentctx.TextContent{
							Type: "text",
							Text: result.String(),
						},
					}, nil
				} else {
					state.mu.Unlock()
				}
			}
		}
	}
}

// SetTimeout sets the timeout for command execution.
func (t *BashTool) SetTimeout(timeout time.Duration) {
	t.execTimeout = timeout
}
