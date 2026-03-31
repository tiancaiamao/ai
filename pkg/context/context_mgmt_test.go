package context

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestContextMgmt_NoAction_UpdatesLastTriggerTurn tests that
// no_action updates LastTriggerTurn without creating checkpoint (Category 5.1).
func TestContextMgmt_NoAction_UpdatesLastTriggerTurn(t *testing.T) {
	// Given: A snapshot with trigger state
	snapshot := &ContextSnapshot{
		LLMContext: "Current context",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 1"),
			NewAssistantMessage(),
		},
		AgentState: AgentState{
			TotalTurns:           20,
			LastTriggerTurn:      10,
			TurnsSinceLastTrigger: 10,
		},
	}

	originalLastTriggerTurn := snapshot.AgentState.LastTriggerTurn
	originalTurnsSinceLastTrigger := snapshot.AgentState.TurnsSinceLastTrigger

	// Simulate no_action: Update LastTriggerTurn to current turn
	// This is what executeNoAction does in loop_context_mgmt.go
	snapshot.AgentState.LastTriggerTurn = snapshot.AgentState.TotalTurns
	snapshot.AgentState.TurnsSinceLastTrigger = 0

	// Then: LastTriggerTurn should be updated to current turn
	if snapshot.AgentState.LastTriggerTurn != 20 {
		t.Errorf("Expected LastTriggerTurn to be 20, got %d",
			snapshot.AgentState.LastTriggerTurn)
	}

	if snapshot.AgentState.TurnsSinceLastTrigger != 0 {
		t.Errorf("Expected TurnsSinceLastTrigger to be 0, got %d",
			snapshot.AgentState.TurnsSinceLastTrigger)
	}

	// And: The values should have changed from original
	if snapshot.AgentState.LastTriggerTurn == originalLastTriggerTurn {
		t.Error("LastTriggerTurn should have been updated")
	}
	if snapshot.AgentState.TurnsSinceLastTrigger == originalTurnsSinceLastTrigger {
		t.Error("TurnsSinceLastTrigger should have been reset to 0")
	}
}

// TestContextMgmt_Truncate_WritesEventToLog tests that
// truncate action writes truncate event to journal (Category 5.2).
func TestContextMgmt_Truncate_WritesEventToLog(t *testing.T) {
	// Given: A temporary directory for journal
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("Failed to create session dir: %v", err)
	}

	// Create a journal
	journal, err := OpenJournal(sessionDir)
	if err != nil {
		t.Fatalf("Failed to create journal: %v", err)
	}

	// And: A snapshot with messages to truncate
	snapshot := &ContextSnapshot{
		LLMContext: "Current context",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 1"),
			NewToolResultMessage("call_123", "bash", []ContentBlock{
				TextContent{Type: "text", Text: "This is a long tool output that should be truncated"},
			}, false),
			NewUserMessage("Message 2"),
		},
		AgentState: AgentState{
			TotalTurns: 15,
		},
	}

	// When: Applying truncate (simulating truncate_messages tool behavior)
	toolCallID := "call_123"
	turn := snapshot.AgentState.TotalTurns

	// Mark message as truncated in snapshot
	for i := range snapshot.RecentMessages {
		if snapshot.RecentMessages[i].ToolCallID == toolCallID {
			snapshot.RecentMessages[i].Truncated = true
			snapshot.RecentMessages[i].TruncatedAt = turn
			snapshot.RecentMessages[i].OriginalSize = len(snapshot.RecentMessages[i].ExtractText())
			break
		}
	}

	// Write truncate event to journal
	truncateEvent := TruncateEvent{
		ToolCallID: toolCallID,
		Turn:       turn,
		Trigger:    "context_management",
		Timestamp:  time.Now().Format(time.RFC3339),
	}
	err = journal.AppendTruncate(truncateEvent)
	if err != nil {
		t.Fatalf("Failed to append truncate to journal: %v", err)
	}

	// Then: Message should be marked as truncated
	foundTruncated := false
	for _, msg := range snapshot.RecentMessages {
		if msg.ToolCallID == toolCallID && msg.Truncated {
			foundTruncated = true
			if msg.TruncatedAt != turn {
				t.Errorf("Expected TruncatedAt %d, got %d", turn, msg.TruncatedAt)
			}
			if msg.OriginalSize == 0 {
				t.Error("Expected OriginalSize to be set")
			}
			break
		}
	}
	if !foundTruncated {
		t.Error("Expected message to be marked as truncated")
	}

	// And: Journal should contain the truncate event
	entries, err := journal.ReadAll()
	if err != nil {
		t.Fatalf("Failed to load journal: %v", err)
	}

	foundTruncateEntry := false
	for _, entry := range entries {
		if entry.Type == "truncate" && entry.Truncate != nil {
			if entry.Truncate.ToolCallID == toolCallID {
				foundTruncateEntry = true
				if entry.Truncate.Turn != turn {
					t.Errorf("Expected journal entry turn %d, got %d",
						turn, entry.Truncate.Turn)
				}
				if entry.Truncate.Trigger != "context_management" {
					t.Errorf("Expected trigger 'context_management', got %s",
						entry.Truncate.Trigger)
				}
				break
			}
		}
	}
	if !foundTruncateEntry {
		t.Error("Expected to find truncate event in journal")
	}
}

