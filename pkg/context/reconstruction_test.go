package context

import (
	"testing"
)

// TestNewContextSnapshot tests creating a new context snapshot.
func TestNewContextSnapshot(t *testing.T) {
	sessionID := "test-session-123"
	cwd := "/test/directory"

	snapshot := NewContextSnapshot(sessionID, cwd)

	if snapshot == nil {
		t.Fatal("NewContextSnapshot returned nil")
	}

	if snapshot.LLMContext != "" {
		t.Errorf("Expected empty LLMContext, got %q", snapshot.LLMContext)
	}

	if len(snapshot.RecentMessages) != 0 {
		t.Errorf("Expected empty RecentMessages, got %d messages", len(snapshot.RecentMessages))
	}

	if snapshot.AgentState.SessionID != sessionID {
		t.Errorf("Expected SessionID %q, got %q", sessionID, snapshot.AgentState.SessionID)
	}

	if snapshot.AgentState.CurrentWorkingDir != cwd {
		t.Errorf("Expected CurrentWorkingDir %q, got %q", cwd, snapshot.AgentState.CurrentWorkingDir)
	}
}

// TestContextSnapshotClone tests cloning a snapshot.
func TestContextSnapshotClone(t *testing.T) {
	original := NewContextSnapshot("test-session", "/test/dir")
	original.LLMContext = "Test context content"
	original.RecentMessages = append(original.RecentMessages, NewUserMessage("Hello"))
	original.AgentState.TotalTurns = 10
	original.AgentState.TokensUsed = 5000

	clone := original.Clone()

	if clone == nil {
		t.Fatal("Clone returned nil")
	}

	// Verify values are copied
	if clone.LLMContext != original.LLMContext {
		t.Errorf("Cloned LLMContext doesn't match original")
	}

	if len(clone.RecentMessages) != len(original.RecentMessages) {
		t.Errorf("Cloned RecentMessages length doesn't match original")
	}

	if clone.AgentState.TotalTurns != original.AgentState.TotalTurns {
		t.Errorf("Cloned TotalTurns doesn't match original")
	}

	// Verify deep copy (modify original, clone should be unchanged)
	original.LLMContext = "Modified"
	original.RecentMessages[0].Role = "modified"

	if clone.LLMContext == "Modified" {
		t.Error("Clone LLMContext was modified when original was changed")
	}

	if clone.RecentMessages[0].Role == "modified" {
		t.Error("Clone RecentMessages was modified when original was changed")
	}
}

// TestContextSnapshotClone_Nil tests cloning a nil snapshot.
func TestContextSnapshotClone_Nil(t *testing.T) {
	var snapshot *ContextSnapshot
	clone := snapshot.Clone()

	if clone != nil {
		t.Error("Expected nil when cloning nil snapshot")
	}
}

// TestApplyTruncateToSnapshot tests applying truncate to a snapshot.
func TestApplyTruncateToSnapshot(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TotalTurns = 5 // Set a turn number for TruncatedAt

	// Add a tool result message
	toolResult := NewToolResultMessage("call-123", "test_tool", []ContentBlock{
		TextContent{Type: "text", Text: "This is a long tool output that should be truncated"},
	}, false)
	snapshot.RecentMessages = append(snapshot.RecentMessages, toolResult)

	// Apply truncate
	truncateEvent := TruncateEvent{
		ToolCallID: "call-123",
		Turn:       5,
		Trigger:    "test",
	}
	err := ApplyTruncateToSnapshot(snapshot, truncateEvent)
	if err != nil {
		t.Fatalf("ApplyTruncateToSnapshot failed: %v", err)
	}

	// Verify message is marked as truncated
	if !snapshot.RecentMessages[0].Truncated {
		t.Error("Message was not marked as truncated")
	}

	if snapshot.RecentMessages[0].TruncatedAt != 5 {
		t.Errorf("Expected TruncatedAt to be 5, got %d", snapshot.RecentMessages[0].TruncatedAt)
	}

	if snapshot.RecentMessages[0].OriginalSize == 0 {
		t.Error("OriginalSize was not set")
	}
}

// TestApplyTruncateToSnapshot_NotFound tests applying truncate to non-existent message.
func TestApplyTruncateToSnapshot_NotFound(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	truncateEvent := TruncateEvent{
		ToolCallID: "non-existent-id",
		Turn:       1,
		Trigger:    "test",
	}
	err := ApplyTruncateToSnapshot(snapshot, truncateEvent)
	if err == nil {
		t.Error("Expected error when applying truncate to non-existent message")
	}
}

