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

	// Create a snapshot
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("Hello"),
			agentctx.NewAssistantMessage(),
		},
		AgentState: agentctx.NewAgentState("test-session", "/workspace"),
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

func TestCheckpointManager_ShouldCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}

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