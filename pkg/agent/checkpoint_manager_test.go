package agent

import (
	"context"
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
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("Hello"),
		agentctx.NewAssistantMessage(),
		agentctx.NewUserMessage("World"),
	}
	agentCtx := &agentctx.AgentContext{
		RecentMessages: messages,
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}

	turn, err := mgr.CreateSnapshot(agentCtx, 5)
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
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("Initial"),
	}

	agentCtx := &agentctx.AgentContext{
		RecentMessages: messages,
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}
	_, _ = mgr.CreateSnapshot(agentCtx, 1)

	// Append more messages
	for i := 0; i < 3; i++ {
		msg := agentctx.NewUserMessage("Message")
		if err := mgr.AppendMessage(msg); err != nil {
			t.Fatalf("Failed to append message: %v", err)
		}
	}

	// Reconstruct
	_, recoveredMessages, recoveredTurns, err := mgr.Reconstruct()
	if err != nil {
		t.Fatalf("Failed to reconstruct: %v", err)
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

	// ShouldCheckpoint now always returns true when enabled (event-driven)
	if !mgr.ShouldCheckpoint() {
		t.Error("ShouldCheckpoint should return true when enabled")
	}
}

func TestCheckpointManager_ShouldCheckpoint_Disabled(t *testing.T) {
	mgr, err := NewAgentContextCheckpointManager("")
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}

	// ShouldCheckpoint returns false when disabled (empty sessionDir)
	if mgr.ShouldCheckpoint() {
		t.Error("ShouldCheckpoint should return false when disabled")
	}
}

// alwaysCompactCompactor is a test compactor that always reports ShouldCompact=true.
type alwaysCompactCompactor struct{}

func (a *alwaysCompactCompactor) ShouldCompact(_ context.Context, _ *agentctx.AgentContext) bool {
	return true
}

func (a *alwaysCompactCompactor) Compact(_ context.Context, _ *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	return &agentctx.CompactionResult{Summary: "test compaction"}, nil
}

func (a *alwaysCompactCompactor) CalculateDynamicThreshold() int {
	return 0
}

// TestCreateSnapshot_PersistsJournalLength verifies that CreateSnapshot saves
// MessageIndex equal to the current journal length, so that a subsequent
// Reconstruct() can correctly replay only entries written AFTER the checkpoint.
func TestCreateSnapshot_PersistsJournalLength(t *testing.T) {
	sessionDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(sessionDir)
	if err != nil {
		t.Fatalf("NewAgentContextCheckpointManager: %v", err)
	}
	defer mgr.Close()

	// Simulate the production write path: messages go directly into the
	// session journal, bypassing checkpointMgr.AppendMessage. Append 3 user
	// messages via Journal (same on-disk format as Session writes).
	for i := 0; i < 3; i++ {
		if err := mgr.journal.AppendMessage(agentctx.NewUserMessage("pre")); err != nil {
			t.Fatalf("AppendMessage %d: %v", i, err)
		}
	}

	// Create a snapshot — this should record MessageIndex = 3 (current
	// journal length), so future replays know where the snapshot ends.
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{agentctx.NewUserMessage("dummy")},
		AgentState:     agentctx.NewAgentState("test", "/workspace"),
	}
	if _, err := mgr.CreateSnapshot(agentCtx, 1); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	// Load the saved checkpoint and verify MessageIndex.
	cpInfo, err := agentctx.LoadLatestCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("LoadLatestCheckpoint: %v", err)
	}
	if cpInfo.MessageIndex != 3 {
		t.Errorf("checkpoint.MessageIndex = %d, want 3 (journal length at snapshot time)", cpInfo.MessageIndex)
	}

	// Additionally verify by appending 2 more messages and reconstructing:
	// we should get 1 (snapshot) + 2 (replayed) = 3 messages, NOT 1 + 5 = 6.
	for i := 0; i < 2; i++ {
		if err := mgr.journal.AppendMessage(agentctx.NewUserMessage("post")); err != nil {
			t.Fatalf("AppendMessage post %d: %v", i, err)
		}
	}

	_, msgs, _, err := mgr.Reconstruct()
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	// Snapshot has 1 message ("dummy"); replay adds 2 post-checkpoint entries.
	// If MessageIndex were 0 (the bug), replay would add all 5 journal messages
	// and we'd see 1 + 5 = 6.
	if got := len(msgs); got != 3 {
		t.Errorf("after Reconstruct: message count = %d, want 3 (1 snapshot + 2 replayed)", got)
		for i, m := range msgs {
			t.Logf("  msg[%d] role=%s text=%q", i, m.Role, firstText(m))
		}
	}
}
