package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// exitGracePeriod is how long the bash tool keeps draining stdout/stderr
// after the main process has exited and no new output has arrived. A
// background descendant (e.g. a subshell waiting on a daemon started with
// `cmd &`) can hold the pipe write ends open, so EOF may never arrive; the
// grace period bounds the drain without truncating actively-written output.
const exitGracePeriod = 250 * time.Millisecond

// maxBashTimeout caps the user-supplied timeout parameter. Models frequently
// pass millisecond values (e.g. 120000 meaning 120s) where the schema asks
// for seconds — unclamped, that becomes a 33-hour timeout and effectively
// disables the safety net. 4h is far beyond any legitimate one-shot command.
// A var (not const) so tests can lower it.
var maxBashTimeout = 4 * time.Hour

// backgroundThreshold is how long Execute blocks on a foreground command
// before detaching it into a background operation (dsh's 5-second rule). The
// turn is not held hostage by a slow command: Execute returns a handle and
// the command's output arrives later as a wake-up event when it finishes.
const backgroundThreshold = 5 * time.Second

// BashTool executes bash commands with dynamic workspace support.
type BashTool struct {
	workspace   *Workspace
	timeout     time.Duration
	execTimeout time.Duration

	// onBackgroundComplete, when set, is invoked with the operation ID and
	// result text once a backgrounded command finishes. The app wires it to
	// wake the agent; nil means background completions are dropped.
	onBackgroundComplete func(opID, result string)
	// bgOpCounter mints unique IDs for background operations.
	bgOpCounter atomic.Int64
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

⚠️ Shell is /bin/sh (POSIX). NOT bash. Avoid:
  • Process substitution: <(...) or >(...)
  • Bash arrays, [[ ]] tests, &> redirects
  • Heredoc with complex quoting: $(cat <<'EOF' ...)
For multi-line text or complex quoting, write to /tmp file first, then use --body-file or cat the file.

Best for quick commands (<2 minutes). For long-running tasks (builds, large tests, servers), use the /tmux skill instead.

Auto-backgrounding:
  • A command still running after 5s is detached as a background operation and returns a handle (e.g. "op-1") immediately.
  • Its output is delivered automatically as a follow-up message when it completes — you can end your turn or do other work meanwhile.
  • Do NOT wait or poll for it; the tool timeout still bounds it.

Timeout behavior:
  • Default: 120 seconds
  • Override: Set timeout parameter (in SECONDS, not milliseconds; max 14400 = 4h)
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
  - Long task with tmux: Use /tmux skill instead (e.g., builds, servers, large tests)

Workspace:
  - Use the change_workspace tool for any directory change that must persist across multiple commands (or after creating/selecting a git worktree)
  - cd <dir> && <command> is valid only for a one-off command - a bare cd does not persist the workspace
`
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
				"description": "Timeout in SECONDS (not milliseconds; default: 120, 0 for no timeout, max 14400). On timeout, command is killed.",
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
	if isBroadFilesystemSearch(command, t.workspace.GetCWD()) {
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

	// Block sleep inside while/until loops: `while ...; do sleep N; done`
	// runs indefinitely even with a small N, defeating the sleep guard and
	// the command timeout (which models often pass as milliseconds).
	if hasSleepLoop(command) {
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: "⛔ Blocked: sleep inside a while/until loop (`while ...; do sleep ...; done`) can run for an unbounded time.\n\n" +
					"Use one of these instead:\n" +
					"  • Wait for an ai run to finish its turn:  ai watch --id <id> --follow --pretty\n" +
					"    (note: `ai serve` processes are long-lived servers that do NOT exit when\n" +
					"     their turn completes — never wait for their PID to die; use watch for turn completion)\n" +
					"  • Wait for a tmux session:  ~/.ai/skills/tmux/bin/tmux_wait.sh <session> [timeout]\n" +
					"  • Bounded one-shot wait (no loop):  sleep 6\n\n" +
					"For anything longer, use the /tmux skill for background management.",
			},
		}, nil
	}

	if isBareCDCommand(command) {
		return nil, fmt.Errorf("bare 'cd' only affects this shell subprocess and does not persist workspace. For directory changes that must span multiple commands (e.g. after creating/selecting a git worktree), use the change_workspace tool. 'cd <dir> && <command>' is valid only for a one-off command")
	}

	// Transform sudo commands: rewrite to sudo -S -p '' with the password
	// piped when SUDO_PASSWORD is set, or to sudo -n (non-interactive,
	// never blocks) otherwise. Must happen after all safety guards (which
	// check the original command) and before exec.CommandContext below.
	sudoResult := transformSudoCommand(command)
	execCommand := sudoResult.command

	// Get current working directory from workspace
	cwd := t.workspace.GetCWD()

	// Handle timeout parameter (default: 120 seconds)
	execTimeout := t.execTimeout
	var timeoutWarning string
	if timeoutArg, ok := args["timeout"].(float64); ok {
		if timeoutArg > 0 {
			// Compare in seconds BEFORE converting to nanoseconds: a huge
			// value (e.g. 10000000000) overflows time.Duration (int64 ns)
			// into a negative number, which would skip the cap and fall
			// through to the no-timeout branch.
			if timeoutArg > float64(maxBashTimeout/time.Second) {
				timeoutWarning = fmt.Sprintf("⚠️ bash timeout clamped: requested %ds exceeds the %s cap, using %s.\nIf you meant milliseconds, this tool's timeout is in SECONDS (e.g. 120000 → 33h was clamped; pass 120 for 120s).\n\n", int64(timeoutArg), maxBashTimeout, maxBashTimeout)
				execTimeout = maxBashTimeout
				slog.Warn("[Bash] timeout parameter exceeds cap, clamped", "requested", int64(timeoutArg), "cappedTo", maxBashTimeout.Seconds(), "command", command)
			} else {
				execTimeout = time.Duration(timeoutArg) * time.Second
			}
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
	// bg is set once the command is detached into a background operation.
	// After that the command must outlive turn cancellation (Abort), so both
	// the deferred cancel and the parent-cancel link below skip it.
	var bg atomic.Bool
	defer func() {
		if !bg.Load() {
			cancel()
		}
	}()

	// Also cancel on parent context done (foreground phase only).
	if ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			if !bg.Load() {
				cancel()
			}
		}()
	}

	// Create command — use the sudo-transformed version if applicable.
	cmd := exec.CommandContext(cmdCtx, "/bin/sh", "-c", execCommand)
	cmd.Dir = cwd

	// Set process group to enable cleanup of entire process tree
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// When sudo needs a password piped to stdin, set up a pipe.
	// Declare stdinRead outside the if block so the deferred close
	// is visible regardless of which path we take.
	var stdinRead *os.File
	var stdinPipeWrite *os.File
	if sudoResult.passwordLines != "" {
		var pipeErr error
		stdinRead, stdinPipeWrite, pipeErr = os.Pipe()
		if pipeErr != nil {
			return nil, fmt.Errorf("failed to create stdin pipe: %w", pipeErr)
		}
		cmd.Stdin = stdinRead
		defer stdinRead.Close()
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
		if stdinPipeWrite != nil {
			stdinPipeWrite.Close()
		}
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

	// If sudo password is needed, write it to stdin on a goroutine to avoid
	// pipe-buffer deadlocks (the child may not read stdin until its stdout
	// buffer fills, or vice versa).
	if stdinPipeWrite != nil {
		go func() {
			defer stdinPipeWrite.Close()
			if _, err := io.WriteString(stdinPipeWrite, sudoResult.passwordLines); err != nil {
				slog.Debug("[Bash] sudo password write error (child may have exited early)", "error", err)
			}
		}()
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
	// activity signals that output arrived after the main process exited;
	// it re-arms the idle grace timer below so we don't truncate a slow tail.
	activity := make(chan struct{}, 1)
	streamPipe := func(reader io.Reader) {
		defer outputWG.Done()
		bufReader := bufio.NewReader(reader)
		for {
			line, err := bufReader.ReadString('\n')
			if len(line) > 0 {
				outputMu.Lock()
				output.WriteString(line)
				outputMu.Unlock()
				select {
				case activity <- struct{}{}:
				default:
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				// os.ErrClosed means we closed the read end ourselves (idle
				// grace / timeout path below) — not a real read error.
				if !errors.Is(err, os.ErrClosed) {
					outputMu.Lock()
					output.WriteString(fmt.Sprintf("stream read error: %v\n", err))
					outputMu.Unlock()
				}
				break
			}
		}
	}

	outputWG.Add(2)
	go streamPipe(stdoutRead)
	go streamPipe(stderrRead)
	streamDone := make(chan struct{})
	go func() {
		outputWG.Wait()
		close(streamDone)
	}()

	// Run the wait/drain/assemble phase in a goroutine so Execute can detach
	// the command into the background once backgroundThreshold elapses,
	// rather than holding the agent's turn hostage. The channel is buffered
	// so the goroutine never blocks on a late reader (background case).
	outcomeCh := make(chan []agentctx.ContentBlock, 1)
	go func() {
		// Wait for command to finish. Since we use our own os.Pipe(),
		// cmd.Wait() will NOT close our pipes — we control the lifecycle.
		err := cmd.Wait()

		// Drain stdout/stderr without hanging on descendant-held pipes.
		//
		// After the main shell exits, a background descendant (e.g. a subshell
		// waiting on a daemon started with `cmd &`) can keep the pipe write ends
		// open, so EOF never arrives. Blocking on outputWG.Wait() would hang the
		// tool forever (the deadline check below is only reached after the
		// drain). Instead, finalize once the pipes fall idle: the grace timer is
		// re-armed on every chunk, so actively-writing descendants keep us
		// reading, while a quiet holder of the pipe releases us after the grace
		// period elapses. The daemon itself is left running — it is not part of
		// the command.
		idleTimer := time.NewTimer(exitGracePeriod)
		defer idleTimer.Stop()
		drainDone := make(chan struct{})
		go func() {
			defer close(drainDone)
			for {
				select {
				case <-streamDone:
					// Both pipes reached EOF — full output captured.
					return
				case <-activity:
					// Output still arriving — defer finalizing so we don't
					// truncate the tail.
					if !idleTimer.Stop() {
						select {
						case <-idleTimer.C:
						default:
						}
					}
					idleTimer.Reset(exitGracePeriod)
				case <-idleTimer.C:
					// Pipes idle — release the blocked stream goroutines.
					stdoutRead.Close()
					stderrRead.Close()
					<-streamDone
					return
				case <-cmdCtx.Done():
					// Deadline/cancel fired while draining (e.g. a background
					// process keeps producing output past the timeout). Release
					// the pipes so the timeout/cancel handling below proceeds.
					stdoutRead.Close()
					stderrRead.Close()
					<-streamDone
					return
				}
			}
		}()
		<-drainDone

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

			resultText := timeoutWarning + fmt.Sprintf(
				"Command timed out after %v and was terminated.\n"+
					"Partial output (%d bytes):\n%s\n\n"+
					"For long-running tasks, use the /tmux skill for proper background management.",
				execTimeout, output.Len(), output.String())

			// When the command pipes into head/tail, the buffered partial output
			// dies with the process group and partial output above is empty.
			if strings.Contains(command, "|") {
				resultText += "\n\ntip: piping to head/tail loses partial output on kill; redirect to a file instead (cmd > /tmp/out.log 2>&1)"
			}

			outcomeCh <- []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: resultText,
				},
			}
			return
		}

		if cmdCtx.Err() == context.Canceled {
			slog.Info("[Bash] Command canceled",
				"command", command,
				"elapsed", elapsed.Seconds(),
				"outputSize", output.Len())

			resultText := fmt.Sprintf("Command canceled.\n\nOutput:\n%s", output.String())
			outcomeCh <- []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: resultText,
				},
			}
			return
		}

		slog.Info("[Bash] Command completed",
			"command", command,
			"exitCode", cmd.ProcessState.ExitCode(),
			"elapsed", elapsed.Seconds(),
			"outputSize", output.Len())

		// Handle sudo password issues: add helpful hints when sudo fails
		// in non-interactive contexts.
		outputText := output.String()
		if hint := SudoHint(outputText); hint != "" {
			outputText += hint
		}

		// Build result
		var result strings.Builder
		if timeoutWarning != "" {
			result.WriteString(timeoutWarning)
		}
		result.WriteString(outputText)
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

		outcomeCh <- []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: result.String(),
			},
		}
	}()

	// Foreground wait: if the command finishes within the threshold, return
	// its output directly (the common case). Otherwise detach it into a
	// background operation and return a handle immediately; the result is
	// injected as a wake-up when the command finishes.
	bgTimer := time.NewTimer(backgroundThreshold)
	defer bgTimer.Stop()
	select {
	case blocks := <-outcomeCh:
		return blocks, nil
	case <-bgTimer.C:
		bg.Store(true)
		opID := fmt.Sprintf("op-%d", t.bgOpCounter.Add(1))
		go func() {
			blocks := <-outcomeCh
			cancel()
			if t.onBackgroundComplete != nil {
				t.onBackgroundComplete(opID, blocksText(blocks))
			}
		}()
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: backgroundHandleText(opID, command),
			},
		}, nil
	}
}

// SetTimeout sets the timeout for command execution.
func (t *BashTool) SetTimeout(timeout time.Duration) {
	t.execTimeout = timeout
}

// SetBackgroundNotifier sets the callback invoked when a backgrounded command
// completes. The app wires it to enqueue a wake-up event so the agent resumes
// with the command's output.
func (t *BashTool) SetBackgroundNotifier(fn func(opID, result string)) {
	t.onBackgroundComplete = fn
}

// blocksText flattens tool content blocks into plain text for a
// background-completion notification.
func blocksText(blocks []agentctx.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if tc, ok := b.(agentctx.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// backgroundHandleText is the tool result returned when a command is detached
// into a background operation.
func backgroundHandleText(opID, command string) string {
	return fmt.Sprintf(
		"⏳ Command still running after %s — detached as background operation %s.\n"+
			"Command: %s\n\n"+
			"It will finish on its own (bounded by the tool timeout) and its output "+
			"will be delivered as a new message at the next model-call boundary, "+
			"i.e. after your current response (and any tool calls it makes). "+
			"Work on something else meanwhile — do NOT wait or poll for it.",
		backgroundThreshold, opID, command)
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

// hasSleepLoop reports whether the command sleeps inside a while/until or
// for loop, e.g. `while kill -0 $PID 2>/dev/null; do sleep 2; done`. Such
// loops run for an unbounded time even though each individual sleep is
// small, which bypasses the single-sleep guard. Only the captured loop
// body (between `do` and the first `done`) is inspected, so a `sleep`
// after `done` is not a false positive, and the sleep match accepts any
// argument (variables, decimals) — the loop is what makes the wait
// unbounded, not the duration.
func hasSleepLoop(command string) bool {
	for _, re := range [](*regexp.Regexp){loopBodyRe, forLoopBodyRe} {
		if m := re.FindStringSubmatch(command); m != nil && sleepCmdRe.MatchString(m[1]) {
			return true
		}
	}
	return false
}

var (
	// The captured group is the loop body between `do` and the first `done`
	// (non-greedy), so a `sleep` after `done` is never a false positive and
	// the sleep is matched even when its duration is a variable or decimal.
	loopBodyRe    = regexp.MustCompile(`(?s)\b(?:while|until)\b.*?\bdo\b(.*?)\bdone\b`)
	forLoopBodyRe = regexp.MustCompile(`(?s)\bfor\b.*?\bdo\b(.*?)\bdone\b`)
	sleepCmdRe    = regexp.MustCompile(`\bsleep\b\s+\S`)
)

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
//   - find /*           (glob expands to all of root)
//   - find -- /         (double-dash separator)
//   - find ~
//   - find ~/*          (glob expands to all of home)
//   - find $HOME
//
// Allowed:
//   - find /tmp -name x   (specific subdirectory)
//   - find ~/project      (specific subdirectory under home)
//   - find .              (current directory)
func isBroadFilesystemSearch(command string, cwd string) bool {
	// find /, find /*, find -- /  — root path or glob of root.
	// Matches when / is immediately followed by: whitespace, pipe, semicolon,
	// &, ), * (glob), or end of string. Does NOT match /tmp, /home/user, etc.
	rootRe := regexp.MustCompile(`\bfind\s+(--\s+)?/([\s|;&)*]|$)`)

	// find ~, find ~/*, find -- ~  — home path or glob of home.
	// Matches when ~ is followed by a terminator (whitespace, pipe, etc.),
	// end of string, /* (glob), or a bare trailing slash (~/ at end or ~/ |).
	// Does NOT match ~/project.
	homeTildeRe := regexp.MustCompile(`\bfind\s+(--\s+)?~([\s|;&)]|$|/\*|/(?:[\s|;&)]|$))`)

	// find $HOME or find ${HOME}
	homeEnvRe := regexp.MustCompile(`\bfind\s+\$\{?HOME\}?\b`)

	if rootRe.MatchString(command) ||
		homeTildeRe.MatchString(command) ||
		homeEnvRe.MatchString(command) {
		return true
	}

	// find /Users/genius — home directory via absolute path.
	// Same risk as find ~, but bypasses the tilde check. Block it too,
	// unless the workspace cwd IS the home directory (equivalent to find .).
	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" || homeDir == "/" {
		return false
	}
	if cwd != "" && cwd == homeDir {
		return false
	}
	homeEscaped := regexp.QuoteMeta(homeDir)
	homeAbsRe := regexp.MustCompile(`\bfind\s+(--\s+)?` + homeEscaped + `([\s|;&)]|$|/\*|/(?:[\s|;&)]|$))`)

	return homeAbsRe.MatchString(command)
}

// isDangerousTmuxKill checks if the command contains tmux kill-server,
// which destroys the entire tmux server and all sessions (including the agent's own).
func isDangerousTmuxKill(command string) bool {
	return regexp.MustCompile(`\btmux\s+kill-server\b`).MatchString(command)
}
