package agent

import (
	"fmt"
	"log/slog"
	"os"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// AgentContextCheckpointManager manages checkpoint integration with AgentContext.
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

// CreateSnapshot creates a checkpoint from current AgentContext state.
// Returns turn number and error.
func (m *AgentContextCheckpointManager) CreateSnapshot(
	agentCtx *agentctx.AgentContext,
	totalTurns int,
) (int, error) {
	if !m.enabled {
		return totalTurns, nil
	}

	agentState := agentCtx.AgentState.Clone()
	agentState.TotalTurns = totalTurns

	info, err := agentctx.SaveCheckpoint(m.sessionDir, &agentctx.ContextSnapshot{
		AgentState: agentState,
	}, totalTurns)
	if err != nil {
		return 0, fmt.Errorf("failed to save checkpoint: %w", err)
	}

	return info.Turn, nil
}

// ShouldCheckpoint returns true if an event-driven checkpoint should be created.
// Always returns true when enabled — the caller decides when to trigger.
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

// saveCheckpointAfterCompaction creates a checkpoint after compaction to
// persist the current AgentState (turn count, tokens, CWD, etc.).
func saveCheckpointAfterCompaction(mgr *AgentContextCheckpointManager, agentCtx *agentctx.AgentContext, turnCount int, trigger string) {
	if mgr == nil || !mgr.ShouldCheckpoint() {
		return
	}
	if _, err := mgr.CreateSnapshot(agentCtx, turnCount); err != nil {
		slog.Warn("[Loop] Failed to create checkpoint after compaction", "error", err, "turn", turnCount)
	} else {
		slog.Info("[Loop] Checkpoint created after compaction", "trigger", trigger, "turn", turnCount)
	}
}