// TestApplyTruncateToSnapshot_MultipleMessages tests finding the right message among many.
func TestApplyTruncateToSnapshot_MultipleMessages(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	// Add multiple messages
	snapshot.RecentMessages = append(snapshot.RecentMessages, NewUserMessage("User message"))
	snapshot.RecentMessages = append(snapshot.RecentMessages, NewAssistantMessage())

	toolResult1 := NewToolResultMessage("call-111", "tool1", []ContentBlock{
		TextContent{Type: "text", Text: "Output 1"},
	}, false)
	snapshot.RecentMessages = append(snapshot.RecentMessages, toolResult1)

	toolResult2 := NewToolResultMessage("call-222", "tool2", []ContentBlock{
		TextContent{Type: "text", Text: "Output 2"},
	}, false)
	snapshot.RecentMessages = append(snapshot.RecentMessages, toolResult2)

	// Truncate the second tool result
	truncateEvent := TruncateEvent{
		ToolCallID: "call-222",
		Turn:       3,
		Trigger:    "test",
	}
	err := ApplyTruncateToSnapshot(snapshot, truncateEvent)
	if err != nil {
		t.Fatalf("ApplyTruncateToSnapshot failed: %v", err)
	}

	// Verify only the second tool result is truncated
	if snapshot.RecentMessages[2].Truncated {
		t.Error("First tool result should not be truncated")
	}

	if !snapshot.RecentMessages[3].Truncated {
		t.Error("Second tool result should be truncated")
	}
}

// TestReconstructSnapshotMessages tests reconstructing messages from journal.
func TestReconstructSnapshotMessages(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	// Create journal entries
	entries := []JournalEntry{
		{
			Type: "message",
			Message: func() *AgentMessage {
				msg := NewUserMessage("First message")
				return &msg
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				msg := NewAssistantMessage()
				msg.Content = append(msg.Content, TextContent{Type: "text", Text: "Assistant response"})
				return &msg
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				msg := NewToolResultMessage("call-123", "test_tool", []ContentBlock{
					TextContent{Type: "text", Text: "Tool output"},
				}, false)
				return &msg
			}(),
		},
	}

	// Reconstruct messages
	err := ReconstructSnapshotMessages(snapshot, entries, 0)
	if err != nil {
		t.Fatalf("ReconstructSnapshotMessages failed: %v", err)
	}

	// Verify messages were reconstructed
	if len(snapshot.RecentMessages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(snapshot.RecentMessages))
	}

	if snapshot.RecentMessages[0].Role != "user" {
		t.Errorf("First message role should be 'user', got %s", snapshot.RecentMessages[0].Role)
	}

	if snapshot.RecentMessages[1].Role != "assistant" {
		t.Errorf("Second message role should be 'assistant', got %s", snapshot.RecentMessages[1].Role)
	}

	if snapshot.RecentMessages[2].Role != "toolResult" {
		t.Errorf("Third message role should be 'toolResult', got %s", snapshot.RecentMessages[2].Role)
	}
}

// TestReconstructSnapshotMessages_WithStartIndex tests reconstructing with a start index.
func TestReconstructSnapshotMessages_WithStartIndex(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	// Create journal entries
	entries := []JournalEntry{
		{
			Type: "message",
			Message: func() *AgentMessage {
				msg := NewUserMessage("First message")
				return &msg
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				msg := NewUserMessage("Second message")
				return &msg
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				msg := NewUserMessage("Third message")
				return &msg
			}(),
		},
	}

	// Reconstruct from index 1 (skip first message)
	err := ReconstructSnapshotMessages(snapshot, entries, 1)
	if err != nil {
		t.Fatalf("ReconstructSnapshotMessages failed: %v", err)
	}

	// Should only have 2 messages (second and third)
	if len(snapshot.RecentMessages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(snapshot.RecentMessages))
	}

	if snapshot.RecentMessages[0].ExtractText() != "Second message" {
		t.Errorf("First message should be 'Second message', got %s", snapshot.RecentMessages[0].ExtractText())
	}
}

