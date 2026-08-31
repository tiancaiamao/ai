package tui

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateID_LengthAndFormat(t *testing.T) {
	id := GenerateID()
	assert.Len(t, id, 6, "ID should be 6 characters")
	for _, c := range id {
		assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"ID should be lowercase hex, got: %c", c)
	}
}

func TestGenerateID_Unique(t *testing.T) {
	// 100 iterations keeps collision probability at ~0.03% (birthday problem
	// with 2^24 space). 1000 iterations would raise it to ~3%, causing flaky CI.
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateID()
		assert.False(t, seen[id], "duplicate ID generated: %s", id)
		seen[id] = true
	}
}

func TestRunDir(t *testing.T) {
	// With explicit baseDir
	dir := RunDir("/tmp/.ai", "abc123")
	assert.Equal(t, "/tmp/.ai/runs/abc123", dir)

	// With empty baseDir (should use home)
	home, _ := os.UserHomeDir()
	dir = RunDir("", "abc123")
	assert.Equal(t, filepath.Join(home, ".ai", "runs", "abc123"), dir)
}

func TestRunMetaPath(t *testing.T) {
	p := RunMetaPath("/tmp/.ai", "abc123")
	assert.Equal(t, "/tmp/.ai/runs/abc123/run.json", p)
}

func TestEventsPath(t *testing.T) {
	p := EventsPath("/tmp/.ai", "abc123")
	assert.Equal(t, "/tmp/.ai/runs/abc123/events.jsonl", p)
}

func TestSocketPath(t *testing.T) {
	p := SocketPath("/tmp/.ai", "abc123")
	assert.Equal(t, "/tmp/.ai/runs/abc123/control.sock", p)
}

func TestSaveAndLoadRunMeta(t *testing.T) {
	tmpDir := t.TempDir()
	meta := &RunMeta{
		ID:        "abc123",
		PID:       12345,
		CWD:       "/home/user/project",
		Status:    StatusRunning,
		StartedAt: 1700000000,
		Name:      "test run",
		ParentRun: "",
	}
	path := filepath.Join(tmpDir, "runs", "abc123", "run.json")

	err := SaveRunMeta(meta, path)
	require.NoError(t, err)

	loaded, err := LoadRunMeta(path)
	require.NoError(t, err)
	assert.Equal(t, meta, loaded)
}

func TestFindRunningByCwd(t *testing.T) {
	tmpDir := t.TempDir()

	// Create three runs: two running with same cwd, one done, one running with different cwd
	runs := []RunMeta{
		{ID: "aa0001", PID: 1001, CWD: "/project/a", Status: StatusRunning, StartedAt: 1},
		{ID: "aa0002", PID: 1002, CWD: "/project/a", Status: StatusRunning, StartedAt: 2},
		{ID: "aa0003", PID: 1003, CWD: "/project/a", Status: StatusDone, StartedAt: 3},
		{ID: "aa0004", PID: 1004, CWD: "/project/b", Status: StatusRunning, StartedAt: 4},
	}

	for _, r := range runs {
		path := RunMetaPath(tmpDir, r.ID)
		err := SaveRunMeta(&r, path)
		require.NoError(t, err)
	}

	// Find running in /project/a -> should return aa0001 and aa0002
	// Note: FindRunningByCwd now checks IsRunning (process alive), so use
	// os.Getpid() for runs that should match.
	myPID := os.Getpid()
	aliveRuns := []RunMeta{
		{ID: "aa0001", PID: myPID, CWD: "/project/a", Status: StatusRunning, StartedAt: 1},
		{ID: "aa0002", PID: myPID, CWD: "/project/a", Status: StatusRunning, StartedAt: 2},
	}
	for _, r := range aliveRuns {
		path := RunMetaPath(tmpDir, r.ID)
		err := SaveRunMeta(&r, path)
		require.NoError(t, err)
	}

	matches, err := FindRunningByCwd(tmpDir, "/project/a")
	require.NoError(t, err)
	assert.Len(t, matches, 2)

	ids := map[string]bool{}
	for _, m := range matches {
		ids[m.ID] = true
	}
	assert.True(t, ids["aa0001"])
	assert.True(t, ids["aa0002"])

	// Find running in /project/b -> should return aa0004
	aliveB := RunMeta{ID: "aa0004", PID: myPID, CWD: "/project/b", Status: StatusRunning, StartedAt: 4}
	err = SaveRunMeta(&aliveB, RunMetaPath(tmpDir, "aa0004"))
	require.NoError(t, err)

	matches, err = FindRunningByCwd(tmpDir, "/project/b")
	require.NoError(t, err)
	assert.Len(t, matches, 1)
	assert.Equal(t, "aa0004", matches[0].ID)

	// Find running in /project/c -> empty
	matches, err = FindRunningByCwd(tmpDir, "/project/c")
	require.NoError(t, err)
	assert.Len(t, matches, 0)
}

