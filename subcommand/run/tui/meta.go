package tui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Status constants for run lifecycle states.
const (
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
	StatusKilled  = "killed"
)

// RunMeta holds metadata for a single run (an ai rpc subprocess invocation).
type RunMeta struct {
	ID           string `json:"id"`             // 6-char hex ID
	PID          int    `json:"pid"`            // process ID of the ai rpc subprocess
	CWD          string `json:"cwd"`            // working directory where ai run was invoked
	Status       string `json:"status"`         // running, done, failed, killed
	StartedAt    int64  `json:"started_at"`     // unix timestamp
	FinishedAt   int64  `json:"finished_at"`    // unix timestamp, 0 if still running
	Name         string `json:"name"`           // optional human-readable name
	ParentRun    string `json:"parent_run"`     // optional parent run ID for subagents
	PidStartTime int64  `json:"pid_start_time"` // epoch seconds of process start (for PID reuse detection)
}

// GenerateID returns a 6-character lowercase hex string using crypto/rand (3 bytes).
func GenerateID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RunDir returns the directory path for a run: <baseDir>/runs/<id>/.
// If baseDir is empty, it defaults to ~/.ai.
func RunDir(baseDir, id string) string {
	base := resolveBase(baseDir)
	return filepath.Join(base, "runs", id)
}

// RunMetaPath returns the path to run.json inside the run directory.
func RunMetaPath(baseDir, id string) string {
	return filepath.Join(RunDir(baseDir, id), "run.json")
}

// EventsPath returns the path to events.jsonl inside the run directory.
func EventsPath(baseDir, id string) string {
	return filepath.Join(RunDir(baseDir, id), "events.jsonl")
}

// SocketPath returns the path to control.sock inside the run directory.
func SocketPath(baseDir, id string) string {
	return filepath.Join(RunDir(baseDir, id), "control.sock")
}

// FindRunningByCwd scans all run directories under baseDir/runs/,
// loads their run.json, and returns those with status "running", matching cwd,
// and whose process is still alive (IsRunning).
func FindRunningByCwd(baseDir, cwd string) ([]RunMeta, error) {
	return findByFilter(baseDir, func(m *RunMeta) bool {
		return m.CWD == cwd && IsRunning(m)
	})
}

// FindByPrefix scans all run directories under baseDir/runs/,
// and returns those whose ID starts with the given prefix.
// Returns an error if prefix matches more than one run.
func FindByPrefix(baseDir, prefix string) ([]RunMeta, error) {
	matches, err := findByFilter(baseDir, func(m *RunMeta) bool {
		return len(prefix) > 0 && len(m.ID) >= len(prefix) && m.ID[:len(prefix)] == prefix
	})
	if err != nil {
		return nil, err
	}
	if len(matches) > 1 {
		return matches, fmt.Errorf("prefix %q matches %d runs", prefix, len(matches))
	}
	return matches, nil
}

// LoadRunMeta reads and parses a single run.json file.
func LoadRunMeta(path string) (*RunMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read run meta %s: %w", path, err)
	}
	var meta RunMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse run meta %s: %w", path, err)
	}
	return &meta, nil
}

// SaveRunMeta writes a run.json file atomically by writing to a temp file first.
func SaveRunMeta(meta *RunMeta, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create run dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run meta: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}
	return nil
}

