// Package agent manages agent lifecycle: spawn, status, steer, abort, prompt,
// kill, shutdown, rm, output, wait. All operations target .ag/agents/<id>/.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/genius/ag/internal/storage"
)

// Valid agent ID: alphanumeric, underscore, hyphen, max 64 chars
var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidateID checks that an agent ID is valid.
func ValidateID(id string) error {
	if !validID.MatchString(id) {
		return fmt.Errorf("invalid agent ID %q: must be 1-64 chars, alphanumeric/underscore/hyphen only", id)
	}
	return nil
}

// AgentDir returns the storage directory for the given agent.
func AgentDir(id string) string {
	return storage.AgentDir(id)
}

// Exists returns true if the agent directory exists.
func Exists(id string) bool {
	return storage.Exists(AgentDir(id))
}

// EnsureExists validates agent ID and checks the agent directory exists.
func EnsureExists(id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	if !Exists(id) {
		return fmt.Errorf("agent not found: %s", id)
	}
	return nil
}

// AgentEntry is used by List to return agent summary info.
type AgentEntry struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Backend   string `json:"backend,omitempty"`
	StartedAt int64  `json:"startedAt,omitempty"`
}

// List returns all agents with their current status.
// Reads from activity.json (no socket calls, fast).
func List() ([]AgentEntry, error) {
	agentsDir := filepath.Join(storage.BaseDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []AgentEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		activity, err := ReadActivity(id)
		if err != nil {
			// Agent dir exists but no activity.json yet — show as unknown
			result = append(result, AgentEntry{ID: id, Status: "unknown"})
			continue
		}

		// Lazy status reconciliation: if activity.json says "running" but the
		// process is dead, update the status so callers see the real state.
		if activity.Status == "running" && activity.Pid > 0 {
					if !IsProcessAlive(activity.Pid, activity.PidStartTime) {
				activity.Status = "done"
				if activity.FinishedAt == 0 {
					activity.FinishedAt = time.Now().Unix()
				}
				actPath := filepath.Join(AgentDir(id), "activity.json")
				_ = storage.AtomicWriteJSON(actPath, activity)
			}
		}

		result = append(result, AgentEntry{
			ID:        id,
			Status:    string(activity.Status),
			Backend:   activity.Backend,
			StartedAt: activity.StartedAt,
		})
	}
	return result, nil
}

// IsProcessAlive checks if a process with the given PID is still running.
// It distinguishes between running, zombie, exited, and PID-reused processes:
//   - Running → true
//   - Zombie (Z state) → false (process exited but parent hasn't reaped)
//   - Exited/nonexistent → false
//   - PID reused (start time mismatch) → false
//
// If startTimeEpoch > 0, the function additionally verifies that the current
// process at this PID started at the same time as the original, preventing
// false positives from OS PID recycling.
//
// On macOS/Linux, os.FindProcess always succeeds, and proc.Signal(0) returns
// nil for zombie processes (the PID still exists in the process table).
// We use syscall.Wait4 with WNOHANG to detect zombies without blocking.
func IsProcessAlive(pid int, startTimeEpoch ...int64) bool {
	if pid <= 0 {
		return false
	}

	// Fast check: if signal fails, process is definitely gone.
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}

	// Signal succeeded — but on Unix, zombies also accept signal(0).
	// Use Wait4 with WNOHANG to distinguish zombie from running.
	var status syscall.WaitStatus
	var rusage syscall.Rusage
	wpid, err := syscall.Wait4(pid, &status, syscall.WNOHANG, &rusage)
	if err != nil {
		// ECHILD: not our child process — can't wait4 it.
		// This is the common case for spawned agents (different process group).
		// Fall back to checking /proc or ps for zombie state.
		if isZombieFromPS(pid) {
			return false
		}
	} else if wpid > 0 {
		// wpid > 0: child exited, was reaped by Wait4. It's dead.
		return false
	}
	// wpid == 0 or ECHILD with non-zombie: process appears alive.

	// Verify PID hasn't been recycled by checking start time.
	if len(startTimeEpoch) > 0 && startTimeEpoch[0] > 0 {
		currentStart := getProcessStartTime(pid)
		if currentStart == 0 || currentStart != startTimeEpoch[0] {
			return false
		}
	}

	return true
}

// isZombieFromPS checks if a process is in zombie state using ps.
// This handles the case where the process is not our direct child
// (so Wait4 returns ECHILD) but may be a zombie.
func isZombieFromPS(pid int) bool {
	// ps -o stat= -p <pid> prints the 2-4 char state string.
	// "Z" or "Z+" means zombie. Empty output means process doesn't exist.
	out, err := exec.Command("ps", "-o", "stat=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		// ps failed — process likely gone entirely.
		return false
	}
	stat := strings.TrimSpace(string(out))
	if stat == "" {
		return false // process doesn't exist
	}
	// Check for zombie state: starts with 'Z'
	return strings.HasPrefix(stat, "Z")
}

// ReadActivity reads activity.json for an agent.
func ReadActivity(id string) (*Activity, error) {
	path := filepath.Join(AgentDir(id), "activity.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a Activity
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Activity mirrors bridge.AgentActivity for use by CLI commands.
// Defined here to avoid circular imports (bridge imports storage, agent imports storage).
type Activity struct {
	Status       string `json:"status"`
	Pid          int    `json:"pid,omitempty"`
	StartedAt    int64  `json:"startedAt,omitempty"`
	FinishedAt   int64  `json:"finishedAt,omitempty"`
	PidStartTime int64  `json:"pidStartTime,omitempty"` // epoch seconds, for PID reuse detection
	Turns        int    `json:"turns"`
	TokensIn     int64  `json:"tokensIn"`
	TokensOut    int64  `json:"tokensOut"`
	TokensTotal  int64  `json:"tokensTotal"`
	LastTool     string `json:"lastTool,omitempty"`
	LastText     string `json:"lastText,omitempty"`
	Error        string `json:"error,omitempty"`
	Backend      string `json:"backend,omitempty"`
}

// IsTerminal returns true if the agent status is terminal (won't change).
func IsTerminal(status string) bool {
	return status == "done" || status == "failed" || status == "killed"
}

// timeNow returns current Unix timestamp.
func timeNow() int64 {
	return time.Now().Unix()
}

// getProcessStartTime returns the epoch-second start time of the given PID
// using `ps -o lstart= -p <pid>`. Returns 0 on error.
func getProcessStartTime(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	cmd := exec.Command("ps", "-o", "lstart=", "-p", fmt.Sprintf("%d", pid))
	cmd.Env = append(os.Environ(), "LC_TIME=C")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return 0
	}
	t, err := time.Parse("Mon Jan  2 15:04:05 2006", line)
	if err != nil {
		t, err = time.Parse("Mon Jan 2 15:04:05 2006", line)
		if err != nil {
			return 0
		}
	}
	return t.Unix()
}
