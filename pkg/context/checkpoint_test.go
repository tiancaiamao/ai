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

	assert.Equal(t, "", snapshot.LLMContext)
	assert.Empty(t, snapshot.RecentMessages)
	assert.Equal(t, "session-1", snapshot.AgentState.SessionID)
	assert.Equal(t, "/workspace", snapshot.AgentState.CurrentWorkingDir)
	assert.Equal(t, 200000, snapshot.AgentState.TokensLimit)
}

func TestContextSnapshotClone(t *testing.T) {
	original := NewContextSnapshot("test-session", "/test/dir")
	original.LLMContext = "Test context content"
	original.RecentMessages = append(original.RecentMessages, NewUserMessage("Hello"))
	original.AgentState.TotalTurns = 10

	clone := original.Clone()

	assert.Equal(t, original.LLMContext, clone.LLMContext)
	assert.Equal(t, len(original.RecentMessages), len(clone.RecentMessages))
	assert.Equal(t, original.AgentState.TotalTurns, clone.AgentState.TotalTurns)

	// Verify deep copy
	original.LLMContext = "Modified"
	assert.NotEqual(t, "Modified", clone.LLMContext)
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
		LLMContext: "This is the LLM context with important information",
		RecentMessages: []AgentMessage{
			NewUserMessage("First message"),
			NewAssistantMessage(),
			NewUserMessage("Second message"),
		},
		AgentState: &AgentState{
			WorkspaceRoot:        "/workspace",
			CurrentWorkingDir:    "/workspace/project",
			TotalTurns:           10,
			TokensUsed:           5000,
			TokensLimit:          200000,
			LastLLMContextUpdate: 12345,
			LastCheckpoint:       0,
			LastTriggerTurn:      5,
		},
	}

	info, err := SaveCheckpoint(sessionDir, originalSnapshot, 10, 3)
	require.NoError(t, err)

	assert.Equal(t, 10, info.Turn)
	assert.Equal(t, 3, info.MessageIndex)
	assert.Equal(t, len(originalSnapshot.LLMContext), info.LLMContextChars)
	assert.Equal(t, len(originalSnapshot.RecentMessages), info.RecentMessagesCount)

	loadedSnapshot, err := LoadCheckpoint(sessionDir, info)
	require.NoError(t, err)

	assert.Equal(t, originalSnapshot.LLMContext, loadedSnapshot.LLMContext)
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
		LLMContext: "Test LLM context",
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
	assert.FileExists(t, filepath.Join(checkpointPath, "llm_context.txt"))
	assert.FileExists(t, filepath.Join(checkpointPath, "agent_state.json"))
	assert.FileExists(t, filepath.Join(checkpointPath, "messages.jsonl"))
}

func TestCheckpoint_EmptyRecentMessagesNotSaved(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	snapshot := &ContextSnapshot{
		LLMContext:     "Test context",
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

	for _, turn := range []int{5, 10, 15} {
		snapshot := &ContextSnapshot{
			LLMContext:     "Context",
			RecentMessages: []AgentMessage{NewUserMessage("msg")},
			AgentState:     NewAgentState("session", "/workspace"),
		}
		snapshot.AgentState.TotalTurns = turn
		_, err := SaveCheckpoint(sessionDir, snapshot, turn, 1)
		require.NoError(t, err)
	}

	currentPath, err := GetCurrentCheckpointPath(sessionDir)
	require.NoError(t, err)

	assert.True(t, strings.HasSuffix(currentPath, "checkpoint_00002"),
		"current/ should point to latest checkpoint, got %s", currentPath)
}

// --- CheckpointIndex tests ---

func TestCheckpointIndex_AddCheckpoint(t *testing.T) {
	idx := &CheckpointIndex{Checkpoints: []CheckpointInfo{}}

	idx.AddCheckpoint(CheckpointInfo{Turn: 10, Path: "checkpoints/checkpoint_00010"})
	idx.AddCheckpoint(CheckpointInfo{Turn: 20, Path: "checkpoints/checkpoint_00020"})

	require.Len(t, idx.Checkpoints, 2)
	assert.Equal(t, 10, idx.Checkpoints[0].Turn)
	assert.Equal(t, 20, idx.Checkpoints[1].Turn)
	assert.Equal(t, 20, idx.LatestCheckpointTurn)
}

func TestCheckpointIndex_GetCheckpointAtTurn(t *testing.T) {
	idx := &CheckpointIndex{
		Checkpoints: []CheckpointInfo{
			{Turn: 10, Path: "checkpoints/checkpoint_00010"},
			{Turn: 20, Path: "checkpoints/checkpoint_00020"},
		},
	}

	info, err := idx.GetCheckpointAtTurn(20)
	require.NoError(t, err)
	assert.Equal(t, 20, info.Turn)

	_, err = idx.GetCheckpointAtTurn(25)
	assert.Error(t, err)
}

func TestCheckpointIndex_AddAndSave(t *testing.T) {
	tmpDir := t.TempDir()

	idx := &CheckpointIndex{Checkpoints: []CheckpointInfo{}}
	err := idx.AddAndSave(CheckpointInfo{Turn: 5, Path: "checkpoints/checkpoint_00000"}, tmpDir)
	require.NoError(t, err)

	// Reload from disk
	loaded, err := LoadCheckpointIndex(tmpDir)
	require.NoError(t, err)
	require.Len(t, loaded.Checkpoints, 1)
	assert.Equal(t, 5, loaded.Checkpoints[0].Turn)
}

// --- Journal tests ---

func TestJournal_AppendAndRead(t *testing.T) {
	tmpDir := t.TempDir()

	journal, err := OpenJournal(tmpDir)
	require.NoError(t, err)
	defer journal.Close()

	msg1 := NewUserMessage("Hello")
	require.NoError(t, journal.AppendMessage(msg1))

	msg2 := NewAssistantMessage()
	require.NoError(t, journal.AppendMessage(msg2))

	entries, err := journal.ReadAll()
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, "message", entries[0].Type)
	assert.Equal(t, "user", entries[0].Message.Role)
	assert.Equal(t, "message", entries[1].Type)
	assert.Equal(t, "assistant", entries[1].Message.Role)
}

