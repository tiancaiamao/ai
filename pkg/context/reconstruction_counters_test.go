package context

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReconstructSnapshotWithCheckpoint_RecalculatesCounters verifies that
// ReconstructSnapshotWithCheckpoint correctly recalculates runtime counters
// (ToolCallsSinceLastTrigger, TurnsSinceLastTrigger, TotalTurns, LastLLMContextUpdate)
// from replayed journal entries after checkpoint.
//
// This addresses the issue where checkpoint saved counters right after compact
// (when they were reset to 0), making them meaningless for resume operations.
// The fix recalculates counters from messages replayed after checkpoint.
func TestReconstructSnapshotWithCheckpoint_RecalculatesCounters(t *testing.T) {
	sessionDir := t.TempDir()

	// Create a session with initial messages
	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	// Add some messages before checkpoint
	msg1 := NewUserMessage("Initial message")
	msg2 := NewAssistantMessage()
	msg3 := NewToolResultMessage("call_1", "read", []ContentBlock{
		TextContent{Type: "text", Text: "file content"},
	}, false)

	require.NoError(t, journal.AppendMessage(msg1))
	require.NoError(t, journal.AppendMessage(msg2))
	require.NoError(t, journal.AppendMessage(msg3))

	// Create a checkpoint with reset counters (simulating post-compact state)
	snapshot := NewContextSnapshot("test-session", "/tmp")
	snapshot.LLMContext = "# Initial Context\nTask: test"
	snapshot.RecentMessages = []AgentMessage{msg1, msg2, msg3}
	snapshot.AgentState = NewAgentState("test-session", "/tmp")
	snapshot.AgentState.TotalTurns = 1
	snapshot.AgentState.ToolCallsSinceLastTrigger = 0  // Reset after compact
	snapshot.AgentState.TurnsSinceLastTrigger = 0     // Reset after compact
	snapshot.AgentState.LastTriggerTurn = 1
	snapshot.AgentState.LastLLMContextUpdate = 1

	cpInfo, err := SaveCheckpoint(sessionDir, snapshot, 1, 3)
	require.NoError(t, err)

	// Add more messages after checkpoint (simulating resume scenario)
	msg4 := NewUserMessage("Continue task")
	msg5 := NewAssistantMessage()
	msg6 := NewToolResultMessage("call_2", "grep", []ContentBlock{
		TextContent{Type: "text", Text: "search results"},
	}, false)
	msg7 := NewUserMessage("Another request")
	msg8 := NewAssistantMessage()
	msg9 := NewToolResultMessage("call_3", "edit", []ContentBlock{
		TextContent{Type: "text", Text: "file edited"},
	}, false)
	msg10 := NewToolResultMessage("update_llm_context_1", "update_llm_context", []ContentBlock{
		TextContent{Type: "text", Text: "LLM Context updated."},
	}, false)

	require.NoError(t, journal.AppendMessage(msg4))
	require.NoError(t, journal.AppendMessage(msg5))
	require.NoError(t, journal.AppendMessage(msg6))
	require.NoError(t, journal.AppendMessage(msg7))
	require.NoError(t, journal.AppendMessage(msg8))
	require.NoError(t, journal.AppendMessage(msg9))
	require.NoError(t, journal.AppendMessage(msg10))

	// Read all journal entries
	entries, err := journal.ReadAll()
	require.NoError(t, err)

	// Reconstruct snapshot with checkpoint
	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, cpInfo, entries)
	require.NoError(t, err)

	// Verify counters were recalculated from replayed messages
	// Expected: 3 tool calls after checkpoint (call_2, call_3, update_llm_context_1)
	//          2 assistant messages after checkpoint (msg5, msg8)
	assert.Equal(t, 3, reconstructed.AgentState.ToolCallsSinceLastTrigger,
		"ToolCallsSinceLastTrigger should count tool calls after checkpoint")
	assert.Equal(t, 2, reconstructed.AgentState.TurnsSinceLastTrigger,
		"TurnsSinceLastTrigger should count assistant messages after checkpoint")
	assert.Equal(t, 3, reconstructed.AgentState.TotalTurns,
		"TotalTurns should be updated from replayed messages")
	assert.Equal(t, 3, reconstructed.AgentState.LastLLMContextUpdate,
		"LastLLMContextUpdate should track update_llm_context calls after checkpoint")

	// Verify checkpoint values are NOT used (they were 0)
	assert.NotEqual(t, 0, reconstructed.AgentState.ToolCallsSinceLastTrigger,
		"ToolCallsSinceLastTrigger should not be 0 (checkpoint value)")
	assert.NotEqual(t, 0, reconstructed.AgentState.TurnsSinceLastTrigger,
		"TurnsSinceLastTrigger should not be 0 (checkpoint value)")

	// Verify messages were replayed
	assert.Equal(t, 10, len(reconstructed.RecentMessages),
		"All messages should be replayed (3 from checkpoint + 7 from journal)")
}

