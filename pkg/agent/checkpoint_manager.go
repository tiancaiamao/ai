package agent

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// AgentContextCheckpointManager manages checkpoint integration with AgentContext.
type AgentContextCheckpointManager struct {
	sessionDir   string
	journal      *agentctx.Journal
	lastTurn     int
	messageIndex int
	enabled      bool
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

	journal, err := agentctx.OpenJournal(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open journal: %w", err)
	}

	return &AgentContextCheckpointManager{
		sessionDir: sessionDir,
		journal:    journal,
		lastTurn:   0,
		enabled:    true,
	}, nil
}

// AppendMessage appends a message to the journal.
func (m *AgentContextCheckpointManager) AppendMessage(msg agentctx.AgentMessage) error {
	if !m.enabled {
		return nil
	}
	m.messageIndex++
	return m.journal.AppendMessage(msg)
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

	// Capture the full AgentState from the running context.
	agentState := agentCtx.AgentState.Clone()
	agentState.TotalTurns = totalTurns

	snapshot := &agentctx.ContextSnapshot{
		RecentMessages: agentCtx.RecentMessages,
		AgentState:     agentState,
	}

	// Persist MessageIndex = current journal length, so that future
	// Reconstruct() calls replay only entries written AFTER this checkpoint.
	// The legacy m.messageIndex counter (incremented by AppendMessage) is
	// unreliable in production: the Session write path bypasses AppendMessage,
	// leaving the counter at 0 forever. Reading the on-disk journal length
	// gives the correct value regardless of which writer produced the entries.
	messageIndex := m.messageIndex
	if m.journal != nil {
		if n := m.journal.GetLength(); n > messageIndex {
			messageIndex = n
		}
	}

	info, err := agentctx.SaveCheckpoint(m.sessionDir, snapshot, totalTurns, messageIndex)
	if err != nil {
		return 0, fmt.Errorf("failed to save checkpoint: %w", err)
	}

	m.lastTurn = totalTurns
	return info.Turn, nil
}

// Reconstruct reconstructs AgentContext state from checkpoint + journal.
// This should be called after loading checkpoint to get the latest state.
func (m *AgentContextCheckpointManager) Reconstruct() (
	llmContext string,
	messages []agentctx.AgentMessage,
	totalTurns int,
	err error,
) {
	if !m.enabled {
		return "", nil, 0, nil
	}

	info, err := agentctx.LoadLatestCheckpoint(m.sessionDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, 0, nil
		}
		return "", nil, 0, err
	}

	entries, err := m.journal.ReadAll()
	if err != nil {
		return "", nil, 0, fmt.Errorf("failed to read journal: %w", err)
	}

	snapshot, err := agentctx.ReconstructSnapshotWithCheckpoint(m.sessionDir, info, entries)
	if err != nil {
		return "", nil, 0, fmt.Errorf("failed to reconstruct snapshot: %w", err)
	}

	m.lastTurn = snapshot.AgentState.TotalTurns
	m.messageIndex = len(entries)

	return "", snapshot.RecentMessages, snapshot.AgentState.TotalTurns, nil
}

// ShouldCheckpoint returns true if an event-driven checkpoint should be created.
// Always returns true when enabled — the caller decides when to trigger.
func (m *AgentContextCheckpointManager) ShouldCheckpoint() bool {
	return m.enabled
}

// Close closes the journal file.
func (m *AgentContextCheckpointManager) Close() error {
	if !m.enabled || m.journal == nil {
		return nil
	}
	return m.journal.Close()
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
// The checkpoint's messages.jsonl is not used for resume (messages come from
// sess.GetMessages() via compaction snapshot refs); only agent_state.json
// is consumed by LoadResumeState.
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
