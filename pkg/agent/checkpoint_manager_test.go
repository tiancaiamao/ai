package agent

import (
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestCheckpointManager_CreateSnapshot(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Create a snapshot
	llmContext := "# Current Task\nTest task"
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("Hello"),
		agentctx.NewAssistantMessage(),
		agentctx.NewUserMessage("World"),
	}

	turn, err := mgr.CreateSnapshot(llmContext, messages, 5)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	if turn != 5 {
		t.Errorf("Expected turn 5, got %d", turn)
	}

	// Verify checkpoint directory exists
	checkpointsDir := filepath.Join(tmpDir, "checkpoints")
	if _, err := os.Stat(checkpointsDir); os.IsNotExist(err) {
		t.Error("Checkpoints directory should exist")
	}

	// Verify current/ symlink exists
	currentLink := filepath.Join(tmpDir, "current")
	if _, err := os.Lstat(currentLink); os.IsNotExist(err) {
		t.Error("current/ symlink should exist")
	}
}

func TestCheckpointManager_JournalAppend(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Append messages
	msg1 := agentctx.NewUserMessage("Message 1")
	if err := mgr.AppendMessage(msg1); err != nil {
		t.Fatalf("Failed to append message: %v", err)
	}

	msg2 := agentctx.NewAssistantMessage()
	if err := mgr.AppendMessage(msg2); err != nil {
		t.Fatalf("Failed to append message: %v", err)
	}

	// Verify journal file exists
	journalPath := filepath.Join(tmpDir, "messages.jsonl")
	if _, err := os.Stat(journalPath); os.IsNotExist(err) {
		t.Error("Journal file should exist")
	}
}

func TestCheckpointManager_Reconstruct(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Create initial snapshot
	llmContext := "# Current Task\nInitial task"
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("Initial"),
	}

	_, _ = mgr.CreateSnapshot(llmContext, messages, 1)

	// Append more messages
	for i := 0; i < 3; i++ {
		msg := agentctx.NewUserMessage("Message")
		if err := mgr.AppendMessage(msg); err != nil {
			t.Fatalf("Failed to append message: %v", err)
		}
	}

	// Reconstruct
	recoveredLLMContext, recoveredMessages, recoveredTurns, err := mgr.Reconstruct()
	if err != nil {
		t.Fatalf("Failed to reconstruct: %v", err)
	}

	if recoveredLLMContext != llmContext {
		t.Errorf("LLMContext mismatch: expected %q, got %q", llmContext, recoveredLLMContext)
	}

	// Should have 1 + 3 = 4 messages
	if len(recoveredMessages) != 4 {
		t.Errorf("Expected 4 messages, got %d", len(recoveredMessages))
	}

	if recoveredTurns != 1 {
		t.Errorf("Expected turns 1, got %d", recoveredTurns)
	}
}

func TestCheckpointManager_ShouldCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Initially should not checkpoint (turn 0 < 10)
	if mgr.ShouldCheckpoint(0) {
		t.Error("Should not checkpoint at turn 0")
	}

	// Should checkpoint at turn 10
	if !mgr.ShouldCheckpoint(10) {
		t.Error("Should checkpoint at turn 10")
	}

	// After checkpoint, should not checkpoint again until turn 20
	_, _ = mgr.CreateSnapshot("test", nil, 10)
	if mgr.ShouldCheckpoint(15) {
		t.Error("Should not checkpoint at turn 15 (last checkpoint at 10)")
	}

	// Should checkpoint at turn 20
	if !mgr.ShouldCheckpoint(20) {
		t.Error("Should checkpoint at turn 20")
	}
}