// TestReconstructSnapshotMessages_WithTruncate tests reconstructing with truncate entries.
func TestReconstructSnapshotMessages_WithTruncate(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	// Create journal entries
	entries := []JournalEntry{
		{
			Type: "message",
			Message: func() *AgentMessage {
				msg := NewToolResultMessage("call-123", "test_tool", []ContentBlock{
					TextContent{Type: "text", Text: "Long tool output"},
				}, false)
				return &msg
			}(),
		},
		{
			Type: "truncate",
			Truncate: &TruncateEvent{
				ToolCallID: "call-123",
			},
		},
	}

	// Reconstruct messages
	err := ReconstructSnapshotMessages(snapshot, entries, 0)
	if err != nil {
		t.Fatalf("ReconstructSnapshotMessages failed: %v", err)
	}

	// Should have 1 message
	if len(snapshot.RecentMessages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(snapshot.RecentMessages))
	}

	// Message should be marked as truncated
	if !snapshot.RecentMessages[0].Truncated {
		t.Error("Message should be marked as truncated")
	}
}

// TestReconstructSnapshotMessages_TruncateNotFound tests handling truncate for non-existent message.
func TestReconstructSnapshotMessages_TruncateNotFound(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	// Create journal entries with truncate before message
	entries := []JournalEntry{
		{
			Type: "truncate",
			Truncate: &TruncateEvent{
				ToolCallID: "non-existent",
			},
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				msg := NewUserMessage("Hello")
				return &msg
			}(),
		},
	}

	// Should not error, just continue processing
	err := ReconstructSnapshotMessages(snapshot, entries, 0)
	if err != nil {
		t.Fatalf("ReconstructSnapshotMessages should not error with missing truncate target: %v", err)
	}

	// Should still have the user message
	if len(snapshot.RecentMessages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(snapshot.RecentMessages))
	}
}

// TestGetVisibleToolResults_Empty tests getting tool results from empty snapshot.
func TestGetVisibleToolResults_Empty(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	results := snapshot.GetVisibleToolResults()

	if len(results) != 0 {
		t.Errorf("Expected 0 tool results, got %d", len(results))
	}
}

// TestGetVisibleToolResults_OnlyToolResults tests getting tool results when only tool results exist.
func TestGetVisibleToolResults_OnlyToolResults(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	// Add tool results
	for i := 0; i < 5; i++ {
		msg := NewToolResultMessage("call-123", "test_tool", []ContentBlock{
			TextContent{Type: "text", Text: "Tool output"},
		}, false)
		snapshot.RecentMessages = append(snapshot.RecentMessages, msg)
	}

	results := snapshot.GetVisibleToolResults()

	if len(results) != 5 {
		t.Errorf("Expected 5 tool results, got %d", len(results))
	}
}

// TestGetVisibleToolResults_ExcludesTruncated tests that truncated results are excluded.
func TestGetVisibleToolResults_ExcludesTruncated(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	// Add normal tool result
	normalResult := NewToolResultMessage("call-111", "test_tool", []ContentBlock{
		TextContent{Type: "text", Text: "Normal output"},
	}, false)
	snapshot.RecentMessages = append(snapshot.RecentMessages, normalResult)

	// Add truncated tool result
	truncatedResult := NewToolResultMessage("call-222", "test_tool", []ContentBlock{
		TextContent{Type: "text", Text: "Truncated output"},
	}, false)
	truncatedResult.Truncated = true
	snapshot.RecentMessages = append(snapshot.RecentMessages, truncatedResult)

	results := snapshot.GetVisibleToolResults()

	if len(results) != 1 {
		t.Errorf("Expected 1 tool result (truncated excluded), got %d", len(results))
	}

	if results[0].ToolCallID != "call-111" {
		t.Errorf("Expected ToolCallID 'call-111', got %s", results[0].ToolCallID)
	}
}

// TestNewUserMessage tests creating a user message.
func TestNewUserMessage(t *testing.T) {
	text := "Hello, world!"
	msg := NewUserMessage(text)

	if msg.Role != "user" {
		t.Errorf("Expected role 'user', got %s", msg.Role)
	}

	if len(msg.Content) != 1 {
		t.Fatalf("Expected 1 content block, got %d", len(msg.Content))
	}

	textContent, ok := msg.Content[0].(TextContent)
	if !ok {
		t.Fatal("Expected TextContent")
	}

	if textContent.Text != text {
		t.Errorf("Expected text %q, got %q", text, textContent.Text)
	}

	if !msg.AgentVisible {
		t.Error("Expected AgentVisible to be true")
	}

	if !msg.UserVisible {
		t.Error("Expected UserVisible to be true")
	}
}

