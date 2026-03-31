package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckpoint_SaveLoad_PreservesState tests that saving and loading
// a checkpoint preserves the state (Category 4.1).
func TestCheckpoint_SaveLoad_PreservesState(t *testing.T) {
	// Given: A temporary session directory
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("Failed to create session dir: %v", err)
	}

	// And: A snapshot with state
	originalSnapshot := &ContextSnapshot{
		LLMContext: "This is the LLM context with important information",
		RecentMessages: []AgentMessage{
			NewUserMessage("First message"),
			NewAssistantMessage(),
			NewUserMessage("Second message"),
		},
		AgentState: AgentState{
			WorkspaceRoot:        "/workspace",
			CurrentWorkingDir:    "/workspace/project",
			TotalTurns:           10,
			TokensUsed:           5000,
			TokensLimit:          200000,
			LastLLMContextUpdate: 12345,
			LastCheckpoint:       0,
			LastTriggerTurn:      5,
			TurnsSinceLastTrigger: 5,
		},
	}

	// When: Saving the checkpoint
	info, err := SaveCheckpoint(sessionDir, originalSnapshot, 10, 3)
	if err != nil {
		t.Fatalf("Failed to save checkpoint: %v", err)
	}

	// Then: CheckpointInfo should be correct
	if info.Turn != 10 {
		t.Errorf("Expected turn 10, got %d", info.Turn)
	}
	if info.MessageIndex != 3 {
		t.Errorf("Expected message index 3, got %d", info.MessageIndex)
	}
	if info.LLMContextChars != len(originalSnapshot.LLMContext) {
		t.Errorf("Expected LLMContextChars %d, got %d",
			len(originalSnapshot.LLMContext), info.LLMContextChars)
	}
	if info.RecentMessagesCount != len(originalSnapshot.RecentMessages) {
		t.Errorf("Expected RecentMessagesCount %d, got %d",
			len(originalSnapshot.RecentMessages), info.RecentMessagesCount)
	}

	// When: Loading the checkpoint
	loadedSnapshot, err := LoadCheckpoint(sessionDir, info)
	if err != nil {
		t.Fatalf("Failed to load checkpoint: %v", err)
	}

	// Then: Loaded state should match original state
	if loadedSnapshot.LLMContext != originalSnapshot.LLMContext {
		t.Errorf("LLMContext not preserved: expected %q, got %q",
			originalSnapshot.LLMContext, loadedSnapshot.LLMContext)
	}

	// Note: RecentMessages is empty in loaded snapshot (comes from journal)
	if len(loadedSnapshot.RecentMessages) != 0 {
		t.Errorf("RecentMessages should be empty (loaded from journal), got %d",
			len(loadedSnapshot.RecentMessages))
	}

	// Check AgentState fields
	if loadedSnapshot.AgentState.WorkspaceRoot != originalSnapshot.AgentState.WorkspaceRoot {
		t.Errorf("WorkspaceRoot not preserved: expected %q, got %q",
			originalSnapshot.AgentState.WorkspaceRoot, loadedSnapshot.AgentState.WorkspaceRoot)
	}
	if loadedSnapshot.AgentState.CurrentWorkingDir != originalSnapshot.AgentState.CurrentWorkingDir {
		t.Errorf("CurrentWorkingDir not preserved: expected %q, got %q",
			originalSnapshot.AgentState.CurrentWorkingDir, loadedSnapshot.AgentState.CurrentWorkingDir)
	}
	if loadedSnapshot.AgentState.TotalTurns != originalSnapshot.AgentState.TotalTurns {
		t.Errorf("TotalTurns not preserved: expected %d, got %d",
			originalSnapshot.AgentState.TotalTurns, loadedSnapshot.AgentState.TotalTurns)
	}
	if loadedSnapshot.AgentState.TokensUsed != originalSnapshot.AgentState.TokensUsed {
		t.Errorf("TokensUsed not preserved: expected %d, got %d",
			originalSnapshot.AgentState.TokensUsed, loadedSnapshot.AgentState.TokensUsed)
	}
	if loadedSnapshot.AgentState.TokensLimit != originalSnapshot.AgentState.TokensLimit {
		t.Errorf("TokensLimit not preserved: expected %d, got %d",
			originalSnapshot.AgentState.TokensLimit, loadedSnapshot.AgentState.TokensLimit)
	}
	if loadedSnapshot.AgentState.LastTriggerTurn != originalSnapshot.AgentState.LastTriggerTurn {
		t.Errorf("LastTriggerTurn not preserved: expected %d, got %d",
			originalSnapshot.AgentState.LastTriggerTurn, loadedSnapshot.AgentState.LastTriggerTurn)
	}
}