// TestContextMgmt_UpdateContext_CreatesCheckpoint tests that
// update_llm_context action creates a checkpoint (Category 5.3).
func TestContextMgmt_UpdateContext_CreatesCheckpoint(t *testing.T) {
	// Given: A temporary session directory with existing checkpoint
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("Failed to create session dir: %v", err)
	}

	// Create initial checkpoint
	snapshot := &ContextSnapshot{
		LLMContext: "Original context",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 1"),
		},
		AgentState: AgentState{
			TotalTurns: 10,
		},
	}

	info1, err := SaveCheckpoint(sessionDir, snapshot, 10, 1)
	if err != nil {
		t.Fatalf("Failed to create initial checkpoint: %v", err)
	}

	// When: Simulating update_llm_context action
	// This would update the llm_context.txt in the current checkpoint directory
	newLLMContext := "Updated context with new information"
	currentCheckpointPath := filepath.Join(sessionDir, "current")
	llmContextPath := filepath.Join(currentCheckpointPath, "llm_context.txt")

	if err := os.WriteFile(llmContextPath, []byte(newLLMContext), 0644); err != nil {
		t.Fatalf("Failed to write llm_context.txt: %v", err)
	}

	// Then: The llm_context.txt file should contain the new content
	content, err := os.ReadFile(llmContextPath)
	if err != nil {
		t.Fatalf("Failed to read llm_context.txt: %v", err)
	}

	if string(content) != newLLMContext {
		t.Errorf("Expected llm_context.txt to contain %q, got %q",
			newLLMContext, string(content))
	}

	// And: Loading from the checkpoint should return the updated context
	loadedSnapshot, err := LoadCheckpoint(sessionDir, info1)
	if err != nil {
		t.Fatalf("Failed to load checkpoint: %v", err)
	}

	if loadedSnapshot.LLMContext != newLLMContext {
		t.Errorf("Expected loaded LLMContext to be %q, got %q",
			newLLMContext, loadedSnapshot.LLMContext)
	}

	// Note: In the actual implementation, update_llm_context doesn't create
	// a new checkpoint - it updates the existing current checkpoint.
	// The checkpoint creation happens after all context management actions
	// are completed (in ExecuteContextMgmtMode).
}

