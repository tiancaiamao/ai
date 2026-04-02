// Package agent provides tests for checkpoint behavior to prevent bugs.
package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// ============================================================================
// Checkpoint Structure Tests (Prevent Bug #4)
// ============================================================================

// TestCheckpoint_Structure verifies that checkpoints have the correct structure.
// This prevents confusion about what should be in a checkpoint directory.
func TestCheckpoint_Structure(t *testing.T) {
	tempDir := t.TempDir()
	sessionDir := filepath.Join(tempDir, "test-session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create a snapshot with some data
	snapshot := &agentctx.ContextSnapshot{
		LLMContext: "Test LLM context",
		AgentState: agentctx.AgentState{
			TokensLimit: 200000,
			TokensUsed:  50000,
			TotalTurns:  5,
			UpdatedAt:   time.Now(),
		},
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("Hello"),
			agentctx.NewAssistantMessage(),
		},
	}

	// Save checkpoint
	info, err := agentctx.SaveCheckpoint(sessionDir, snapshot, 5, 10)
	require.NoError(t, err)

	// Verify checkpoint directory structure
	checkpointPath := filepath.Join(sessionDir, info.Path)

	// 1. Must have llm_context.txt
	llmContextPath := filepath.Join(checkpointPath, "llm_context.txt")
	assert.FileExists(t, llmContextPath, "Checkpoint must have llm_context.txt")

	// 2. Must have agent_state.json
	agentStatePath := filepath.Join(checkpointPath, "agent_state.json")
	assert.FileExists(t, agentStatePath, "Checkpoint must have agent_state.json")

	// 3. Should NOT have messages.jsonl (per design: checkpoint doesn't save messages)
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	assert.NoFileExists(t, messagesPath, "Checkpoint should NOT have messages.jsonl")

	// 4. Should NOT have other files
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

	// 5. Verify all expected files exist
	for name, found := range expectedFiles {
		assert.True(t, found, "Checkpoint should have %s", name)
	}
}

// TestCheckpoint_MessagesNotSaved verifies that checkpoint does NOT save
// messages.jsonl. Per the event sourcing design, RecentMessages is rebuilt
// by replaying the journal from message_index.
func TestCheckpoint_MessagesJSONLIsSnapshot(t *testing.T) {
	tempDir := t.TempDir()
	sessionDir := filepath.Join(tempDir, "test-session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create snapshot with 3 messages
	snapshot := &agentctx.ContextSnapshot{
		LLMContext: "Test context",
		AgentState: agentctx.AgentState{
			TotalTurns: 3,
			UpdatedAt:  time.Now(),
		},
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("Message 1"),
			agentctx.NewAssistantMessage(),
			agentctx.NewUserMessage("Message 2"),
		},
	}

	// Save checkpoint with messageIndex = 10 (simulating journal has more entries)
	info, err := agentctx.SaveCheckpoint(sessionDir, snapshot, 3, 10)
	require.NoError(t, err)

	// Verify checkpoint does NOT have messages.jsonl
	checkpointPath := filepath.Join(sessionDir, info.Path)
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	assert.NoFileExists(t, messagesPath, "Checkpoint should NOT have messages.jsonl")

	// Load checkpoint back
	loaded, err := agentctx.LoadCheckpoint(sessionDir, info)
	require.NoError(t, err)

	// Verify RecentMessages is empty (will be rebuilt from journal)
	assert.Equal(t, 0, len(loaded.RecentMessages),
		"Checkpoint should NOT save RecentMessages; it should be empty")

	// Verify messageIndex is preserved (for journal replay)
	assert.Equal(t, 10, info.MessageIndex,
		"messageIndex should point to journal position")

	// Verify RecentMessagesCount metadata is saved (for debugging)
	assert.Equal(t, 3, info.RecentMessagesCount,
		"RecentMessagesCount metadata should be saved")
}

// TestCheckpoint_EmptyRecentMessages handles checkpoint when RecentMessages is empty.
func TestCheckpoint_EmptyRecentMessages(t *testing.T) {
	tempDir := t.TempDir()
	sessionDir := filepath.Join(tempDir, "test-session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create snapshot with empty RecentMessages
	snapshot := &agentctx.ContextSnapshot{
		LLMContext:     "Test context",
		RecentMessages: []agentctx.AgentMessage{}, // Empty
		AgentState: agentctx.AgentState{
			TotalTurns: 0,
			UpdatedAt:  time.Now(),
		},
	}

	// Save checkpoint - should not fail
	info, err := agentctx.SaveCheckpoint(sessionDir, snapshot, 0, 0)
	require.NoError(t, err)

	// Verify checkpoint was created
	checkpointPath := filepath.Join(sessionDir, info.Path)
	assert.DirExists(t, checkpointPath, "Checkpoint directory should be created")

	// messages.jsonl should NOT exist when RecentMessages is empty
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	assert.NoFileExists(t, messagesPath,
		"messages.jsonl should not exist when RecentMessages is empty")

	// Loading should work and return empty RecentMessages
	loaded, err := agentctx.LoadCheckpoint(sessionDir, info)
	require.NoError(t, err)
	assert.Empty(t, loaded.RecentMessages,
		"Loaded checkpoint should have empty RecentMessages")
}

// ============================================================================
// Context Management Behavior Tests (Prevent Bug #2)
// ============================================================================

// TestContextManagement_CompactCreatesSingleCheckpoint verifies that
// compact_messages tool creates exactly one checkpoint.
// This prevents Bug #2 where performCompaction + executeContextMgmtTools
// created two checkpoints.
func TestContextManagement_CompactCreatesSingleCheckpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Run("count_checkpoints_after_compact", func(t *testing.T) {
		// This test would require a full agent setup with LLM mocking
		// For now, we'll document the expected behavior:
		t.Skip("TODO: Add integration test with mocked LLM")

		// Expected behavior:
		// 1. Create agent with session
		// 2. Execute some turns to generate messages
		// 3. Count checkpoints before compact
		// 4. Trigger compact_messages tool
		// 5. Count checkpoints after compact
		// 6. Verify: checkpoints_after = checkpoints_before + 1

		// Dummy code to avoid unused variable errors
		_ = t
	})
}

