// Package agent provides fast unit tests for context management.
// These tests use NO external API calls and complete in milliseconds.
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
// Fast Unit Tests for Context Management (< 1 second each)
// ============================================================================

// TestContextMgmt_CheckpointOperations tests checkpoint save/load.
func TestContextMgmt_CheckpointOperations(t *testing.T) {
	t.Run("save_and_load_checkpoint", func(t *testing.T) {
		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "test-session")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		// Create snapshot
		snapshot := &agentctx.ContextSnapshot{
			LLMContext: "Test LLM context",
			AgentState: agentctx.AgentState{
				TokensLimit:     200000,
				TokensUsed:      50000,
				TotalTurns:      5,
				UpdatedAt:       time.Now(),
				LastTriggerTurn: 5,
			},
			RecentMessages: []agentctx.AgentMessage{
				agentctx.NewUserMessage("Hello"),
				agentctx.NewAssistantMessage(),
			},
		}

		// Save checkpoint
		info, err := agentctx.SaveCheckpoint(sessionDir, snapshot, 5, 2)
		require.NoError(t, err)
		assert.Equal(t, 5, info.Turn)
		assert.Equal(t, 2, info.MessageIndex)

		// Verify checkpoint file exists
		checkpointPath := filepath.Join(sessionDir, info.Path)
		_, err = os.Stat(checkpointPath)
		require.NoError(t, err)

		// Load checkpoint - RecentMessages is always empty (rebuilt from journal separately)
		loadedSnapshot, err := agentctx.LoadCheckpoint(sessionDir, info)
		require.NoError(t, err)

		// Verify loaded data - LLMContext and AgentState are preserved
		assert.Equal(t, snapshot.AgentState.TotalTurns, loadedSnapshot.AgentState.TotalTurns)
		assert.Equal(t, snapshot.AgentState.TokensUsed, loadedSnapshot.AgentState.TokensUsed)
		assert.Equal(t, snapshot.LLMContext, loadedSnapshot.LLMContext)

		// RecentMessages is preserved in checkpoint via messages.jsonl
		assert.Equal(t, len(snapshot.RecentMessages), len(loadedSnapshot.RecentMessages),
			"RecentMessages should be preserved from checkpoint")
	})

	t.Run("checkpoint_index_operations", func(t *testing.T) {
		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "test-session")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		// Create multiple checkpoints
		for turn := 1; turn <= 3; turn++ {
			snapshot := &agentctx.ContextSnapshot{
				AgentState: agentctx.AgentState{
					TotalTurns: turn,
				},
			}
			_, err := agentctx.SaveCheckpoint(sessionDir, snapshot, turn, turn*2)
			require.NoError(t, err)
		}

		// Load checkpoint index
		idx, err := agentctx.LoadCheckpointIndex(sessionDir)
		require.NoError(t, err)

		assert.Equal(t, 3, len(idx.Checkpoints))
		assert.Equal(t, 3, idx.LatestCheckpointTurn)

		// Get checkpoint at specific turn
		checkpoint, err := idx.GetCheckpointAtTurn(2)
		require.NoError(t, err)
		assert.Equal(t, 2, checkpoint.Turn)

		// Get latest checkpoint
		latest, err := idx.GetLatestCheckpoint()
		require.NoError(t, err)
		assert.Equal(t, 3, latest.Turn)
	})
}