func TestFindByPrefix(t *testing.T) {
	tmpDir := t.TempDir()

	runs := []RunMeta{
		{ID: "aabb01", PID: 2001, CWD: "/x", Status: StatusRunning, StartedAt: 1},
		{ID: "aabb02", PID: 2002, CWD: "/x", Status: StatusRunning, StartedAt: 2},
		{ID: "ccdd01", PID: 2003, CWD: "/x", Status: StatusRunning, StartedAt: 3},
	}

	for _, r := range runs {
		path := RunMetaPath(tmpDir, r.ID)
		err := SaveRunMeta(&r, path)
		require.NoError(t, err)
	}

	// Unique prefix "ccdd" -> one match
	matches, err := FindByPrefix(tmpDir, "ccdd")
	require.NoError(t, err)
	assert.Len(t, matches, 1)
	assert.Equal(t, "ccdd01", matches[0].ID)

	// Ambiguous prefix "aab" -> error
	matches, err = FindByPrefix(tmpDir, "aab")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "matches 2 runs")
	assert.Len(t, matches, 2)

	// No match
	matches, err = FindByPrefix(tmpDir, "zzzz")
	assert.NoError(t, err)
	assert.Len(t, matches, 0)

	// Empty prefix -> no match (empty prefix is not valid)
	matches, err = FindByPrefix(tmpDir, "")
	assert.NoError(t, err)
	assert.Len(t, matches, 0)
}

func TestFindByPrefix_EmptyRunsDir(t *testing.T) {
	tmpDir := t.TempDir()
	// runs dir doesn't exist yet
	matches, err := FindByPrefix(tmpDir, "abc")
	assert.NoError(t, err)
	assert.Len(t, matches, 0)
}

func TestIsRunning_CurrentProcess(t *testing.T) {
	meta := &RunMeta{
		ID:        "test01",
		PID:       os.Getpid(), // current process is alive
		CWD:       "/tmp",
		Status:    StatusRunning,
		StartedAt: 1,
	}
	assert.True(t, IsRunning(meta))
}

func TestIsRunning_NotRunningStatus(t *testing.T) {
	meta := &RunMeta{
		ID:        "test02",
		PID:       os.Getpid(),
		CWD:       "/tmp",
		Status:    StatusDone,
		StartedAt: 1,
	}
	assert.False(t, IsRunning(meta))
}

func TestIsRunning_DeadProcess(t *testing.T) {
	// Use a PID that very likely doesn't exist
	meta := &RunMeta{
		ID:        "test03",
		PID:       99999999,
		CWD:       "/tmp",
		Status:    StatusRunning,
		StartedAt: 1,
	}
	// Signal(0) on a non-existent process should return an error
	// (ESRCH or permission denied depending on OS)
	result := IsRunning(meta)
	assert.False(t, result)
}

func TestIsRunning_ProcessGroupLeader(t *testing.T) {
	// PID 1 (init/launchd) should exist but we may not have permission to signal it
	// Just verify it doesn't panic
	meta := &RunMeta{
		ID:        "test04",
		PID:       1,
		CWD:       "/tmp",
		Status:    StatusRunning,
		StartedAt: 1,
	}
	// Don't assert result since it depends on permissions, just don't panic
	_ = IsRunning(meta)
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "running", StatusRunning)
	assert.Equal(t, "done", StatusDone)
	assert.Equal(t, "failed", StatusFailed)
	assert.Equal(t, "killed", StatusKilled)
}

