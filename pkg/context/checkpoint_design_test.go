// Package context provides tests for checkpoint design compliance.
// These tests verify that checkpoints follow the event sourcing design:
// - Checkpoints should NOT contain messages.jsonl
// - RecentMessages should be rebuilt by replaying journal from message_index
// - Journal is the single source of truth for message history
package context

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckpoint_ShouldNotSaveMessagesJsonl verifies that checkpoints do NOT
// save messages.jsonl. Per the design document, checkpoints should only contain:
// - llm_context.txt (LLM-maintained structured context)
// - agent_state.json (System metadata with message_index)
//
// The RecentMessages should be rebuilt by replaying the journal from message_index,
// not by loading a messages.jsonl file from the checkpoint directory.
func TestCheckpoint_ShouldNotSaveMessagesJsonl(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create a snapshot with some RecentMessages
	snapshot := &ContextSnapshot{
		LLMContext: "Test LLM context",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 1"),
			NewAssistantMessage(),
			NewUserMessage("Message 2"),
		},
		AgentState: AgentState{
			TotalTurns: 3,
		},
	}

	// Save checkpoint
	info, err := SaveCheckpoint(sessionDir, snapshot, 3, 10)
	require.NoError(t, err)

	// Verify checkpoint directory structure
	checkpointPath := filepath.Join(sessionDir, info.Path)

	// ✓ Should have llm_context.txt
	llmContextPath := filepath.Join(checkpointPath, "llm_context.txt")
	assert.FileExists(t, llmContextPath, "Checkpoint must have llm_context.txt")

	// ✓ Should have agent_state.json
	agentStatePath := filepath.Join(checkpointPath, "agent_state.json")
	assert.FileExists(t, agentStatePath, "Checkpoint must have agent_state.json")

	// ✗ Should NOT have messages.jsonl (this is the key assertion!)
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	assert.NoFileExists(t, messagesPath, "Checkpoint should NOT have messages.jsonl")

	// Verify only expected files exist
	entries, err := os.ReadDir(checkpointPath)
	require.NoError(t, err)

	expectedFiles := map[string]bool{
		"llm_context.txt":  false,
		"agent_state.json": false,
	}

	for _, entry := range entries {
		name := entry.Name()
		if _, expected := expectedFiles[name]; expected {
			expectedFiles[name] = true
		} else {
			t.Errorf("Unexpected file in checkpoint: %s", name)
		}
	}

	for name, found := range expectedFiles {
		assert.True(t, found, "Checkpoint should have %s", name)
	}
}

// TestCheckpoint_EmptyRecentMessagesNotSaved verifies that when RecentMessages
// is empty, no messages.jsonl should be created in the checkpoint.
func TestCheckpoint_EmptyRecentMessagesNotSaved(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create snapshot with empty RecentMessages
	snapshot := &ContextSnapshot{
		LLMContext:     "Test context",
		RecentMessages: []AgentMessage{}, // Empty
		AgentState: AgentState{
			TotalTurns: 0,
		},
	}

	// Save checkpoint - should not fail
	info, err := SaveCheckpoint(sessionDir, snapshot, 0, 0)
	require.NoError(t, err)

	// Verify checkpoint directory
	checkpointPath := filepath.Join(sessionDir, info.Path)
	assert.DirExists(t, checkpointPath, "Checkpoint directory should be created")

	// messages.jsonl should NOT exist
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	assert.NoFileExists(t, messagesPath, "messages.jsonl should not exist when RecentMessages is empty")
}

// TestCheckpoint_LoadWithoutMessagesJsonl verifies that LoadCheckpoint works
// correctly when checkpoint does NOT have messages.jsonl.
// Per design, RecentMessages should be empty after loading - it will be rebuilt
// by replaying journal from message_index.
func TestCheckpoint_LoadWithoutMessagesJsonl(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create and save checkpoint with RecentMessages
	originalSnapshot := &ContextSnapshot{
		LLMContext: "Test LLM context",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 1"),
			NewAssistantMessage(),
		},
		AgentState: AgentState{
			TotalTurns:    2,
			TokensUsed:    1000,
			TokensLimit:   200000,
		},
	}

	info, err := SaveCheckpoint(sessionDir, originalSnapshot, 2, 5)
	require.NoError(t, err)

	// Load checkpoint back
	loadedSnapshot, err := LoadCheckpoint(sessionDir, info)
	require.NoError(t, err)

	// LLMContext and AgentState should be preserved
	assert.Equal(t, originalSnapshot.LLMContext, loadedSnapshot.LLMContext,
		"LLMContext should be preserved")
	assert.Equal(t, originalSnapshot.AgentState.TotalTurns, loadedSnapshot.AgentState.TotalTurns,
		"TotalTurns should be preserved")

	// RecentMessages should be empty because checkpoint doesn't have messages.jsonl
	// (In the correct design, RecentMessages is rebuilt by replaying journal, not loaded from checkpoint)
	assert.Empty(t, loadedSnapshot.RecentMessages,
		"RecentMessages should be empty when checkpoint has no messages.jsonl")
}

// TestCheckpoint_MessageIndexConsistency verifies that message_index in
// checkpoint info matches the journal length at checkpoint creation time.
func TestCheckpoint_MessageIndexConsistency(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create journal with some entries
	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	// Append 10 messages to journal
	for i := 0; i < 10; i++ {
		msg := NewUserMessage("Message")
		require.NoError(t, journal.AppendMessage(msg))
	}

	// Create checkpoint
	snapshot := &ContextSnapshot{
		LLMContext:     "Test context",
		RecentMessages: []AgentMessage{NewUserMessage("Recent")},
		AgentState:     *NewAgentState("session-test", "/workspace"),
	}
	snapshot.AgentState.TotalTurns = 5

	// message_index should equal journal length
	expectedLength := journal.GetLength()
	info, err := SaveCheckpoint(sessionDir, snapshot, 5, expectedLength)
	require.NoError(t, err)

	assert.Equal(t, expectedLength, info.MessageIndex,
		"message_index should match journal length")
	assert.Equal(t, 10, info.MessageIndex, "Should have 10 journal entries")
}

