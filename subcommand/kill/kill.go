package kill

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	rpc "github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/subcommand/helpers"
	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

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

	// Graceful: cancel the in-flight turn via ACP. The serve process
	// exits on its own after the turn ends and updates run.json.
	if client, sid, err := rpc.DialACP(tui.SocketPath(baseDir, meta.ID)); err == nil {
		_ = client.Cancel(sid)
		client.Close()
		// Wait briefly for the process to exit.
		waitForExit(meta.PID, 5*time.Second)
		// If it's still alive, force kill.
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

	// Update run.json (the serve process cannot save its own state after
	// SIGKILL).
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