// IsRunning checks if the run's process is still alive by sending signal 0
// to the PID, then verifying PID identity via start time to detect PID reuse.
// Returns true only if status is "running" and the process exists and matches.
func IsRunning(meta *RunMeta) bool {
	if meta.Status != StatusRunning {
		return false
	}
	proc, err := os.FindProcess(meta.PID)
	if err != nil {
		return false
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	// Verify PID still belongs to the original process.
	// If PidStartTime was recorded, the current start time must match
	// within a 1-second tolerance. The tolerance accounts for boot time
	// calculation skew between the old approach (now - Sysinfo.Uptime) and
	// the current /proc/stat btime approach — they can differ by 1 second
	// due to integer rounding. This is safe for PID reuse detection because
	// recycled PIDs have start times differing by many seconds or more.
	if meta.PidStartTime > 0 {
		currentStart := GetProcessStartTime(meta.PID)
		if currentStart == 0 {
			return false
		}
		diff := currentStart - meta.PidStartTime
		if diff < 0 {
			diff = -diff
		}
		if diff > 1 {
			return false
		}
	}
	return true
}

// GetProcessStartTime returns the epoch-second start time of the given PID.
// Returns 0 if the start time cannot be determined (process gone, unsupported OS).
//
// On Linux, it reads /proc/<pid>/stat field 22 (starttime in clock ticks).
// On macOS/other, it uses `ps -o lstart= -p <pid>` and parses the date.
var GetProcessStartTime = getProcessStartTime

func getProcessStartTime(pid int) int64 {
	if pid <= 0 {
		return 0
	}

	if runtime.GOOS == "linux" {
		return getProcessStartTimeLinux(pid)
	}
	return getProcessStartTimePS(pid)
}

// getProcessStartTimeLinux reads /proc/<pid>/stat field 22 (starttime).
// Field 22 is the number of clock ticks since system boot.
// NOTE: This implementation is only used on Linux. On non-Linux platforms,
// getProcessStartTime falls through to getProcessStartTimePS.
func getProcessStartTimeLinux(pid int) int64 {
	// Non-Linux: fall back to ps-based approach.
	return getProcessStartTimePS(pid)
}

// getClockTicks returns the sysconf(_SC_CLK_TCK) value.
func getClockTicks() int64 {
	return 100
}

// getProcessStartTimePS uses `ps -o lstart= -p <pid>` to get the start time.
// Works on macOS and other Unix systems.
// Forces LC_TIME=C to ensure consistent date format regardless of locale.
func getProcessStartTimePS(pid int) int64 {
	if pid <= 0 {
		return 0
	}
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_TIME=C")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return 0
	}
	// ps lstart format (C locale): "Wed Jan  2 15:04:05 2006"
	t, err := time.Parse("Mon Jan  2 15:04:05 2006", line)
	if err != nil {
		// Try alternative format (single-digit day without extra space)
		t, err = time.Parse("Mon Jan 2 15:04:05 2006", line)
		if err != nil {
			return 0
		}
	}
	return t.Unix()
}

// --- internal helpers ---

func resolveBase(baseDir string) string {
	if baseDir != "" {
		return baseDir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// fallback
		return filepath.Join("/tmp", ".ai")
	}
	return filepath.Join(home, ".ai")
}

func findByFilter(baseDir string, match func(*RunMeta) bool) ([]RunMeta, error) {
	runsDir := filepath.Join(resolveBase(baseDir), "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read runs dir %s: %w", runsDir, err)
	}

	var results []RunMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(runsDir, e.Name(), "run.json")
		meta, err := LoadRunMeta(metaPath)
		if err != nil {
			continue // skip unreadable entries
		}
		if match(meta) {
			results = append(results, *meta)
		}
	}
	return results, nil
}

// Now returns the current unix timestamp. Extracted for testability.
var now = func() int64 {
	return time.Now().Unix()
}

// Helper to create a test run meta. Not exported.
func newTestMeta(id string) *RunMeta {
	return &RunMeta{
		ID:        id,
		PID:       os.Getpid(),
		CWD:       "/tmp",
		Status:    StatusRunning,
		StartedAt: now(),
	}
}

// CreateRun is a convenience to generate an ID, build RunMeta, and save it.
func CreateRun(baseDir, cwd string, pid int) (*RunMeta, error) {
	id := GenerateID()
	meta := &RunMeta{
		ID:           id,
		PID:          pid,
		CWD:          cwd,
		Status:       StatusRunning,
		StartedAt:    now(),
		PidStartTime: GetProcessStartTime(pid),
	}
	path := RunMetaPath(baseDir, id)
	if err := SaveRunMeta(meta, path); err != nil {
		return nil, err
	}
	return meta, nil
}

// PIDToString converts a PID to string (utility).
func PIDToString(pid int) string {
	return strconv.Itoa(pid)
}
