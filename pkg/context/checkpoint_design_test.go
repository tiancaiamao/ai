// Package context provides tests for checkpoint design compliance.
// These tests verify that checkpoints properly persist and restore RecentMessages:
// - Checkpoints should contain llm_context.txt, agent_state.json, and messages.jsonl
// - RecentMessages is loaded from checkpoint for fast resume
// - Journal replay only processes增量 entries after checkpoint's MessageIndex
// - Backward compatible: old checkpoints without messages.jsonl still work
package context

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckpoint_SavesMessagesJsonl verifies that checkpoints save messages.jsonl
// when RecentMessages is non-empty. Checkpoints should contain:
// - llm_context.txt (LLM-maintained structured context)
// - agent_state.json (System metadata with message_index)
// - messages.jsonl (RecentMessages for fast resume)
func TestCheckpoint_SavesMessagesJsonl(t *testing.T) {
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
	assert.FileExists(t, filepath.Join(checkpointPath, "llm_context.txt"), "Checkpoint must have llm_context.txt")

	// ✓ Should have agent_state.json
	assert.FileExists(t, filepath.Join(checkpointPath, "agent_state.json"), "Checkpoint must have agent_state.json")

	// ✓ Should have messages.jsonl (key assertion!)
	assert.FileExists(t, filepath.Join(checkpointPath, "messages.jsonl"), "Checkpoint should have messages.jsonl")

	// Verify only expected files exist
	entries, err := os.ReadDir(checkpointPath)
	require.NoError(t, err)

	expectedFiles := map[string]bool{
		"llm_context.txt":  false,
		"agent_state.json": false,
		"messages.jsonl":   false,
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

	// messages.jsonl should NOT exist when RecentMessages is empty
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	assert.NoFileExists(t, messagesPath, "messages.jsonl should not exist when RecentMessages is empty")
}

// TestCheckpoint_LoadRestoresRecentMessages verifies that LoadCheckpoint
// restores RecentMessages from the checkpoint's messages.jsonl.
func TestCheckpoint_LoadRestoresRecentMessages(t *testing.T) {
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
			TotalTurns:  2,
			TokensUsed:  1000,
			TokensLimit: 200000,
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

	// RecentMessages should now be preserved (not empty!)
	assert.Equal(t, len(originalSnapshot.RecentMessages), len(loadedSnapshot.RecentMessages),
		"RecentMessages should be preserved from checkpoint")
	if len(loadedSnapshot.RecentMessages) > 0 {
		assert.Equal(t, "user", loadedSnapshot.RecentMessages[0].Role)
		assert.Equal(t, "assistant", loadedSnapshot.RecentMessages[1].Role)
	}
}

// TestCheckpoint_LoadBackwardCompatible verifies that LoadCheckpoint works correctly
// when checkpoint does NOT have messages.jsonl (old format checkpoint).
// RecentMessages should be empty after loading, and full journal replay will be used.
func TestCheckpoint_LoadBackwardCompatible(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create a snapshot without RecentMessages (simulating old checkpoint)
	snapshot := &ContextSnapshot{
		LLMContext:     "Test LLM context",
		RecentMessages: []AgentMessage{},
		AgentState: AgentState{
			TotalTurns:  2,
			TokensUsed:  1000,
			TokensLimit: 200000,
		},
	}

	info, err := SaveCheckpoint(sessionDir, snapshot, 2, 5)
	require.NoError(t, err)

	// Load checkpoint - should return empty RecentMessages
	loadedSnapshot, err := LoadCheckpoint(sessionDir, info)
	require.NoError(t, err)
	assert.Empty(t, loadedSnapshot.RecentMessages,
		"Old checkpoint without messages should return empty RecentMessages")
	assert.Equal(t, "Test LLM context", loadedSnapshot.LLMContext)
}

// TestResume_FromCheckpointWithJournal verifies the full resume flow:
// save checkpoint, append journal entries, load and reconstruct.
func TestResume_FromCheckpointWithJournal(t *testing.T) {
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

	// Verify empty checkpoint doesn't have messages.jsonl
	checkpointPath := filepath.Join(sessionDir, info.Path)
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	assert.NoFileExists(t, messagesPath, "Empty checkpoint should not have messages.jsonl")

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
	// Load checkpoint (should return empty RecentMessages since no messages in checkpoint)
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

// TestResume_IncrementalReplay verifies that when checkpoint has RecentMessages,
// only incremental journal entries after checkpoint's MessageIndex are replayed.
// This is the key performance fix: avoids replaying the entire journal.
func TestResume_IncrementalReplay(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Step 1: Create initial checkpoint (empty, message_index = 0)
	agentState := NewAgentState("session-1", "/workspace")
	snapshot := &ContextSnapshot{
		LLMContext:     "Initial context",
		RecentMessages: []AgentMessage{},
		AgentState:     *agentState,
	}
	_, err := SaveCheckpoint(sessionDir, snapshot, 0, 0)
	require.NoError(t, err)

	// Step 2: Append 5 messages to journal
	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	initialMessages := []AgentMessage{
		NewUserMessage("User message 1"),
		NewAssistantMessage(),
		NewToolResultMessage("call-1", "bash", []ContentBlock{
			TextContent{Type: "text", Text: "Tool output 1"},
		}, false),
		NewUserMessage("User message 2"),
		NewAssistantMessage(),
	}
	for _, msg := range initialMessages {
		require.NoError(t, journal.AppendMessage(msg))
	}

	// Step 3: Create checkpoint at message_index=5 (after 5 messages)
	// This simulates a context management checkpoint that saves current RecentMessages
	snapshot2 := &ContextSnapshot{
		LLMContext:     "Context after initial work",
		RecentMessages: initialMessages,
		AgentState:     *agentState,
	}
	snapshot2.AgentState.TotalTurns = 3
	info2, err := SaveCheckpoint(sessionDir, snapshot2, 3, 5)
	require.NoError(t, err)

	// Verify checkpoint has messages.jsonl with the 5 messages
	checkpointPath := filepath.Join(sessionDir, info2.Path)
	assert.FileExists(t, filepath.Join(checkpointPath, "messages.jsonl"),
		"Checkpoint should have messages.jsonl")

	// Step 4: Append 3 more messages (incremental)
	incrementalMessages := []AgentMessage{
		NewUserMessage("User message 3"),
		NewAssistantMessage(),
		NewToolResultMessage("call-2", "bash", []ContentBlock{
			TextContent{Type: "text", Text: "Tool output 2"},
		}, false),
	}
	for _, msg := range incrementalMessages {
		require.NoError(t, journal.AppendMessage(msg))
	}

	// Step 5: Simulate resume - load checkpoint and reconstruct
	loadedSnapshot, err := LoadCheckpoint(sessionDir, info2)
	require.NoError(t, err)

	// RecentMessages should be loaded from checkpoint (5 messages)
	assert.Equal(t, 5, len(loadedSnapshot.RecentMessages),
		"RecentMessages should be loaded from checkpoint")

	// Read all journal entries
	entries, err := journal.ReadAll()
	require.NoError(t, err)
	assert.Equal(t, 8, len(entries), "Journal should have 8 entries total")

	// Reconstruct: should only replay entries after message_index=5
	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, info2, entries)
	require.NoError(t, err)

	// Verify: 5 (from checkpoint) + 3 (incremental replay) = 8
	assert.Equal(t, 8, len(reconstructed.RecentMessages),
		"RecentMessages should have 5 from checkpoint + 3 incremental = 8 total")

	// Verify LLMContext from checkpoint
	assert.Equal(t, "Context after initial work", reconstructed.LLMContext)

	// Verify message order
	assert.Equal(t, "user", reconstructed.RecentMessages[0].Role, "msg 1: user")
	assert.Equal(t, "assistant", reconstructed.RecentMessages[1].Role, "msg 2: assistant")
	assert.Equal(t, "toolResult", reconstructed.RecentMessages[2].Role, "msg 3: tool result")
	assert.Equal(t, "user", reconstructed.RecentMessages[3].Role, "msg 4: user")
	assert.Equal(t, "assistant", reconstructed.RecentMessages[4].Role, "msg 5: assistant")
	assert.Equal(t, "user", reconstructed.RecentMessages[5].Role, "msg 6: user (incremental)")
	assert.Equal(t, "assistant", reconstructed.RecentMessages[6].Role, "msg 7: assistant (incremental)")
	assert.Equal(t, "toolResult", reconstructed.RecentMessages[7].Role, "msg 8: tool result (incremental)")
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
// 2. Checkpoint saves the compacted RecentMessages
// 3. Resume correctly rebuilds state with incremental replay
func TestCompaction_DoesNotLoseJournalHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping compaction test in short mode")
	}

	t.Skip("TODO: Implement with mocked compactor")

	// Test scenario:
	// 1. Create 100 messages in journal
	// 2. Perform compaction (reduces RecentMessages to 5)
	// 3. Create checkpoint (saves compacted RecentMessages)
	// 4. Verify checkpoint has messages.jsonl with 5 messages
	// 5. Verify journal still has 100+ entries (not truncated)
	// 6. Append 10 new messages
	// 7. Resume and verify incremental replay: 5 (from checkpoint) + 10 (new)
	_ = t
}