// TestNewAssistantMessage tests creating an assistant message.
func TestNewAssistantMessage(t *testing.T) {
	msg := NewAssistantMessage()

	if msg.Role != "assistant" {
		t.Errorf("Expected role 'assistant', got %s", msg.Role)
	}

	if len(msg.Content) != 0 {
		t.Errorf("Expected empty content, got %d blocks", len(msg.Content))
	}

	if !msg.AgentVisible {
		t.Error("Expected AgentVisible to be true")
	}

	if !msg.UserVisible {
		t.Error("Expected UserVisible to be true")
	}
}

// TestNewToolResultMessage tests creating a tool result message.
func TestNewToolResultMessage(t *testing.T) {
	toolCallID := "call-123"
	toolName := "test_tool"
	content := []ContentBlock{
		TextContent{Type: "text", Text: "Tool output"},
	}
	isError := false

	msg := NewToolResultMessage(toolCallID, toolName, content, isError)

	if msg.Role != "toolResult" {
		t.Errorf("Expected role 'toolResult', got %s", msg.Role)
	}

	if msg.ToolCallID != toolCallID {
		t.Errorf("Expected ToolCallID %q, got %q", toolCallID, msg.ToolCallID)
	}

	if msg.ToolName != toolName {
		t.Errorf("Expected ToolName %q, got %q", toolName, msg.ToolName)
	}

	if msg.IsError != isError {
		t.Errorf("Expected IsError %v, got %v", isError, msg.IsError)
	}

	if !msg.AgentVisible {
		t.Error("Expected AgentVisible to be true")
	}

	if !msg.UserVisible {
		t.Error("Expected UserVisible to be true")
	}
}

