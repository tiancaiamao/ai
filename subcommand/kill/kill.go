package kill

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tiancaiamao/ai/subcommand/helpers"
	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

// resolveRunID resolves the target run given an optional ID flag.
// If id is empty, it auto-selects by cwd. If id is a partial prefix,
// it uses FindByPrefix.
func resolveRunID(baseDir, id string) (*tui.RunMeta, error) {
	if id != "" {
		// Try exact match first: look for run.json directly.
		exactPath := tui.RunMetaPath(baseDir, id)
		if meta, err := tui.LoadRunMeta(exactPath); err == nil && tui.IsRunning(meta) {
			return meta, nil
		}

		// Try prefix match.
		matches, err := tui.FindByPrefix(baseDir, id)
		if err != nil {
			return nil, fmt.Errorf("prefix match for %q: %w", id, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("no running run found matching %q", id)
		}
		// FindByPrefix returns at most 1 match on success (errors on multiple).
		m := matches[0]
		if !tui.IsRunning(&m) {
			return nil, fmt.Errorf("run %s is not running (status: %s)", m.ID, m.Status)
		}
		return &m, nil
	}

	// Auto-select by cwd.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get cwd: %w", err)
	}

	matches, err := tui.FindRunningByCwd(baseDir, cwd)
	if err != nil {
		return nil, fmt.Errorf("find running by cwd: %w", err)
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no running instances found in %s", cwd)
	case 1:
		return &matches[0], nil
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		return nil, fmt.Errorf("multiple running instances in %s (IDs: %v), use --id to disambiguate", cwd, ids)
	}
}

func KillSubcommand() {
	fs := flag.NewFlagSet("kill", flag.ExitOnError)
	idFlag := fs.String("id", "", "run ID or prefix (auto-selects by cwd if omitted)")
	forceFlag := fs.Bool("force", false, "send SIGKILL instead of graceful abort")
	fs.Parse(os.Args[1:])

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get home directory: %v\n", err)
		os.Exit(1)
	}
	baseDir := filepath.Join(home, ".ai")

	meta, err := helpers.ResolveRunID(baseDir, *idFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if *forceFlag {
		killRun(meta, baseDir)
		return
	}

	// Try graceful shutdown via socket first.
	sockPath := tui.SocketPath(baseDir, meta.ID)
	killed := trySocketAbort(sockPath)
	if killed {
		// Wait briefly for process to exit and update its own state.
		waitForExit(meta.PID, 5*time.Second)
		// Re-check: if process is still alive, force kill.
		if processAlive(meta.PID) {
			killRun(meta, baseDir)
		} else {
			fmt.Printf("run %s stopped\n", meta.ID)
		}
		return
	}

	// Socket not available — fall back to signal-based kill.
	killRun(meta, baseDir)
}

// trySocketAbort attempts to send an "abort" command via the Unix socket.
// Returns true if the socket responded successfully.
func trySocketAbort(sockPath string) bool {
	conn, err := net.DialTimeout("unix", sockPath, 3*time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return false
	}

	cmd := tui.Command{Type: "abort"}
	data, err := json.Marshal(cmd)
	if err != nil {
		return false
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return false
	}

	// Read one line-delimited response.
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return false
	}

	var resp tui.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return false
	}

	return resp.OK
}

// killRun sends SIGKILL to the run's process and updates run.json.
func killRun(meta *tui.RunMeta, baseDir string) {
	proc, err := os.FindProcess(meta.PID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot find process %d: %v\n", meta.PID, err)
		os.Exit(1)
	}

	// Send SIGKILL.
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to kill process %d: %v\n", meta.PID, err)
		os.Exit(1)
	}

	// Update run.json.
	meta.Status = tui.StatusKilled
	meta.FinishedAt = time.Now().Unix()
	metaPath := tui.RunMetaPath(baseDir, meta.ID)
	if err := tui.SaveRunMeta(meta, metaPath); err != nil {
		fmt.Fprintf(os.Stderr, "warn: failed to update run.json: %v\n", err)
	}

	// Also kill the process group to clean up child processes, but only
	// when this PID is actually the process-group leader.
	if pgid, err := syscall.Getpgid(meta.PID); err == nil && pgid == meta.PID {
		_ = syscall.Kill(-meta.PID, syscall.SIGKILL)
	}

	fmt.Printf("run %s killed (pid %d)\n", meta.ID, meta.PID)
}

// waitForExit waits up to timeout for the process to exit.
func waitForExit(pid int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// processAlive checks if a process with the given PID is still running.
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