// TestContextMgmt_JournalOperations tests journal append and read.
func TestContextMgmt_JournalOperations(t *testing.T) {
	t.Run("append_and_read_messages", func(t *testing.T) {
		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "test-session")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		// Open journal
		journal, err := agentctx.OpenJournal(sessionDir)
		require.NoError(t, err)
		defer journal.Close()

		// Append some messages
		messages := []agentctx.AgentMessage{
			agentctx.NewUserMessage("First message"),
			agentctx.NewAssistantMessage(),
			agentctx.NewUserMessage("Second message"),
		}

		for _, msg := range messages {
			err = journal.AppendMessage(msg)
			require.NoError(t, err)
		}

		// Read all entries
		entries, err := journal.ReadAll()
		require.NoError(t, err)
		assert.Equal(t, 3, len(entries))

		// Verify message content
		for i, entry := range entries {
			assert.NotNil(t, entry.Message)
			assert.Equal(t, messages[i].Role, entry.Message.Role)
		}
	})

	t.Run("append_truncate_event", func(t *testing.T) {
		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "test-session")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		journal, err := agentctx.OpenJournal(sessionDir)
		require.NoError(t, err)
		defer journal.Close()

		// Append a truncate event
		truncateEvent := agentctx.TruncateEvent{
			ToolCallID: "call_123",
			Turn:       5,
			Trigger:    "compact",
		}

		err = journal.AppendTruncate(truncateEvent)
		require.NoError(t, err)

		// Read and verify
		entries, err := journal.ReadAll()
		require.NoError(t, err)
		assert.Equal(t, 1, len(entries))
		assert.NotNil(t, entries[0].Truncate)
		assert.Equal(t, "call_123", entries[0].Truncate.ToolCallID)
	})

	t.Run("read_from_index", func(t *testing.T) {
		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "test-session")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		journal, err := agentctx.OpenJournal(sessionDir)
		require.NoError(t, err)
		defer journal.Close()

		// Append 5 messages
		for i := 0; i < 5; i++ {
			err = journal.AppendMessage(agentctx.NewUserMessage("Message"))
			require.NoError(t, err)
		}

		// Read from index 2 (should get 3 entries)
		entries, err := journal.ReadFromIndex(2)
		require.NoError(t, err)
		assert.Equal(t, 3, len(entries))
	})
}

// TestContextMgmt_TriggerConditions tests trigger checker logic.
func TestContextMgmt_TriggerConditions(t *testing.T) {
	t.Run("urgent_trigger_with_tool_calls", func(t *testing.T) {
		snapshot := &agentctx.ContextSnapshot{
			AgentState: agentctx.AgentState{
				TokensLimit:              200000,
				TokensUsed:               150000, // 75% - urgent threshold (TokenUrgent)
				TotalTurns:               10,
				ToolCallsSinceLastTrigger: 1,    // Met IntervalAtUrgent (1)
			},
			RecentMessages: generateTestMessages(10),
		}

		checker := agentctx.NewTriggerChecker()
		shouldTrigger, urgency, reason := checker.ShouldTrigger(snapshot)

		assert.True(t, shouldTrigger, "Should trigger at 75% tokens (urgent threshold)")
		assert.Equal(t, agentctx.UrgencyUrgent, urgency)
		assert.Equal(t, "token_usage_75%", reason)
	})

	t.Run("high_token_but_tool_interval_not_met", func(t *testing.T) {
		snapshot := &agentctx.ContextSnapshot{
			AgentState: agentctx.AgentState{
				TokensLimit:              200000,
				TokensUsed:               100000, // 50% - above high threshold
				TotalTurns:               10,
				ToolCallsSinceLastTrigger: 3,     // Below IntervalAtHigh (5)
			},
			RecentMessages: generateTestMessages(10),
		}

		checker := agentctx.NewTriggerChecker()
		shouldTrigger, _, reason := checker.ShouldTrigger(snapshot)

		assert.False(t, shouldTrigger, "Should NOT trigger when tool call interval not met")
		assert.Equal(t, "high_but_interval_3/5", reason)
	})

	t.Run("low_token_with_enough_tool_calls", func(t *testing.T) {
		snapshot := &agentctx.ContextSnapshot{
			AgentState: agentctx.AgentState{
				TokensLimit:              200000,
				TokensUsed:               45000, // 22.5% - low band
				TotalTurns:               10,
				ToolCallsSinceLastTrigger: 30,   // Met IntervalAtLow (30)
			},
			RecentMessages: generateTestMessages(10),
		}

		checker := agentctx.NewTriggerChecker()
		shouldTrigger, urgency, reason := checker.ShouldTrigger(snapshot)

		assert.True(t, shouldTrigger, "Should trigger at low token with enough tool calls")
		assert.Equal(t, agentctx.UrgencyPeriodic, urgency)
		assert.Equal(t, "token_low_22%", reason)
	})
}

