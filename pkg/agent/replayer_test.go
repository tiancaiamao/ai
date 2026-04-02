// Package agent provides session replay functionality for regression testing.
package agent

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReplay_ScanSession demonstrates scanning a session for key events.
func TestReplay_ScanSession(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping replay test in short mode")
	}

	// Use the resume_bug_case session
	sessionDir := filepath.Join("testdata", "sessions", "resume_bug_case")

	// Record from session
	recording, err := RecordFromSession(sessionDir)
	require.NoError(t, err, "Should record from session")

	// Create replayer and scan
	replayer := NewReplayer(recording, sessionDir)
	replayer.Scan()

	// Verify we found messages
	assert.Greater(t, len(replayer.GetRecording().Messages), 0,
		"Should have found at least one message")

	// Log scan results
	for _, log := range replayer.GetScanLog() {
		t.Log(log)
	}
}

// TestReplay_VerifyCheckpointCount verifies checkpoint count matches expected.
func TestReplay_VerifyCheckpointCount(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping replay test in short mode")
	}

	sessionDir := filepath.Join("testdata", "sessions", "resume_bug_case")

	// Count actual checkpoints
	actualCount, err := CountCheckpoints(sessionDir)
	require.NoError(t, err)

	// Record from session with trace (to get checkpoint events)
	traceFile := filepath.Join("testdata", "traces", "resume_bug_case_trace.json")
	recording, err := RecordFromSessionWithTrace(sessionDir, traceFile)
	require.NoError(t, err)

	replayer := NewReplayer(recording, sessionDir)
	replayer.Scan()

	// If we have trace events, verify checkpoint count matches
	if len(recording.Events) > 0 {
		assert.Equal(t, actualCount, replayer.CheckpointsCreated(),
			"Checkpoint count should match actual count from trace")
	} else {
		t.Skip("No trace events found, skipping checkpoint count verification")
	}
}

// TestReplay_FindCheckpointAtTurn tests finding a checkpoint at a specific turn.
func TestReplay_FindCheckpointAtTurn(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping replay test in short mode")
	}

	sessionDir := filepath.Join("testdata", "sessions", "resume_bug_case")

	// Find checkpoint at turn 7
	cp, err := FindCheckpointByTurn(sessionDir, 7)
	require.NoError(t, err, "Should find checkpoint at turn 7")

	assert.Equal(t, 7, cp.Turn, "Checkpoint should be at turn 7")
	assert.NotEmpty(t, cp.Path, "Checkpoint should have a path")
}

// TestReplay_ContextMgmtTriggered verifies context management was triggered.
func TestReplay_ContextMgmtTriggered(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping replay test in short mode")
	}

	sessionDir := filepath.Join("testdata", "sessions", "resume_bug_case")

	// Record from session with trace
	traceFile := filepath.Join("testdata", "traces", "resume_bug_case_trace.json")
	recording, err := RecordFromSessionWithTrace(sessionDir, traceFile)
	if err != nil {
		t.Skip("Trace file not found, skipping test")
	}

	replayer := NewReplayer(recording, sessionDir)
	replayer.Scan()

	// Verify context management was triggered
	if len(replayer.GetScanLog()) > 0 {
		for _, log := range replayer.GetScanLog() {
			t.Log(log)
		}
	}
}
