package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
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

	// Block dangerous tmux commands that can destroy the entire tmux server.
	// The agent itself runs inside tmux, so kill-server kills the agent too.
	if isDangerousTmuxKill(command) {
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: "⛔ Blocked: `tmux kill-server` is forbidden. It destroys the ENTIRE tmux server, killing all sessions including your own.\n\n" +
					"You may only kill sessions you created yourself:\n" +
					"  ✅ tmux kill-session -t <your-session-name>\n" +
					"  ❌ tmux kill-server\n" +
					"  ❌ looping over all sessions and killing them\n\n" +
					"If you need to clean up, kill only the specific named sessions you spawned.",
			},
		}, nil
	}

		// Block broad filesystem searches (find /, find ~, find $HOME).
	// These are slow, noisy, and wasteful. The agent should target specific directories.
	if isBroadFilesystemSearch(command) {
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: "⛔ Blocked: searching from filesystem root or home directory is forbidden.\n\n" +
					"Full-tree `find` is slow, noisy, and wasteful.\n\n" +
					"Instead, search within a specific directory:\n" +
					"  ❌ find /\n" +
					"  ❌ find ~\n" +
					"  ❌ find $HOME\n" +
					"  ✅ find /path/to/specific/dir -name '*.go'\n" +
					"  ✅ Use the grep tool for source code search\n\n" +
					"Either target a known specific directory, or search within the cwd/workspace directory.",
			},
		}, nil
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

	// Setup pipes for stdout and stderr using os.Pipe() instead of
	// cmd.StdoutPipe()/StderrPipe() to avoid a race condition:
	// cmd.Wait() closes pipes returned by StdoutPipe()/StderrPipe(),
	// which can race with our streaming goroutines that are still reading
	// from them, causing "file already closed" errors.
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		stdoutRead.Close()
		stdoutWrite.Close()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite

	// Start the command
	startTime := time.Now()
	if err := cmd.Start(); err != nil {
		stdoutRead.Close()
		stdoutWrite.Close()
		stderrRead.Close()
		stderrWrite.Close()
		msg := fmt.Sprintf("Failed to start command: %v", err)
		if cmd.Dir != "" {
			if _, statErr := os.Stat(cmd.Dir); statErr != nil {
				msg = fmt.Sprintf("Failed to start command: working directory %q does not exist (error: %v). Use change_workspace to switch to a valid directory.", cmd.Dir, err)
			}
		}
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: msg,
			},
		}, nil
	}

	// Close write ends in the parent process. The child process has its own
	// copies via fork/exec. Closing the parent's write ends ensures:
	// 1. No fd leak in parent
	// 2. When child exits, the write end is fully closed → our goroutines get EOF
	stdoutWrite.Close()
	stderrWrite.Close()

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
	go streamPipe(stdoutRead)
	go streamPipe(stderrRead)

	// Wait for command to finish. Since we use our own os.Pipe(),
	// cmd.Wait() will NOT close our pipes — we control the lifecycle.
	err = cmd.Wait()

	// Wait for output streaming to complete (goroutines get EOF after
	// child's write ends are closed on process exit).
	outputWG.Wait()

	// Close read ends now that goroutines have finished.
	stdoutRead.Close()
	stderrRead.Close()

	elapsed := time.Since(startTime)

	// Check result
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

// isBroadFilesystemSearch checks if the command runs `find` against the
// filesystem root (/), home directory (~), or $HOME — all of which are
// slow, noisy, and wasteful.
//
// Blocked patterns:
//   - find /            (any position in compound commands)
//   - find ~
//   - find $HOME
//
// Allowed:
//   - find /tmp -name x   (specific subdirectory)
//   - find ~/project      (specific subdirectory under home)
//   - find .              (current directory)
func isBroadFilesystemSearch(command string) bool {
	// find / — slash immediately followed by whitespace, pipe, semicolon, &,
	// ), or end of string. Does NOT match /tmp, /home/user, etc.
	rootRe := regexp.MustCompile(`\bfind\s+/([\s|;&)]|$)`)

	// find ~ — tilde immediately followed by whitespace, pipe, semicolon, &,
	// ), or end of string. Does NOT match ~/project (tilde followed by /).
	homeTildeRe := regexp.MustCompile(`\bfind\s+~([\s|;&)]|$)`)

	// find $HOME
	homeEnvRe := regexp.MustCompile(`\bfind\s+\$HOME\b`)

	return rootRe.MatchString(command) ||
		homeTildeRe.MatchString(command) ||
		homeEnvRe.MatchString(command)
}

// isDangerousTmuxKill checks if the command contains tmux kill-server,
// which destroys the entire tmux server and all sessions (including the agent's own).
func isDangerousTmuxKill(command string) bool {
	return regexp.MustCompile(`\btmux\s+kill-server\b`).MatchString(command)
}