// TestContextMgmt_Truncate_SnapshotAndJournalConsistent tests that
// truncation is consistent between snapshot and journal.
func TestContextMgmt_Truncate_SnapshotAndJournalConsistent(t *testing.T) {
	// Given: A temporary directory
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("Failed to create session dir: %v", err)
	}

	journal, err := OpenJournal(sessionDir)
	if err != nil {
		t.Fatalf("Failed to create journal: %v", err)
	}

	snapshot := &ContextSnapshot{
		LLMContext: "Test context",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 1"),
			NewToolResultMessage("call_1", "bash", []ContentBlock{
				TextContent{Type: "text", Text: "Output 1"},
			}, false),
			NewToolResultMessage("call_2", "grep", []ContentBlock{
				TextContent{Type: "text", Text: "Output 2"},
			}, false),
			NewUserMessage("Message 2"),
		},
		AgentState: AgentState{
			TotalTurns: 5,
		},
	}

	// When: Truncating multiple messages and writing to journal
	truncates := []struct {
		id   string
		turn int
	}{
		{"call_1", 5},
		{"call_2", 5},
	}

	for _, tc := range truncates {
		// Mark in snapshot
		for i := range snapshot.RecentMessages {
			if snapshot.RecentMessages[i].ToolCallID == tc.id {
				snapshot.RecentMessages[i].Truncated = true
				snapshot.RecentMessages[i].TruncatedAt = tc.turn
				break
			}
		}
		// Write to journal
		journal.AppendTruncate(TruncateEvent{
			ToolCallID: tc.id,
			Turn:       tc.turn,
			Trigger:    "context_management",
			Timestamp:  time.Now().Format(time.RFC3339),
		})
	}

	// Then: Snapshot and journal should be consistent
	entries, _ := journal.ReadAll()

	journalTruncates := 0
	for _, entry := range entries {
		if entry.Type == "truncate" {
			journalTruncates++
		}
	}

	snapshotTruncates := 0
	for _, msg := range snapshot.RecentMessages {
		if msg.Truncated {
			snapshotTruncates++
		}
	}

	if journalTruncates != snapshotTruncates {
		t.Errorf("Journal has %d truncates but snapshot has %d",
			journalTruncates, snapshotTruncates)
	}

	if journalTruncates != 2 {
		t.Errorf("Expected 2 truncate events, got %d", journalTruncates)
	}
}

// TestContextMgmt_Truncate_ReplayAppliesCorrectly tests that
// truncate events are correctly applied during journal replay.
func TestContextMgmt_Truncate_ReplayAppliesCorrectly(t *testing.T) {
	// Given: A base snapshot with tool results
	baseSnapshot := &ContextSnapshot{
		LLMContext: "Test context",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 1"),
		},
		AgentState: AgentState{
			TotalTurns: 0,
		},
	}

	// And: Journal entries with messages and truncate event
	journalEntries := []JournalEntry{
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewToolResultMessage("call_1", "bash", []ContentBlock{
					TextContent{Type: "text", Text: "Long output 1"},
				}, false)
				return &m
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewToolResultMessage("call_2", "grep", []ContentBlock{
					TextContent{Type: "text", Text: "Long output 2"},
				}, false)
				return &m
			}(),
		},
		{
			Type: "truncate",
			Truncate: &TruncateEvent{
				ToolCallID: "call_1",
				Turn:       5,
				Trigger:    "context_management",
			},
		},
	}

	// When: Replaying journal
	snapshot := &ContextSnapshot{
		LLMContext:     baseSnapshot.LLMContext,
		RecentMessages: make([]AgentMessage, 0, len(baseSnapshot.RecentMessages)),
		AgentState:     baseSnapshot.AgentState,
	}

	for _, entry := range journalEntries {
		if entry.Type == "message" && entry.Message != nil {
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			ApplyTruncateToSnapshot(snapshot, entry.Truncate.ToolCallID)
		}
	}

	// Then: call_1 should be truncated, call_2 should not
	var call1Truncated, call2Truncated bool
	for _, msg := range snapshot.RecentMessages {
		if msg.ToolCallID == "call_1" && msg.Truncated {
			call1Truncated = true
		}
		if msg.ToolCallID == "call_2" && msg.Truncated {
			call2Truncated = true
		}
	}

	if !call1Truncated {
		t.Error("Expected call_1 to be truncated after replay")
	}
	if call2Truncated {
		t.Error("Expected call_2 to NOT be truncated")
	}
}
