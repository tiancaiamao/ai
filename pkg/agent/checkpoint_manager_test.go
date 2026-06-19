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

	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("Hello"),
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

	// Verify agent_state.json exists in session root
	statePath := filepath.Join(tmpDir, "agent_state.json")
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Error("agent_state.json should exist in session root")
	}

	// Verify it can be loaded
	loaded, err := agentctx.LoadAgentState(tmpDir)
	if err != nil {
		t.Fatalf("LoadAgentState: %v", err)
	}
	if loaded.TotalTurns != 5 {
		t.Errorf("loaded TotalTurns = %d, want 5", loaded.TotalTurns)
	}
}

func TestCheckpointManager_ShouldCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}

	if !mgr.ShouldCheckpoint() {
		t.Error("ShouldCheckpoint should return true when enabled")
	}
}

func TestCheckpointManager_ShouldCheckpoint_Disabled(t *testing.T) {
	mgr, err := NewAgentContextCheckpointManager("")
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}

	if mgr.ShouldCheckpoint() {
		t.Error("ShouldCheckpoint should return false when disabled")
	}
}