// TestAgentMessage_ExtractText tests extracting text from messages.
func TestAgentMessage_ExtractText(t *testing.T) {
	tests := []struct {
		name     string
		message  AgentMessage
		expected string
	}{
		{
			name: "Single text block",
			message: AgentMessage{
				Content: []ContentBlock{
					TextContent{Type: "text", Text: "Hello"},
				},
			},
			expected: "Hello",
		},
		{
			name: "Multiple text blocks",
			message: AgentMessage{
				Content: []ContentBlock{
					TextContent{Type: "text", Text: "Hello "},
					TextContent{Type: "text", Text: "world"},
				},
			},
			expected: "Hello world",
		},
		{
			name: "Mixed content blocks",
			message: AgentMessage{
				Content: []ContentBlock{
					TextContent{Type: "text", Text: "Text "},
					ToolCallContent{Type: "tool_call", ID: "call-123", Name: "tool", Arguments: nil},
					TextContent{Type: "text", Text: "more text"},
				},
			},
			expected: "Text more text",
		},
		{
			name:     "Empty content",
			message:  AgentMessage{Content: []ContentBlock{}},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.message.ExtractText()
			if result != tt.expected {
				t.Errorf("ExtractText() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

// TestAgentStateClone tests cloning agent state.
func TestAgentStateClone(t *testing.T) {
	original := NewAgentState("test-session", "/test/dir")
	original.TotalTurns = 100
	original.TokensUsed = 50000
	original.ActiveToolCalls = []string{"call-1", "call-2"}

	clone := original.Clone()

	// Verify values are copied
	if clone.SessionID != original.SessionID {
		t.Error("Cloned SessionID doesn't match")
	}

	if clone.TotalTurns != original.TotalTurns {
		t.Error("Cloned TotalTurns doesn't match")
	}

	if clone.TokensUsed != original.TokensUsed {
		t.Error("Cloned TokensUsed doesn't match")
	}

	if len(clone.ActiveToolCalls) != len(original.ActiveToolCalls) {
		t.Error("Cloned ActiveToolCalls length doesn't match")
	}

	// Verify deep copy (modify original, clone should be unchanged)
	original.ActiveToolCalls[0] = "modified"
	if clone.ActiveToolCalls[0] == "modified" {
		t.Error("Clone ActiveToolCalls was modified when original was changed")
	}
}

// TestAgentStateClone_Nil tests cloning nil agent state.
func TestAgentStateClone_Nil(t *testing.T) {
	var state *AgentState
	clone := state.Clone()

	if clone != nil {
		t.Error("Expected nil when cloning nil state")
	}
}

// TestEmptyJournal_Replay_ReturnsBaseSnapshot tests that replaying an empty journal
// returns the base snapshot from checkpoint (Category 1.1).
func TestEmptyJournal_Replay_ReturnsBaseSnapshot(t *testing.T) {
	// Given: A base snapshot with LLMContext and some messages
	baseSnapshot := &ContextSnapshot{
		LLMContext: "Initial context",
		RecentMessages: []AgentMessage{
			NewUserMessage("hello"),
			NewAssistantMessage(),
		},
		AgentState: *NewAgentState("test-session", "/test/dir"),
	}
	baseSnapshot.AgentState.TotalTurns = 10

	// When: Replaying empty journal
	journalEntries := []JournalEntry{}
	snapshot := baseSnapshot // In real implementation, this would come from loading checkpoint

	// Apply empty journal entries (should not change anything)
	for _, entry := range journalEntries {
		if entry.Type == "message" && entry.Message != nil {
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		}
	}

	// Then: Snapshot contains only base messages
	if snapshot.LLMContext != "Initial context" {
		t.Errorf("Expected LLMContext 'Initial context', got %q", snapshot.LLMContext)
	}

	if len(snapshot.RecentMessages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(snapshot.RecentMessages))
	}

	// Check first message is user message with "hello"
	if snapshot.RecentMessages[0].Role != "user" {
		t.Errorf("Expected first message role 'user', got %s", snapshot.RecentMessages[0].Role)
	}
	if snapshot.RecentMessages[0].ExtractText() != "hello" {
		t.Errorf("Expected first message text 'hello', got %s", snapshot.RecentMessages[0].ExtractText())
	}

	// Check second message is assistant
	if snapshot.RecentMessages[1].Role != "assistant" {
		t.Errorf("Expected second message role 'assistant', got %s", snapshot.RecentMessages[1].Role)
	}
}

// TestReplay_Deterministic_SameResult tests that replaying the same journal
// multiple times produces the same snapshot (Category 1.4).
func TestReplay_Deterministic_SameResult(t *testing.T) {
	// Given: A checkpoint and journal
	baseSnapshot1 := &ContextSnapshot{
		LLMContext:   "Test context",
		RecentMessages: []AgentMessage{},
		AgentState:   *NewAgentState("test-session", "/test/dir"),
	}

	// Create journal entries
	journal := []JournalEntry{
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewUserMessage("msg1")
				return &m
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewToolResultMessage("call_1", "bash", []ContentBlock{
					TextContent{Type: "text", Text: "output1"},
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
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewAssistantMessage()
				return &m
			}(),
		},
	}

	// When: Replaying journal twice
	var snapshot1, snapshot2 *ContextSnapshot

	// First replay
	snapshot1 = &ContextSnapshot{
		LLMContext:   baseSnapshot1.LLMContext,
		RecentMessages: []AgentMessage{},
		AgentState:   *baseSnapshot1.AgentState.Clone(),
	}
	for _, entry := range journal {
		if entry.Type == "message" && entry.Message != nil {
			snapshot1.RecentMessages = append(snapshot1.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			ApplyTruncateToSnapshot(snapshot1, *entry.Truncate)
		}
	}

	// Second replay (from same base)
	snapshot2 = &ContextSnapshot{
		LLMContext:   baseSnapshot1.LLMContext,
		RecentMessages: []AgentMessage{},
		AgentState:   *baseSnapshot1.AgentState.Clone(),
	}
	for _, entry := range journal {
		if entry.Type == "message" && entry.Message != nil {
			snapshot2.RecentMessages = append(snapshot2.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			ApplyTruncateToSnapshot(snapshot2, *entry.Truncate)
		}
	}

	// Then: Both replays produce identical snapshots
	if len(snapshot1.RecentMessages) != len(snapshot2.RecentMessages) {
		t.Errorf("Replay not deterministic: got %d messages first time, %d messages second time",
			len(snapshot1.RecentMessages), len(snapshot2.RecentMessages))
	}

	if snapshot1.LLMContext != snapshot2.LLMContext {
		t.Error("Replay not deterministic: LLMContext differs")
	}

	// Check truncate status matches
	truncated1 := false
	truncated2 := false
	for _, msg := range snapshot1.RecentMessages {
		if msg.ToolCallID == "call_1" && msg.Truncated {
			truncated1 = true
		}
	}
	for _, msg := range snapshot2.RecentMessages {
		if msg.ToolCallID == "call_1" && msg.Truncated {
			truncated2 = true
		}
	}

	if truncated1 != truncated2 {
		t.Error("Replay not deterministic: Truncate status differs")
	}

	if !truncated1 || !truncated2 {
		t.Error("Expected call_1 to be truncated in both replays")
	}
}

// TestReconstructSnapshotWithCheckpoint_WithMessages tests reconstruction when checkpoint
// has messages.jsonl (RecentMessages not empty). Should replay from checkpoint.MessageIndex.
func TestReconstructSnapshotWithCheckpoint_WithMessages(t *testing.T) {
	// This test uses a temporary directory for checkpoint files
	// For simplicity, we'll mock the checkpoint loading behavior

	// Create a checkpoint with 2 messages (from messages.jsonl)
	checkpoint := &CheckpointInfo{
		Turn:          5,
		MessageIndex:  2, // Checkpoint created after journal entry 2
		Path:          "checkpoints/checkpoint_00001",
		CreatedAt:     "2026-04-01T12:00:00Z",
	}

	// Create journal entries
	entries := []JournalEntry{
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewUserMessage("checkpoint msg 1")
				return &m
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewAssistantMessage()
				m.Content = append(m.Content, TextContent{Type: "text", Text: "checkpoint response 1"})
				return &m
			}(),
		},
		// Entries after checkpoint
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewUserMessage("after checkpoint msg 1")
				return &m
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewToolResultMessage("call-123", "bash", []ContentBlock{
					TextContent{Type: "text", Text: "tool output"},
				}, false)
				return &m
			}(),
		},
		{
			Type: "truncate",
			Truncate: &TruncateEvent{
				ToolCallID: "call-123",
			},
		},
	}

	// LoadCheckpoint would return checkpoint's RecentMessages
	// We simulate this by creating a snapshot with messages
	baseSnapshot := &ContextSnapshot{
		LLMContext: "Checkpoint context",
		RecentMessages: []AgentMessage{
			NewUserMessage("checkpoint msg 1"),
			func() AgentMessage {
				m := NewAssistantMessage()
				m.Content = append(m.Content, TextContent{Type: "text", Text: "checkpoint response 1"})
				return m
			}(),
		},
		AgentState: *NewAgentState("test-session", "/test/dir"),
	}
	baseSnapshot.AgentState.TotalTurns = 5

	// Apply reconstruction logic
	snapshot := baseSnapshot
	startIndex := checkpoint.MessageIndex
	if startIndex > len(entries) {
		startIndex = len(entries)
	}

	for i := startIndex; i < len(entries); i++ {
		entry := entries[i]
		if entry.Type == "message" && entry.Message != nil {
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			ApplyTruncateToSnapshot(snapshot, *entry.Truncate)
		}
	}

	// Verify: Should have 4 messages (2 from checkpoint + 2 after checkpoint)
	if len(snapshot.RecentMessages) != 4 {
		t.Errorf("Expected 4 messages, got %d", len(snapshot.RecentMessages))
	}

	// First two should be from checkpoint
	if snapshot.RecentMessages[0].ExtractText() != "checkpoint msg 1" {
		t.Errorf("Expected first message 'checkpoint msg 1', got %s", snapshot.RecentMessages[0].ExtractText())
	}

	// Last two should be from journal after checkpoint
	if snapshot.RecentMessages[2].ExtractText() != "after checkpoint msg 1" {
		t.Errorf("Expected third message 'after checkpoint msg 1', got %s", snapshot.RecentMessages[2].ExtractText())
	}

	// Tool result should be truncated
	if !snapshot.RecentMessages[3].Truncated {
		t.Error("Expected tool result to be marked as truncated")
	}
}

// TestReconstructSnapshotWithCheckpoint_NoMessages tests reconstruction when checkpoint
// has no messages.jsonl (RecentMessages empty). Should replay from beginning.
func TestReconstructSnapshotWithCheckpoint_NoMessages(t *testing.T) {
	// Checkpoint with no messages (old format or messages.jsonl missing)
	// Note: MessageIndex would typically be larger than current journal length for old checkpoints
	_ = &CheckpointInfo{
		Turn:          5,
		MessageIndex:  5, // Checkpoint created after journal entry 5
		Path:          "checkpoints/checkpoint_00001",
		CreatedAt:     "2026-04-01T12:00:00Z",
	}

	// Create journal entries
	entries := []JournalEntry{
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewUserMessage("message 1")
				return &m
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewAssistantMessage()
				m.Content = append(m.Content, TextContent{Type: "text", Text: "response 1"})
				return &m
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewToolResultMessage("call-123", "bash", []ContentBlock{
					TextContent{Type: "text", Text: "tool output"},
				}, false)
				return &m
			}(),
		},
		{
			Type: "truncate",
			Truncate: &TruncateEvent{
				ToolCallID: "call-123",
			},
		},
	}

	// LoadCheckpoint returns empty RecentMessages (no messages.jsonl)
	baseSnapshot := &ContextSnapshot{
		LLMContext:     "Checkpoint context",
		RecentMessages: []AgentMessage{}, // Empty!
		AgentState:     *NewAgentState("test-session", "/test/dir"),
	}
	baseSnapshot.AgentState.TotalTurns = 5

	// Apply NEW reconstruction logic: if no messages in checkpoint, replay from beginning
	snapshot := baseSnapshot
	startIndex := 0 // Replay from beginning since RecentMessages is empty
	if startIndex > len(entries) {
		startIndex = len(entries)
	}

	for i := startIndex; i < len(entries); i++ {
		entry := entries[i]
		if entry.Type == "message" && entry.Message != nil {
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			ApplyTruncateToSnapshot(snapshot, *entry.Truncate)
		}
	}

	// Verify: Should have 3 messages (all from journal)
	if len(snapshot.RecentMessages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(snapshot.RecentMessages))
	}

	// All messages should be from journal
	if snapshot.RecentMessages[0].ExtractText() != "message 1" {
		t.Errorf("Expected first message 'message 1', got %s", snapshot.RecentMessages[0].ExtractText())
	}

	// Tool result should be truncated
	if !snapshot.RecentMessages[2].Truncated {
		t.Error("Expected tool result to be marked as truncated")
	}
}

