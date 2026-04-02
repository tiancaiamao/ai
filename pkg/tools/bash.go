package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
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
  • Custom timeout: {"command": "go build ./...", "timeout": 300}
  • No timeout: {"command": "go test -race ./...", "timeout": 0}
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
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("invalid command argument: command cannot be empty")
	}

	// Detect sleep commands with duration >= 30 seconds
	if sleepDuration, hasSleep := detectSleepCommand(command); hasSleep && sleepDuration >= 30 {
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf(
					"Error: sleep with duration >= 30 seconds is not allowed in bash tool (detected: %d seconds).\n\n"+
						"For long-running tasks, use the /tmux skill instead.\n\n"+
						"Quick guide:\n"+
						"• Start in tmux: tmux new -s <name> -d \"<your command>\"\n"+
						"• Check progress: tmux capture-pane -t <name> -p\n"+
						"• Attach to see: tmux attach -t <name>\n"+
						"• Wait in script: ~/.ai/skills/tmux/bin/tmux_wait.sh <name> [timeout]\n\n"+
						"See /tmux skill documentation for more details.",
					sleepDuration,
				),
			},
		}, nil
	}

	if isBareCDCommand(command) {
		return nil, fmt.Errorf("bare 'cd' only affects this shell subprocess and does not persist workspace. Use change_workspace for persistent switching, or use 'cd <dir> && <command>' for a one-off command")
	}

	// Get current working directory from workspace
	cwd := t.workspace.GetCWD()

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
	var outputMu sync.Mutex // Protect output from concurrent writes
	var outputWG sync.WaitGroup
	streamPipe := func(reader io.Reader) {
		defer outputWG.Done()
		bufReader := bufio.NewReader(reader)
		for {
			line, err := bufReader.ReadString('\n')
			if len(line) > 0 {
				outputMu.Lock()
				output.WriteString(line)
				outputMu.Unlock()
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				outputMu.Lock()
				output.WriteString(fmt.Sprintf("stream read error: %v\n", err))
				outputMu.Unlock()
				break
			}
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

	// Check if context was canceled (e.g., by /abort command)
	if cmdCtx.Err() == context.Canceled {
		// Kill entire process group to ensure all child processes are terminated
		if cmd.Process != nil {
			pgid, err := syscall.Getpgid(cmd.Process.Pid)
			if err == nil {
				// Kill the process group (negative PID)
				syscall.Kill(-pgid, syscall.SIGKILL)
				slog.Info("[Bash] Killed process group due to cancellation", "pgid", pgid)
			} else {
				// Fallback: kill just the process
				syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
				slog.Info("[Bash] Killed single process due to cancellation (no process group)", "pid", cmd.Process.Pid)
			}
		}

		slog.Info("[Bash] Command canceled",
			"command", command,
			"elapsed", elapsed.Seconds(),
			"outputSize", output.Len())

		resultText := fmt.Sprintf("Command canceled.\n\nPartial output (%d bytes):\n%s", output.Len(), output.String())
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: resultText,
			},
		}, nil
	}

	// Check if command timed out
	if cmdCtx.Err() == context.DeadlineExceeded {
		// Kill entire process group to ensure all child processes are terminated
		if cmd.Process != nil {
			pgid, err := syscall.Getpgid(cmd.Process.Pid)
			if err == nil {
				// Kill the process group (negative PID)
				syscall.Kill(-pgid, syscall.SIGKILL)
				slog.Debug("[Bash] Killed process group", "pgid", pgid)
			} else {
				// Fallback: kill just the process
				syscall.Kill(cmd.Process.Pid, syscall.SIGKILL)
				slog.Debug("[Bash] Killed single process (no process group)", "pid", cmd.Process.Pid)
			}
		}

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

func isBareCDCommand(command string) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}
	if cmd == "cd" {
		return true
	}
	if !strings.HasPrefix(cmd, "cd ") && !strings.HasPrefix(cmd, "cd\t") {
		return false
	}
	// Allow command-local directory changes such as `cd dir && make`.
	if strings.Contains(cmd, "&&") || strings.Contains(cmd, "||") {
		return false
	}
	// Any shell operator indicates this is more than a bare cd invocation.
	if strings.ContainsAny(cmd, ";&|\n") {
		return false
	}
	return true
}

// detectSleepCommand detects sleep commands and returns the duration in seconds.
// Returns (duration, true) if a sleep command is found, (0, false) otherwise.
// Handles patterns like:
// - sleep 90
// - sleep 30s
// - /bin/sleep 120
// - command && sleep 60
// - sleep 2m
func detectSleepCommand(command string) (int, bool) {
	// Match sleep command followed by a number (possibly with unit)
	// Pattern: sleep<space>[number][unit]
	// Supports: sleep 90, sleep 30s, /bin/sleep 120, command && sleep 60
	sleepPattern := regexp.MustCompile(`\bsleep\s+(\d+)([smh]?\b)`)

	matches := sleepPattern.FindStringSubmatch(command)
	if matches == nil {
		return 0, false
	}

	// Parse the numeric duration
	durationStr := matches[1]
	duration, err := strconv.Atoi(durationStr)
	if err != nil {
		return 0, false
	}

	// Handle time units
	unit := matches[2]
	switch unit {
	case "s", "":
		// Already in seconds
	case "m":
		duration *= 60
	case "h":
		duration *= 3600
	}

	return duration, true
}