// TestReconstructSnapshotWithCheckpoint_ResetsCountersOnCompact verifies that
// counters are reset when a compact event is encountered during reconstruction.
func TestReconstructSnapshotWithCheckpoint_ResetsCountersOnCompact(t *testing.T) {
	sessionDir := t.TempDir()

	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	// Create checkpoint
	snapshot := NewContextSnapshot("test", "/tmp")
	snapshot.LLMContext = "# Context"
	snapshot.RecentMessages = []AgentMessage{}
	snapshot.AgentState = NewAgentState("test", "/tmp")
	snapshot.AgentState.TotalTurns = 5

	cpInfo, err := SaveCheckpoint(sessionDir, snapshot, 5, 0)
	require.NoError(t, err)

	// Add messages after checkpoint
	msg1 := NewToolResultMessage("call_1", "grep", []ContentBlock{TextContent{Type: "text", Text: "result1"}}, false)
	require.NoError(t, journal.AppendMessage(msg1))

	// Add compact event
	require.NoError(t, journal.AppendCompact(CompactEvent{
		Turn:             6,
		KeptMessageCount: 0,
		Summary:          "# Compacted Context",
	}))

	// Add messages after compact
	msg2 := NewToolResultMessage("call_2", "grep", []ContentBlock{TextContent{Type: "text", Text: "result2"}}, false)
	require.NoError(t, journal.AppendMessage(msg2))

	// Reconstruct
	entries, _ := journal.ReadAll()
	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, cpInfo, entries)
	require.NoError(t, err)

	// Counters should be reset after compact event
	assert.Equal(t, 1, reconstructed.AgentState.ToolCallsSinceLastTrigger,
		"Should only count tool calls after last compact")
}

// TestReconstructSnapshotWithCheckpoint_NoMessagesAfterCheckpoint verifies
// that counters remain zero when no messages are replayed after checkpoint.
func TestReconstructSnapshotWithCheckpoint_NoMessagesAfterCheckpoint(t *testing.T) {
	sessionDir := t.TempDir()

	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	// Create checkpoint with non-zero counters
	snapshot := NewContextSnapshot("test", "/tmp")
	snapshot.LLMContext = "# Context"
	snapshot.RecentMessages = []AgentMessage{}
	snapshot.AgentState = NewAgentState("test", "/tmp")
	snapshot.AgentState.TotalTurns = 10
	snapshot.AgentState.ToolCallsSinceLastTrigger = 5
	snapshot.AgentState.TurnsSinceLastTrigger = 3

	cpInfo, err := SaveCheckpoint(sessionDir, snapshot, 10, 0)
	require.NoError(t, err)

	// No messages after checkpoint

	// Reconstruct
	entries, _ := journal.ReadAll()
	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, cpInfo, entries)
	require.NoError(t, err)

	// Counters should match checkpoint (no messages to recalculate)
	assert.Equal(t, 5, reconstructed.AgentState.ToolCallsSinceLastTrigger)
	assert.Equal(t, 3, reconstructed.AgentState.TurnsSinceLastTrigger)
	assert.Equal(t, 10, reconstructed.AgentState.TotalTurns)
}

// TestReconstructSnapshotWithCheckpoint_TracksLastLLMContextUpdate verifies
// that LastLLMContextUpdate is tracked correctly when update_llm_context tool is called.
func TestReconstructSnapshotWithCheckpoint_TracksLastLLMContextUpdate(t *testing.T) {
	sessionDir := t.TempDir()

	journal, err := OpenJournal(sessionDir)
	require.NoError(t, err)
	defer journal.Close()

	// Create checkpoint with old LLM context update
	snapshot := NewContextSnapshot("test", "/tmp")
	snapshot.LLMContext = "# Old Context"
	snapshot.AgentState = NewAgentState("test", "/tmp")
	snapshot.AgentState.TotalTurns = 5
	snapshot.AgentState.LastLLMContextUpdate = 5

	cpInfo, err := SaveCheckpoint(sessionDir, snapshot, 5, 0)
	require.NoError(t, err)

	// Add messages including update_llm_context
	msg1 := NewToolResultMessage("call_1", "grep", []ContentBlock{TextContent{Type: "text", Text: "result"}}, false)
	require.NoError(t, journal.AppendMessage(msg1))

	// This should update LastLLMContextUpdate
	msg2 := NewToolResultMessage("update_ctx_1", "update_llm_context", []ContentBlock{
		TextContent{Type: "text", Text: "Updated context"},
	}, false)
	require.NoError(t, journal.AppendMessage(msg2))

	msg3 := NewToolResultMessage("call_2", "read", []ContentBlock{TextContent{Type: "text", Text: "file"}}, false)
	require.NoError(t, journal.AppendMessage(msg3))

	// Reconstruct
	entries, _ := journal.ReadAll()
	reconstructed, err := ReconstructSnapshotWithCheckpoint(sessionDir, cpInfo, entries)
	require.NoError(t, err)

	// LastLLMContextUpdate should reflect the update_llm_context call
	// Note: Since there are no assistant messages, lastTurn stays at 5
	// The update_llm_context call happens in turn 5 (after checkpoint)
	assert.Equal(t, 5, reconstructed.AgentState.LastLLMContextUpdate,
		"LastLLMContextUpdate should be updated to current turn of update_llm_context")
}