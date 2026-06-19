package context

import (
	"os"
	"path/filepath"
		"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Snapshot tests ---

func TestNewContextSnapshot(t *testing.T) {
	snapshot := NewContextSnapshot("session-1", "/workspace")

	assert.Empty(t, snapshot.RecentMessages)
	assert.Equal(t, "session-1", snapshot.AgentState.SessionID)
	assert.Equal(t, "/workspace", snapshot.AgentState.CurrentWorkingDir)
	assert.Equal(t, 200000, snapshot.AgentState.TokensLimit)
}

func TestContextSnapshotClone(t *testing.T) {
	original := NewContextSnapshot("test-session", "/test/dir")
	original.RecentMessages = append(original.RecentMessages, NewUserMessage("Hello"))
	original.AgentState.TotalTurns = 10

	clone := original.Clone()

	assert.Equal(t, len(original.RecentMessages), len(clone.RecentMessages))
	assert.Equal(t, original.AgentState.TotalTurns, clone.AgentState.TotalTurns)
}

func TestContextSnapshotClone_Nil(t *testing.T) {
	var snapshot *ContextSnapshot
	clone := snapshot.Clone()
	assert.Nil(t, clone)
}

// --- AgentState tests ---

func TestAgentStateClone(t *testing.T) {
	state := NewAgentState("session-1", "/workspace")
	state.TotalTurns = 42
	state.ActiveToolCalls = []string{"call-1", "call-2"}

	clone := state.Clone()

	assert.Equal(t, state.TotalTurns, clone.TotalTurns)
	assert.Equal(t, len(state.ActiveToolCalls), len(clone.ActiveToolCalls))

	// Verify deep copy of slice
	state.ActiveToolCalls[0] = "modified"
	assert.NotEqual(t, "modified", clone.ActiveToolCalls[0])
}

func TestAgentStateClone_Nil(t *testing.T) {
	var state *AgentState
	clone := state.Clone()
	assert.Nil(t, clone)
}

// --- Checkpoint save/load tests ---

func TestCheckpoint_SaveLoad_PreservesState(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	originalSnapshot := &ContextSnapshot{
		AgentState: &AgentState{
			WorkspaceRoot:     "/workspace",
			CurrentWorkingDir: "/workspace/project",
			TotalTurns:        10,
			TokensUsed:        5000,
			TokensLimit:       200000,
			LastCheckpoint:    0,
			LastTriggerTurn:   5,
		},
	}

	info, err := SaveCheckpoint(sessionDir, originalSnapshot, 10)
	require.NoError(t, err)

	assert.Equal(t, 10, info.Turn)

	// Verify agent_state.json exists in checkpoint dir
	checkpointPath := filepath.Join(sessionDir, info.Path)
	assert.FileExists(t, filepath.Join(checkpointPath, "agent_state.json"))

	// Verify agent state can be loaded
	loadedState, err := LoadCheckpointAgentState(checkpointPath)
	require.NoError(t, err)

	assert.Equal(t, originalSnapshot.AgentState.WorkspaceRoot, loadedState.WorkspaceRoot)
	assert.Equal(t, originalSnapshot.AgentState.TotalTurns, loadedState.TotalTurns)
	assert.Equal(t, originalSnapshot.AgentState.TokensUsed, loadedState.TokensUsed)
	assert.Equal(t, originalSnapshot.AgentState.LastTriggerTurn, loadedState.LastTriggerTurn)
}

// --- Symlink tests ---

func TestCurrentSymlink_PointsToLatest(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	snapshot1 := &ContextSnapshot{
		AgentState: &AgentState{TotalTurns: 1},
	}
	_, err := SaveCheckpoint(sessionDir, snapshot1, 1)
	require.NoError(t, err)

	snapshot2 := &ContextSnapshot{
		AgentState: &AgentState{TotalTurns: 2},
	}
	info2, err := SaveCheckpoint(sessionDir, snapshot2, 2)
	require.NoError(t, err)

	// Verify current symlink
	latestInfo, err := LoadLatestCheckpoint(sessionDir)
	require.NoError(t, err)
	assert.Equal(t, info2.Path, latestInfo.Path)
}

// --- SplitLines tests ---

func TestSplitLines(t *testing.T) {
	// Empty input
	if got := SplitLines(nil); len(got) != 0 {
		t.Errorf("expected 0 lines, got %d", len(got))
	}
	// Trailing newline only → skipped (empty lines are dropped)
	if got := SplitLines([]byte("\n")); len(got) != 0 {
		t.Errorf("expected 0 lines for lone newline, got %d", len(got))
	}
	// Multiple lines, no trailing newline
	got := SplitLines([]byte("a\nb\nc"))
	if len(got) != 3 || string(got[0]) != "a" || string(got[2]) != "c" {
		t.Errorf("unexpected: %+v", got)
	}
	// Blank lines in the middle are skipped
	got = SplitLines([]byte("a\n\nb"))
	if len(got) != 2 || string(got[0]) != "a" || string(got[1]) != "b" {
		t.Errorf("expected blank lines skipped, got %+v", got)
	}
}