// TestResume_ReplayFromJournalIndex verifies the complete resume flow:
// 1. Load checkpoint (llm_context.txt + agent_state.json, NO messages.jsonl)
// 2. Replay journal entries from checkpoint.message_index
// 3. RecentMessages is rebuilt from journal replay
func TestResume_ReplayFromJournalIndex(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Step 1: Create initial checkpoint (message_index = 0)
	agentState := NewAgentState("session-1", "/workspace")
	initialSnapshot := &ContextSnapshot{
		LLMContext:     "Initial context",
		RecentMessages: []AgentMessage{},
		AgentState:     *agentState,
	}
	initialSnapshot.AgentState.TotalTurns = 0

	info, err := SaveCheckpoint(sessionDir, initialSnapshot, 0, 0)
	require.NoError(t, err)

	// Verify checkpoint doesn't have messages.jsonl
	checkpointPath := filepath.Join(sessionDir, info.Path)
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	assert.NoFileExists(t, messagesPath, "Checkpoint should not have messages.jsonl")

	// Step 2: Append messages to journal
	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	// Append 5 messages
	messages := []AgentMessage{
		NewUserMessage("User message 1"),
		NewAssistantMessage(),
		NewToolResultMessage("call-1", "bash", []ContentBlock{
			TextContent{Type: "text", Text: "Tool output 1"},
		}, false),
		NewUserMessage("User message 2"),
		NewAssistantMessage(),
	}

	for _, msg := range messages {
		require.NoError(t, journal.AppendMessage(msg))
	}

	// Step 3: Simulate resume flow
	// Load checkpoint (should return empty RecentMessages)
	loadedCheckpoint, err := LoadCheckpoint(sessionDir, info)
	require.NoError(t, err)
	assert.Empty(t, loadedCheckpoint.RecentMessages,
		"Loaded checkpoint should have empty RecentMessages")

	// Read all journal entries
	entries, err := journal.ReadAll()
	require.NoError(t, err)

	// Reconstruct by replaying journal from message_index
	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, info, entries)
	require.NoError(t, err)

	// Verify: RecentMessages should be rebuilt from journal replay
	assert.Equal(t, len(messages), len(reconstructed.RecentMessages),
		"RecentMessages should be rebuilt from journal replay")

	// Verify message order
	assert.Equal(t, "user", reconstructed.RecentMessages[0].Role)
	assert.Equal(t, "assistant", reconstructed.RecentMessages[1].Role)
	assert.Equal(t, "toolResult", reconstructed.RecentMessages[2].Role)
}

// TestResume_TruncateEventReplay verifies that truncate events in the journal
// are correctly applied during replay.
func TestResume_TruncateEventReplay(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create initial checkpoint
	agentState := NewAgentState("session-1", "/workspace")
	snapshot := &ContextSnapshot{
		LLMContext:     "Initial context",
		RecentMessages: []AgentMessage{},
		AgentState:     *agentState,
	}

	info, err := SaveCheckpoint(sessionDir, snapshot, 0, 0)
	require.NoError(t, err)

	// Open journal and append entries
	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	// Append a tool result message
	toolResult := NewToolResultMessage("call-123", "bash", []ContentBlock{
		TextContent{Type: "text", Text: "Long tool output that should be truncated"},
	}, false)
	require.NoError(t, journal.AppendMessage(toolResult))

	// Append a truncate event
	truncateEvent := TruncateEvent{
		ToolCallID: "call-123",
		Turn:       5,
	}
	require.NoError(t, journal.AppendTruncate(truncateEvent))

	// Append another message
	require.NoError(t, journal.AppendMessage(NewUserMessage("Next message")))

	// Resume: Load checkpoint and replay journal
	entries, _ := journal.ReadAll()
	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, info, entries)
	require.NoError(t, err)

	// Verify: Tool result should be marked as truncated
	foundTruncated := false
	for _, msg := range reconstructed.RecentMessages {
		if msg.ToolCallID == "call-123" {
			assert.True(t, msg.Truncated, "Tool result should be marked as truncated")
			assert.Equal(t, 5, msg.TruncatedAt, "TruncatedAt should be set")
			assert.Greater(t, msg.OriginalSize, 0, "OriginalSize should be set")
			foundTruncated = true
			break
		}
	}

	assert.True(t, foundTruncated, "Should find the truncated tool result")
}

// TestCompaction_DoesNotLoseJournalHistory verifies that after compaction:
// 1. Journal still has all original entries (append-only, SSOT)
// 2. Checkpoint doesn't save the compacted RecentMessages
// 3. Resume correctly rebuilds state by replaying journal
func TestCompaction_DoesNotLoseJournalHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping compaction test in short mode")
	}

	t.Skip("TODO: Implement with mocked compactor")

	// Test scenario:
	// 1. Create 100 messages in journal
	// 2. Perform compaction (reduces RecentMessages to 5)
	// 3. Create checkpoint
	// 4. Verify checkpoint has NO messages.jsonl
	// 5. Verify journal still has 100+ entries (not truncated)
	// 6. Append 10 new messages
	// 7. Resume and verify RecentMessages = 5 (compacted) + 10 (new)
	_ = t
}