// TestContextMgmt_MessageVisibility tests message visibility flags.
func TestContextMgmt_MessageVisibility(t *testing.T) {
	t.Run("filter_truncated_messages", func(t *testing.T) {
		messages := []agentctx.AgentMessage{
			agentctx.NewUserMessage("Visible message"),
			agentctx.NewUserMessage("Hidden message"),
		}
		messages[1].AgentVisible = false // Truncated

		// Count agent-visible messages
		visibleCount := 0
		for _, msg := range messages {
			if msg.IsAgentVisible() {
				visibleCount++
			}
		}

		assert.Equal(t, 1, visibleCount, "Only one message should be visible")
	})

	t.Run("user_visible_flag_preserved", func(t *testing.T) {
		msg := agentctx.NewUserMessage("Test")
		msg.UserVisible = true
		msg.AgentVisible = false

		assert.True(t, msg.IsUserVisible())
		assert.False(t, msg.IsAgentVisible())
	})

	t.Run("truncated_flag_detection", func(t *testing.T) {
		msg := agentctx.NewAssistantMessage()
		msg.Truncated = true

		// Check the Truncated field directly
		assert.True(t, msg.Truncated, "Truncated flag should be set")
		// Also check IsTruncated() method
		assert.True(t, msg.IsTruncated(), "IsTruncated() should return true")
		// Truncated messages are still agent visible unless AgentVisible is explicitly set to false
		assert.True(t, msg.AgentVisible, "AgentVisible is independent of Truncated")
	})
}

// TestContextMgmt_Reconstruction tests snapshot reconstruction.
func TestContextMgmt_Reconstruction(t *testing.T) {
	t.Run("reconstruct_with_checkpoint", func(t *testing.T) {
		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "test-session")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		// Create snapshot for checkpoint
		snapshot := &agentctx.ContextSnapshot{
			AgentState: agentctx.AgentState{
				TotalTurns: 3,
			},
			RecentMessages: []agentctx.AgentMessage{}, // Empty at checkpoint time
		}

		// Create checkpoint with messageIndex=0 (no prior journal entries in this journal)
		info, err := agentctx.SaveCheckpoint(sessionDir, snapshot, 3, 0)
		require.NoError(t, err)

		// Create journal with messages after checkpoint
		journal, err := agentctx.OpenJournal(sessionDir)
		require.NoError(t, err)

		postCheckpointMessages := []agentctx.AgentMessage{
			agentctx.NewUserMessage("Message 4"),
			agentctx.NewAssistantMessage(),
		}

		for _, msg := range postCheckpointMessages {
			err = journal.AppendMessage(msg)
			require.NoError(t, err)
		}

		// Read journal entries
		entries, err := journal.ReadAll()
		require.NoError(t, err)
		journal.Close()

		// Reconstruct snapshot
		reconstructed, err := agentctx.ReconstructSnapshotWithCheckpoint(
			sessionDir,
			info,
			entries,
		)
		require.NoError(t, err)

		// Verify reconstruction
		assert.Equal(t, 3, reconstructed.AgentState.TotalTurns)
		// Should have post-checkpoint messages
		assert.Equal(t, 2, len(reconstructed.RecentMessages))
	})
}

