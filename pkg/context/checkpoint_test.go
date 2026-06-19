package context

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// --- SaveAgentState / LoadAgentState tests ---

func TestSaveLoadAgentState(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	original := &AgentState{
		WorkspaceRoot:     "/workspace",
		CurrentWorkingDir: "/workspace/project",
		TotalTurns:        10,
		TokensUsed:        5000,
		TokensLimit:       200000,
		LastTriggerTurn:   5,
	}

	require.NoError(t, SaveAgentState(sessionDir, original))
	assert.FileExists(t, filepath.Join(sessionDir, AgentStateFile))

	loaded, err := LoadAgentState(sessionDir)
	require.NoError(t, err)
	assert.Equal(t, original.WorkspaceRoot, loaded.WorkspaceRoot)
	assert.Equal(t, original.TotalTurns, loaded.TotalTurns)
	assert.Equal(t, original.TokensUsed, loaded.TokensUsed)
	assert.Equal(t, original.LastTriggerTurn, loaded.LastTriggerTurn)
}

func TestLoadAgentState_NoFile(t *testing.T) {
	tmpDir := t.TempDir()

	state, err := LoadAgentState(tmpDir)
	require.NoError(t, err)
	assert.Nil(t, state)
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
