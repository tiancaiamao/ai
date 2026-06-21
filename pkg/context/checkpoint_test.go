package context

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