func TestLoadRunMeta_FileNotFound(t *testing.T) {
	_, err := LoadRunMeta("/nonexistent/path/run.json")
	assert.Error(t, err)
}

func TestLoadRunMeta_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "bad.json")
	err := os.WriteFile(path, []byte("not json"), 0o644)
	require.NoError(t, err)
	_, err = LoadRunMeta(path)
	assert.Error(t, err)
}

func TestSaveRunMeta_Atomic(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "runs", "test01", "run.json")

	meta := &RunMeta{ID: "test01", PID: 42, CWD: "/tmp", Status: StatusRunning, StartedAt: 1}
	err := SaveRunMeta(meta, path)
	require.NoError(t, err)

	// tmp file should not exist
	_, err = os.Stat(path + ".tmp")
	assert.True(t, os.IsNotExist(err), "tmp file should be cleaned up")

	// Final file should exist
	_, err = os.Stat(path)
	assert.NoError(t, err)
}

// Verify syscall.Signal(0) behavior as a sanity check for IsRunning.
func TestSyscallSignalZero(t *testing.T) {
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	err = proc.Signal(syscall.Signal(0))
	assert.NoError(t, err, "signal 0 on own process should succeed")
}

func TestGetProcessStartTime_CurrentProcess(t *testing.T) {
	// Should return a non-zero start time for the current process.
	startTime := GetProcessStartTime(os.Getpid())
	assert.Greater(t, startTime, int64(0), "start time should be > 0 for current process")
}

func TestGetProcessStartTime_NonexistentPID(t *testing.T) {
	startTime := GetProcessStartTime(99999999)
	assert.Equal(t, int64(0), startTime, "start time should be 0 for nonexistent PID")
}

func TestGetProcessStartTime_InvalidPID(t *testing.T) {
	startTime := GetProcessStartTime(0)
	assert.Equal(t, int64(0), startTime)
	startTime = GetProcessStartTime(-1)
	assert.Equal(t, int64(0), startTime)
}

func TestIsRunning_PIDReuseDetection(t *testing.T) {
	// Simulate PID reuse: current process PID is alive, but with wrong start time.
	realStartTime := GetProcessStartTime(os.Getpid())

	// With correct start time → should be running.
	meta := &RunMeta{
		ID:           "reuse01",
		PID:          os.Getpid(),
		CWD:          "/tmp",
		Status:       StatusRunning,
		StartedAt:    1,
		PidStartTime: realStartTime,
	}
	assert.True(t, IsRunning(meta), "should be running with correct start time")

	// With wrong start time → should NOT be running (PID recycled).
	meta.PidStartTime = realStartTime - 100000
	assert.False(t, IsRunning(meta), "should NOT be running with wrong start time (PID reused)")

	// With zero start time → should be running (backward compat, no check).
	meta.PidStartTime = 0
	assert.True(t, IsRunning(meta), "should be running with zero start time (backward compat)")
}

func TestIsRunning_PIDReuseWithDeadProcess(t *testing.T) {
	// Dead process with wrong start time → should not be running.
	meta := &RunMeta{
		ID:           "reuse02",
		PID:          99999999, // nonexistent
		CWD:          "/tmp",
		Status:       StatusRunning,
		StartedAt:    1,
		PidStartTime: 12345,
	}
	assert.False(t, IsRunning(meta), "dead process should not be running regardless of start time")
}

func TestCreateRun_RecordsPidStartTime(t *testing.T) {
	tmpDir := t.TempDir()
	meta, err := CreateRun(tmpDir, "/test", os.Getpid())
	require.NoError(t, err)
	assert.Equal(t, GetProcessStartTime(os.Getpid()), meta.PidStartTime,
		"CreateRun should record process start time")
}