// TestCurrentSymlink_PointsToLatest tests that the current/ symlink
// points to the latest checkpoint (Category 4.2).
func TestCurrentSymlink_PointsToLatest(t *testing.T) {
	// Given: A temporary session directory
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatalf("Failed to create session dir: %v", err)
	}

	// When: Creating multiple checkpoints
	snapshot1 := &ContextSnapshot{
		LLMContext: "Context at turn 5",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 1"),
		},
		AgentState: *NewAgentState("session", "/workspace"),
	}
	snapshot1.AgentState.TotalTurns = 5

	info1, err := SaveCheckpoint(sessionDir, snapshot1, 5, 1)
	if err != nil {
		t.Fatalf("Failed to save checkpoint 1: %v", err)
	}

	snapshot2 := &ContextSnapshot{
		LLMContext: "Context at turn 10",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 2"),
		},
		AgentState: *NewAgentState("session", "/workspace"),
	}
	snapshot2.AgentState.TotalTurns = 10

	info2, err := SaveCheckpoint(sessionDir, snapshot2, 10, 2)
	if err != nil {
		t.Fatalf("Failed to save checkpoint 2: %v", err)
	}

	snapshot3 := &ContextSnapshot{
		LLMContext: "Context at turn 15",
		RecentMessages: []AgentMessage{
			NewUserMessage("Message 3"),
		},
		AgentState: *NewAgentState("session", "/workspace"),
	}
	snapshot3.AgentState.TotalTurns = 15

	info3, err := SaveCheckpoint(sessionDir, snapshot3, 15, 3)
	if err != nil {
		t.Fatalf("Failed to save checkpoint 3: %v", err)
	}

	// Then: current/ symlink should point to the latest checkpoint (turn 15)
	currentPath, err := GetCurrentCheckpointPath(sessionDir)
	if err != nil {
		t.Fatalf("Failed to get current checkpoint path: %v", err)
	}

	// Verify the path ends with the latest checkpoint directory
	expectedSuffix := info3.Path
	if !strings.HasSuffix(currentPath, expectedSuffix) {
		t.Errorf("current/ symlink points to %q, expected it to end with %q",
			currentPath, expectedSuffix)
	}

	// Verify it doesn't point to older checkpoints
	if strings.HasSuffix(currentPath, info1.Path) {
		t.Error("current/ symlink points to first checkpoint instead of latest")
	}
	if strings.HasSuffix(currentPath, info2.Path) {
		t.Error("current/ symlink points to second checkpoint instead of latest")
	}

	// Verify symlink target is relative (not absolute)
	if filepath.IsAbs(currentPath) {
		// On some systems, GetCurrentCheckpointPath returns absolute path
		// So we need to check if the base matches sessionDir
		// If it's absolute, check that it starts with sessionDir
		if !strings.HasPrefix(currentPath, sessionDir) {
			t.Errorf("Absolute path %q is not under session dir %q", currentPath, sessionDir)
		}
	}
}

// TestCheckpointIndex_AddCheckpoint tests adding checkpoints to the index.
func TestCheckpointIndex_AddCheckpoint(t *testing.T) {
	// Given: An empty checkpoint index
	idx := &CheckpointIndex{
		Checkpoints: []CheckpointInfo{},
	}

	// When: Adding checkpoints
	info1 := CheckpointInfo{
		Turn:         10,
		MessageIndex: 5,
		Path:         "checkpoints/checkpoint_00010",
	}
	info2 := CheckpointInfo{
		Turn:         20,
		MessageIndex: 10,
		Path:         "checkpoints/checkpoint_00020",
	}

	idx.AddCheckpoint(info1)
	idx.AddCheckpoint(info2)

	// Then: Checkpoints should be in order
	if len(idx.Checkpoints) != 2 {
		t.Fatalf("Expected 2 checkpoints, got %d", len(idx.Checkpoints))
	}

	if idx.Checkpoints[0].Turn != 10 {
		t.Errorf("Expected first checkpoint at turn 10, got %d", idx.Checkpoints[0].Turn)
	}
	if idx.Checkpoints[1].Turn != 20 {
		t.Errorf("Expected second checkpoint at turn 20, got %d", idx.Checkpoints[1].Turn)
	}
}

// TestCheckpointIndex_GetCheckpointAtTurn tests getting a checkpoint at a specific turn.
func TestCheckpointIndex_GetCheckpointAtTurn(t *testing.T) {
	// Given: A checkpoint index with multiple checkpoints
	idx := &CheckpointIndex{
		Checkpoints: []CheckpointInfo{
			{Turn: 10, Path: "checkpoints/checkpoint_00010"},
			{Turn: 20, Path: "checkpoints/checkpoint_00020"},
			{Turn: 30, Path: "checkpoints/checkpoint_00030"},
		},
	}

	// When: Getting checkpoint at turn 20
	info, err := idx.GetCheckpointAtTurn(20)
	if err != nil {
		t.Fatalf("Failed to get checkpoint at turn 20: %v", err)
	}

	// Then: Should return the correct checkpoint
	if info.Turn != 20 {
		t.Errorf("Expected turn 20, got %d", info.Turn)
	}
	if info.Path != "checkpoints/checkpoint_00020" {
		t.Errorf("Expected path checkpoints/checkpoint_00020, got %s", info.Path)
	}

	// When: Getting non-existent checkpoint
	_, err = idx.GetCheckpointAtTurn(25)
	if err == nil {
		t.Error("Expected error for non-existent checkpoint, got nil")
	}
}