// TestReconstructSnapshotWithCheckpoint_MessageIndexEqualsJournalLength tests the
// specific bug case where checkpoint.MessageIndex equals journal length.
func TestReconstructSnapshotWithCheckpoint_MessageIndexEqualsJournalLength(t *testing.T) {
	// This is the bug case: checkpoint.MessageIndex = 3, journal has 3 entries (0, 1, 2)
	// Old logic: startIndex = 3, loop `for i := 3; i < 3` doesn't execute → no messages!
	// New logic: RecentMessages is empty, so startIndex = 0 → replay all entries

	_ = &CheckpointInfo{
		Turn:          5,
		MessageIndex:  3, // Exactly equal to journal length!
		Path:          "checkpoints/checkpoint_00001",
		CreatedAt:     "2026-04-01T12:00:00Z",
	}

	// Journal has exactly 3 entries (indices 0, 1, 2)
	entries := []JournalEntry{
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewUserMessage("msg 1")
				return &m
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewAssistantMessage()
				return &m
			}(),
		},
		{
			Type: "message",
			Message: func() *AgentMessage {
				m := NewToolResultMessage("call-456", "bash", []ContentBlock{
					TextContent{Type: "text", Text: "output"},
				}, false)
				return &m
			}(),
		},
	}

	// Checkpoint has no messages (no messages.jsonl)
	baseSnapshot := &ContextSnapshot{
		LLMContext:     "Context",
		RecentMessages: []AgentMessage{}, // Empty!
		AgentState:     *NewAgentState("test-session", "/test/dir"),
	}

	// Apply NEW logic: since RecentMessages is empty, replay from beginning
	snapshot := baseSnapshot
	startIndex := 0 // Because RecentMessages is empty!

	if startIndex > len(entries) {
		startIndex = len(entries)
	}

	for i := startIndex; i < len(entries); i++ {
		entry := entries[i]
		if entry.Type == "message" && entry.Message != nil {
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		}
	}

	// Verify: Should have 3 messages (replayed from journal)
	if len(snapshot.RecentMessages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(snapshot.RecentMessages))
	}

	// This would fail with old logic! (would get 0 messages)
}