func TestJournal_ReadFromIndex(t *testing.T) {
	tmpDir := t.TempDir()

	journal, err := OpenJournal(tmpDir)
	require.NoError(t, err)
	defer journal.Close()

	for i := 0; i < 5; i++ {
		require.NoError(t, journal.AppendMessage(NewUserMessage("msg")))
	}

	entries, err := journal.ReadFromIndex(3)
	require.NoError(t, err)
	assert.Len(t, entries, 2)
}

func TestJournal_AppendTruncate(t *testing.T) {
	tmpDir := t.TempDir()

	journal, err := OpenJournal(tmpDir)
	require.NoError(t, err)
	defer journal.Close()

	require.NoError(t, journal.AppendTruncate(TruncateEvent{
		ToolCallID: "call-123",
		Turn:       5,
		Trigger:    "test",
	}))

	entries, err := journal.ReadAll()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "truncate", entries[0].Type)
	assert.Equal(t, "call-123", entries[0].Truncate.ToolCallID)
}

func TestJournal_AppendCompact(t *testing.T) {
	tmpDir := t.TempDir()

	journal, err := OpenJournal(tmpDir)
	require.NoError(t, err)
	defer journal.Close()

	require.NoError(t, journal.AppendCompact(CompactEvent{
		Summary:          "Compacted summary",
		KeptMessageCount: 5,
		Turn:             10,
	}))

	entries, err := journal.ReadAll()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "compact", entries[0].Type)
	assert.Equal(t, "Compacted summary", entries[0].Compact.Summary)
}

// --- Reconstruction tests ---

func TestApplyTruncateToSnapshot(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	toolResult := NewToolResultMessage("call-123", "test_tool", []ContentBlock{
		TextContent{Type: "text", Text: "Long tool output that should be truncated"},
	}, false)
	snapshot.RecentMessages = append(snapshot.RecentMessages, toolResult)

	err := ApplyTruncateToSnapshot(snapshot, TruncateEvent{
		ToolCallID: "call-123",
		Turn:       5,
		Trigger:    "test",
	})
	require.NoError(t, err)

	assert.True(t, snapshot.RecentMessages[0].Truncated)
	assert.Equal(t, 5, snapshot.RecentMessages[0].TruncatedAt)
	assert.Greater(t, snapshot.RecentMessages[0].OriginalSize, 0)
}

func TestApplyTruncateToSnapshot_NotFound(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	err := ApplyTruncateToSnapshot(snapshot, TruncateEvent{
		ToolCallID: "non-existent",
		Turn:       1,
	})
	assert.Error(t, err)
}

func TestReconstructSnapshotWithCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create initial checkpoint
	snapshot := &ContextSnapshot{
		LLMContext:     "Initial context",
		RecentMessages: []AgentMessage{},
		AgentState:     NewAgentState("session-1", "/workspace"),
	}

	info, err := SaveCheckpoint(sessionDir, snapshot, 0, 0)
	require.NoError(t, err)

	// Open journal and append messages
	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	require.NoError(t, journal.AppendMessage(NewUserMessage("User message")))
	require.NoError(t, journal.AppendMessage(NewAssistantMessage()))
	require.NoError(t, journal.AppendMessage(NewToolResultMessage("call-1", "bash", []ContentBlock{
		TextContent{Type: "text", Text: "Tool output"},
	}, false)))

	entries, err := journal.ReadAll()
	require.NoError(t, err)

	// Reconstruct
	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, info, entries)
	require.NoError(t, err)

	assert.Equal(t, "Initial context", reconstructed.LLMContext)
	assert.Len(t, reconstructed.RecentMessages, 3)
	assert.Equal(t, "user", reconstructed.RecentMessages[0].Role)
	assert.Equal(t, "assistant", reconstructed.RecentMessages[1].Role)
	assert.Equal(t, "toolResult", reconstructed.RecentMessages[2].Role)
}

func TestReconstructSnapshotWithCheckpoint_TruncateReplay(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	snapshot := &ContextSnapshot{
		LLMContext:     "Initial context",
		RecentMessages: []AgentMessage{},
		AgentState:     NewAgentState("session-1", "/workspace"),
	}

	info, err := SaveCheckpoint(sessionDir, snapshot, 0, 0)
	require.NoError(t, err)

	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	require.NoError(t, journal.AppendMessage(NewToolResultMessage("call-123", "bash", []ContentBlock{
		TextContent{Type: "text", Text: "Long tool output that should be truncated"},
	}, false)))
	require.NoError(t, journal.AppendTruncate(TruncateEvent{
		ToolCallID: "call-123",
		Turn:       5,
	}))
	require.NoError(t, journal.AppendMessage(NewUserMessage("Next message")))

	entries, err := journal.ReadAll()
	require.NoError(t, err)

	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, info, entries)
	require.NoError(t, err)

	// Tool result should be truncated
	found := false
	for _, msg := range reconstructed.RecentMessages {
		if msg.ToolCallID == "call-123" {
			assert.True(t, msg.Truncated)
			assert.Equal(t, 5, msg.TruncatedAt)
			found = true
		}
	}
	assert.True(t, found)
}

func TestReconstructSnapshotWithCheckpoint_IncrementalReplay(t *testing.T) {
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
		LLMContext:     "Context after initial work",
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
	assert.Equal(t, "Context after initial work", reconstructed.LLMContext)
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