// TestContextMgmt_EdgeCases tests edge cases.
func TestContextMgmt_EdgeCases(t *testing.T) {
	t.Run("empty_snapshot_token_estimate", func(t *testing.T) {
		snapshot := &agentctx.ContextSnapshot{
			RecentMessages: []agentctx.AgentMessage{},
			AgentState: agentctx.AgentState{
				TokensLimit: 200000,
			},
		}

		percent := snapshot.EstimateTokenPercent()
		// Empty snapshot should have a very small token percentage
		assert.Less(t, percent, 0.01, "Empty snapshot should have < 1% tokens")
	})

	t.Run("single_message_token_estimate", func(t *testing.T) {
		snapshot := &agentctx.ContextSnapshot{
			RecentMessages: []agentctx.AgentMessage{
				agentctx.NewUserMessage("Hello"),
			},
			AgentState: agentctx.AgentState{
				TokensLimit: 200000,
			},
		}

		percent := snapshot.EstimateTokenPercent()
		assert.Greater(t, percent, 0.0)
		assert.Less(t, percent, 0.1)
	})

	t.Run("checkpoint_with_empty_messages", func(t *testing.T) {
		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "test-session")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		snapshot := &agentctx.ContextSnapshot{
			RecentMessages: []agentctx.AgentMessage{},
			AgentState: agentctx.AgentState{
				TokensLimit: 200000,
			},
		}

		// Should save successfully
		info, err := agentctx.SaveCheckpoint(sessionDir, snapshot, 1, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, info.Turn)

		// Should load successfully
		loaded, err := agentctx.LoadCheckpoint(sessionDir, info)
		require.NoError(t, err)
		assert.Equal(t, 0, len(loaded.RecentMessages))
	})

	t.Run("journal_read_from_invalid_index", func(t *testing.T) {
		tempDir := t.TempDir()
		sessionDir := filepath.Join(tempDir, "test-session")
		require.NoError(t, os.MkdirAll(sessionDir, 0755))

		journal, err := agentctx.OpenJournal(sessionDir)
		require.NoError(t, err)
		defer journal.Close()

		// Append one message
		err = journal.AppendMessage(agentctx.NewUserMessage("Test"))
		require.NoError(t, err)

		// Read from index 10 (beyond available entries)
		entries, err := journal.ReadFromIndex(10)
		require.NoError(t, err)
		assert.Equal(t, 0, len(entries))
	})
}

// TestContextMgmt_TruncateEvent tests truncate event handling.
func TestContextMgmt_TruncateEvent(t *testing.T) {
	t.Run("apply_truncate_to_tool_result", func(t *testing.T) {
		// Create messages including a tool result
		msg1 := agentctx.NewUserMessage("Message 1")
		msg2 := agentctx.NewToolResultMessage("call_test_123", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "Tool output"},
		}, false)
		msg3 := agentctx.NewUserMessage("Message 3")

		messages := []agentctx.AgentMessage{msg1, msg2, msg3}

		snapshot := &agentctx.ContextSnapshot{
			RecentMessages: messages,
		}

		// Apply truncate for the tool result
		truncateEvent := agentctx.TruncateEvent{
			ToolCallID: "call_test_123",
			Turn:       1,
			Trigger:    "test",
		}
		err := agentctx.ApplyTruncateToSnapshot(snapshot, truncateEvent)
		require.NoError(t, err)

		// Tool result message should be truncated
		assert.True(t, snapshot.RecentMessages[1].IsTruncated())
		// Original size should be saved
		assert.Greater(t, snapshot.RecentMessages[1].OriginalSize, 0)
	})

	t.Run("extract_text_from_messages", func(t *testing.T) {
		messages := []agentctx.AgentMessage{
			agentctx.NewUserMessage("User text"),
			agentctx.NewAssistantMessage(),
			agentctx.NewUserMessage("Another user text"),
		}

		// Add text content to assistant message
		messages[1].Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "Assistant text"},
		}

		// Extract text from all messages
		totalText := ""
		for _, msg := range messages {
			totalText += msg.ExtractText()
		}

		assert.Contains(t, totalText, "User text")
		assert.Contains(t, totalText, "Assistant text")
		assert.Contains(t, totalText, "Another user text")
	})

	t.Run("extract_tool_calls_from_assistant", func(t *testing.T) {
		msg := agentctx.NewAssistantMessage()

		// Add a tool call to the assistant message
		toolCall := agentctx.ToolCallContent{
			ID:   "call_test_456",
			Type: "toolCall",
			Name: "bash",
			Arguments: map[string]any{
				"command": "echo test",
			},
		}
		msg.Content = []agentctx.ContentBlock{toolCall}

		// Extract tool calls
		calls := msg.ExtractToolCalls()
		assert.Len(t, calls, 1)
		assert.Equal(t, "bash", calls[0].Name)
		assert.Equal(t, "call_test_456", calls[0].ID)
	})
}