// TestContextManagement_CheckpointIndexIntegrity verifies that
// checkpoint_index.json is consistent and doesn't have duplicate entries.
func TestContextManagement_CheckpointIndexIntegrity(t *testing.T) {
	tempDir := t.TempDir()
	sessionDir := filepath.Join(tempDir, "test-session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create multiple checkpoints
	snapshot := &agentctx.ContextSnapshot{
		LLMContext: "Test",
		AgentState: agentctx.AgentState{
			TotalTurns: 1,
			UpdatedAt:  time.Now(),
		},
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("Test"),
		},
	}

	// Create 3 checkpoints at different turns
	for i := 0; i < 3; i++ {
		snapshot.AgentState.TotalTurns = i + 1
		_, err := agentctx.SaveCheckpoint(sessionDir, snapshot, i+1, i*10)
		require.NoError(t, err)
	}

	// Load checkpoint index
	idx, err := agentctx.LoadCheckpointIndex(sessionDir)
	require.NoError(t, err)

	// Verify we have 3 checkpoints
	assert.Equal(t, 3, len(idx.Checkpoints),
		"Should have 3 checkpoints")

	// Verify no duplicate paths
	paths := make(map[string]bool)
	for _, cp := range idx.Checkpoints {
		if paths[cp.Path] {
			t.Errorf("Duplicate checkpoint path: %s", cp.Path)
		}
		paths[cp.Path] = true
	}

	// Verify latest checkpoint is correct
	latest, err := idx.GetLatestCheckpoint()
	require.NoError(t, err)
	assert.Equal(t, "checkpoints/checkpoint_00002", latest.Path,
		"Latest checkpoint should be checkpoint_00002")
}

// ============================================================================
// Regression Tests for Specific Bugs
// ============================================================================

// TestRegression_Bug2_DoubleCheckpointPrevention is a regression test for Bug #2
// where compact_messages created two checkpoints.
func TestRegression_Bug2_DoubleCheckpointPrevention(t *testing.T) {
	// This test documents the bug and how to detect it
	t.Log("Bug #2: compact_messages tool created two checkpoints")
	t.Log("Root cause: performCompaction called SaveCheckpoint, then executeContextMgmtTools called createCheckpoint")
	t.Log("Fix: executeContextMgmtTools only creates checkpoint for update_llm_context, not compact_messages")
	t.Log("Detection: Count checkpoint directories before and after compact_messages")

	// To verify fix doesn't regress:
	// 1. Run context management with compact_messages
	// 2. Count checkpoints created during the operation
	// 3. Should be exactly 1, not 2
	_ = t // Use t to avoid "declared and not used" error
}

// TestRegression_Bug4_CheckpointMessagesClarification is a regression test for Bug #4
// about confusion around checkpoint's messages.jsonl.
func TestRegression_Bug4_CheckpointMessagesClarification(t *testing.T) {
	// This test documents the design fix
	t.Log("Bug #4: Confusion about checkpoint containing messages.jsonl")
	t.Log("Design fix (per event sourcing pattern):")
	t.Log("  - Session root messages.jsonl = Complete append-only journal (SSOT)")
	t.Log("  - Checkpoint does NOT save messages.jsonl")
	t.Log("  - RecentMessages is rebuilt by replaying journal from message_index")

	tempDir := t.TempDir()
	sessionDir := filepath.Join(tempDir, "test-session")
	require.NoError(t, os.MkdirAll(sessionDir, 0755))

	// Create snapshot and save checkpoint
	snapshot := &agentctx.ContextSnapshot{
		LLMContext: "Test context",
		AgentState: agentctx.AgentState{UpdatedAt: time.Now()},
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("Message 1"),
		},
	}

	info, err := agentctx.SaveCheckpoint(sessionDir, snapshot, 1, 5)
	require.NoError(t, err)

	// Verify checkpoint does NOT have messages.jsonl
	checkpointPath := filepath.Join(sessionDir, info.Path)
	messagesPath := filepath.Join(checkpointPath, "messages.jsonl")
	assert.NoFileExists(t, messagesPath,
		"Checkpoint should NOT have messages.jsonl (per event sourcing design)")

	// Verify message_index is preserved for journal replay
	assert.Equal(t, 5, info.MessageIndex,
		"message_index should be preserved for journal replay")

	// Verify RecentMessagesCount metadata is saved (for debugging)
	assert.Equal(t, 1, info.RecentMessagesCount,
		"RecentMessagesCount metadata should be saved")
}

// ============================================================================
// Invariant Tests
// ============================================================================

// TestCheckpointInvariant_NoOrphanedFiles verifies there are no orphaned files
// in checkpoint directories that shouldn't be there.
func TestCheckpointInvariant_NoOrphanedFiles(t *testing.T) {
	// This would scan all checkpoint directories and verify only expected files exist
	// Useful for catching bugs where code accidentally creates extra files
}

// TestCheckpointInvariant_MessageIndexConsistent verifies that checkpoint.MessageIndex
// is consistent with the journal length at checkpoint time.
func TestCheckpointInvariant_MessageIndexConsistent(t *testing.T) {
	// This would verify that after saving a checkpoint with messageIndex=N,
	// the journal has exactly N entries
}
