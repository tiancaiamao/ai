package context

import (
	"os"
	"path/filepath"
	"strings"
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
		RecentMessages: []AgentMessage{
			NewUserMessage("First message"),
			NewAssistantMessage(),
			NewUserMessage("Second message"),
		},
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

	info, err := SaveCheckpoint(sessionDir, originalSnapshot, 10, 3)
	require.NoError(t, err)

	assert.Equal(t, 10, info.Turn)
	assert.Equal(t, 3, info.MessageIndex)
	assert.Equal(t, len(originalSnapshot.RecentMessages), info.RecentMessagesCount)

	loadedSnapshot, err := LoadCheckpoint(sessionDir, info)
	require.NoError(t, err)

	assert.Equal(t, len(originalSnapshot.RecentMessages), len(loadedSnapshot.RecentMessages))
	assert.Equal(t, originalSnapshot.AgentState.WorkspaceRoot, loadedSnapshot.AgentState.WorkspaceRoot)
	assert.Equal(t, originalSnapshot.AgentState.TotalTurns, loadedSnapshot.AgentState.TotalTurns)
	assert.Equal(t, originalSnapshot.AgentState.TokensUsed, loadedSnapshot.AgentState.TokensUsed)
	assert.Equal(t, originalSnapshot.AgentState.LastTriggerTurn, loadedSnapshot.AgentState.LastTriggerTurn)
}

func TestCheckpoint_SavesMessagesJsonl(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	snapshot := &ContextSnapshot{
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 1"),
			NewAssistantMessage(),
			NewUserMessage("Message 2"),
		},
		AgentState: &AgentState{TotalTurns: 3},
	}

	info, err := SaveCheckpoint(sessionDir, snapshot, 3, 10)
	require.NoError(t, err)

	checkpointPath := filepath.Join(sessionDir, info.Path)
	assert.FileExists(t, filepath.Join(checkpointPath, "agent_state.json"))
	assert.FileExists(t, filepath.Join(checkpointPath, "messages.jsonl"))
}

func TestCheckpoint_EmptyRecentMessagesNotSaved(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	snapshot := &ContextSnapshot{
		RecentMessages: []AgentMessage{},
		AgentState:     &AgentState{TotalTurns: 0},
	}

	info, err := SaveCheckpoint(sessionDir, snapshot, 0, 0)
	require.NoError(t, err)

	checkpointPath := filepath.Join(sessionDir, info.Path)
	assert.NoFileExists(t, filepath.Join(checkpointPath, "messages.jsonl"))
}

// --- Symlink tests ---

func TestCurrentSymlink_PointsToLatest(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	snapshot1 := &ContextSnapshot{
		RecentMessages: []AgentMessage{NewUserMessage("msg1")},
		AgentState:     &AgentState{TotalTurns: 1},
	}
	_, err := SaveCheckpoint(sessionDir, snapshot1, 1, 0)
	require.NoError(t, err)

	snapshot2 := &ContextSnapshot{
		RecentMessages: []AgentMessage{NewUserMessage("msg2")},
		AgentState:     &AgentState{TotalTurns: 2},
	}
	info2, err := SaveCheckpoint(sessionDir, snapshot2, 2, 1)
	require.NoError(t, err)

	// Verify current symlink
	latestInfo, err := LoadLatestCheckpoint(sessionDir)
	require.NoError(t, err)
	assert.Equal(t, info2.Path, latestInfo.Path)
}

// --- ReconstructSnapshotWithCheckpoint tests ---

func TestReconstructSnapshotWithCheckpoint_BasicReconstruction(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Step 1: Create journal with 5 messages
	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		if i%2 == 0 {
			require.NoError(t, journal.AppendMessage(NewUserMessage("msg")))
		} else {
			require.NoError(t, journal.AppendMessage(NewAssistantMessage()))
		}
	}

	// Step 2: Create checkpoint (snapshot includes messages)
	allEntries, err := journal.ReadAll()
	require.NoError(t, err)

	var msgs []AgentMessage
	for _, e := range allEntries {
		if e.Type == "message" && e.Message != nil {
			msgs = append(msgs, *e.Message)
		}
	}

	snapshot := &ContextSnapshot{
		RecentMessages: msgs,
		AgentState:     NewAgentState("session-1", "/workspace"),
	}

	info, err := SaveCheckpoint(sessionDir, snapshot, 5, 5)
	require.NoError(t, err)

	// Step 3: Append 3 more messages
	require.NoError(t, journal.AppendMessage(NewUserMessage("incremental 1")))
	require.NoError(t, journal.AppendMessage(NewAssistantMessage()))
	require.NoError(t, journal.AppendMessage(NewToolResultMessage("call-2", "bash", []ContentBlock{
		TextContent{Type: "text", Text: "output"},
	}, false)))

	// Step 4: Reconstruct
	entries, err := journal.ReadAll()
	require.NoError(t, err)

	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, info, entries)
	require.NoError(t, err)

	// 5 from checkpoint + 3 incremental = 8
	assert.Len(t, reconstructed.RecentMessages, 8)
}

// --- TruncateWithHeadTail tests ---

func TestTruncateWithHeadTail_Short(t *testing.T) {
	result := TruncateWithHeadTail("short text")
	assert.Contains(t, result, "truncated")
}

func TestTruncateWithHeadTail_Long(t *testing.T) {
	longText := strings.Repeat("a", 5000)
	result := TruncateWithHeadTail(longText)
	assert.Contains(t, result, "chars truncated")
	assert.True(t, len(result) < len(longText))
}

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