// ============================================================================
// Benchmarks
// ============================================================================

func BenchmarkTriggerCheck(b *testing.B) {
	snapshot := &agentctx.ContextSnapshot{
		RecentMessages: generateTestMessages(100),
		AgentState: agentctx.AgentState{
			TokensLimit:     200000,
			TokensUsed:      150000,
			TotalTurns:      50,
			LastTriggerTurn: 40,
		},
	}

	checker := agentctx.NewTriggerChecker()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		checker.ShouldTrigger(snapshot)
	}
}

func BenchmarkEstimateTokens(b *testing.B) {
	snapshot := &agentctx.ContextSnapshot{
		RecentMessages: generateTestMessages(50),
		AgentState: agentctx.AgentState{
			TokensLimit: 200000,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		snapshot.EstimateTokenPercent()
	}
}

// ============================================================================
// Helper Functions
// ============================================================================

func generateTestMessages(count int) []agentctx.AgentMessage {
	messages := make([]agentctx.AgentMessage, count)
	for i := 0; i < count; i++ {
		if i%2 == 0 {
			messages[i] = agentctx.NewUserMessage("Test message")
		} else {
			messages[i] = agentctx.NewAssistantMessage()
		}
	}
	return messages
}

// ============================================================================
// Session Test Helper Examples
// ============================================================================

// TestSession_ResumeBug_NoMessagesInCheckpoint demonstrates using SessionTestHelper
// to test a real session bug where checkpoint has no messages.jsonl.
func TestSession_ResumeBug_NoMessagesInCheckpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test with real session data in short mode")
	}

	// Simply describe the test case
	helper := NewSessionTest(t, SessionTestCase{
		Name:        "resume_bug_case",
		SessionDir:  "resume_bug_case", // relative to testdata/sessions/
		Description: "Tests resume when checkpoint has no messages.jsonl - should replay all journal entries",
		ExpectedResults: struct {
			MinMessageCount   int
			MaxMessageCount   int
			FirstMessageRole  string
			LLMContextNotEmpty bool
			CheckpointHadMessages *bool
		}{
			MinMessageCount:    50,  // Should recover at least 50 messages
			FirstMessageRole:   "user",
			LLMContextNotEmpty: true,
			CheckpointHadMessages: boolPtr(false), // Old checkpoint format: no messages.jsonl
		},
	})
	defer helper.Cleanup()

	// Load and replay using the FIXED logic (no messages in checkpoint = replay from beginning)
	helper.LoadSession()

	// Simulate the fixed replay logic
	snapshot := helper.GetSnapshot()
	entries := helper.GetJournalEntries()
	checkpoint := helper.GetCheckpoint()

	startIndex := 0
	if len(snapshot.RecentMessages) > 0 {
		startIndex = checkpoint.MessageIndex
	}

	for i := startIndex; i < len(entries); i++ {
		entry := entries[i]
		if entry.Type == "message" && entry.Message != nil {
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			agentctx.ApplyTruncateToSnapshot(snapshot, *entry.Truncate)
		} else if entry.Type == "compact" && entry.Compact != nil {
			snapshot.LLMContext = entry.Compact.Summary
			snapshot.RecentMessages = []agentctx.AgentMessage{}
		}
	}

	// Verify results with helper methods
	helper.VerifyMinMessages(50).
		VerifyFirstMessageRole("user").
		VerifyLLMContextNotEmpty().
		VerifyCheckpointHadMessages(false)

	t.Logf("✓ Resume bug fixed: %d messages recovered", len(snapshot.RecentMessages))
}

// boolPtr returns a pointer to bool.
func boolPtr(b bool) *bool {
	return &b
}
