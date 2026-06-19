package agent

import (
	"fmt"
	"log/slog"
	"os"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// AgentContextCheckpointManager persists AgentState to the session directory.
type AgentContextCheckpointManager struct {
	sessionDir string
	enabled    bool
}

// NewAgentContextCheckpointManager creates a new checkpoint manager.
// If sessionDir is empty, checkpoint is disabled.
func NewAgentContextCheckpointManager(sessionDir string) (*AgentContextCheckpointManager, error) {
	if sessionDir == "" {
		return &AgentContextCheckpointManager{enabled: false}, nil
	}

	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session dir: %w", err)
	}

	return &AgentContextCheckpointManager{
		sessionDir: sessionDir,
		enabled:    true,
	}, nil
}

// CreateSnapshot persists the current AgentState to agent_state.json.
func (m *AgentContextCheckpointManager) CreateSnapshot(
	agentCtx *agentctx.AgentContext,
	totalTurns int,
) (int, error) {
	if !m.enabled {
		return totalTurns, nil
	}

	agentState := agentCtx.AgentState.Clone()
	agentState.TotalTurns = totalTurns

	if err := agentctx.SaveAgentState(m.sessionDir, agentState); err != nil {
		return 0, fmt.Errorf("failed to save agent state: %w", err)
	}

	return totalTurns, nil
}

// ShouldCheckpoint returns true if checkpoint persistence is enabled.
func (m *AgentContextCheckpointManager) ShouldCheckpoint() bool {
	return m.enabled
}

// initCheckpointManager creates a checkpoint manager from LoopConfig.
// Returns nil manager (no error) if checkpoint is disabled or no session dir.
func initCheckpointManager(config *LoopConfig) *AgentContextCheckpointManager {
	if !config.EnableCheckpoint || config.GetSessionDir == nil {
		return nil
	}
	mgr, err := NewAgentContextCheckpointManager(config.GetSessionDir())
	if err != nil {
		slog.Warn("[Loop] Failed to initialize checkpoint manager", "error", err)
		return nil
	}
	return mgr
}

// saveCheckpointAfterCompaction persists AgentState after compaction.
func saveCheckpointAfterCompaction(mgr *AgentContextCheckpointManager, agentCtx *agentctx.AgentContext, turnCount int, trigger string) {
	if mgr == nil || !mgr.ShouldCheckpoint() {
		return
	}
	if _, err := mgr.CreateSnapshot(agentCtx, turnCount); err != nil {
		slog.Warn("[Loop] Failed to save agent state after compaction", "error", err, "turn", turnCount)
	} else {
		slog.Info("[Loop] Agent state saved after compaction", "trigger", trigger, "turn", turnCount)
	}
}
