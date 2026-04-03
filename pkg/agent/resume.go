package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/tools"
	"log/slog"
)

// LoadSession loads a session from disk or creates a new one.
func LoadSession(ctx context.Context, sessionDir string, model *ModelSpec, apiKey string, eventEmitter EventEmitter) (*AgentNew, error) {
	// Check if session directory exists
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		// New session
		slog.Info("[AgentNew] Creating new session", "sessionDir", sessionDir)
		return createNewSession(sessionDir, model, apiKey, eventEmitter)
	}

	// Try to load existing session
	slog.Info("[AgentNew] Loading existing session", "sessionDir", sessionDir)

	// 1. Load checkpoint index
	idx, err := agentctx.LoadCheckpointIndex(sessionDir)
	if err != nil {
		slog.Warn("[AgentNew] Failed to load checkpoint index, creating new session", "error", err)
		return createNewSession(sessionDir, model, apiKey, eventEmitter)
	}

	// Check if there are any checkpoints
	if idx.GetCheckpointCount() == 0 {
		slog.Info("[AgentNew] No checkpoints found, creating new session")
		return createNewSession(sessionDir, model, apiKey, eventEmitter)
	}

	// 2. Load latest checkpoint
	latestCheckpoint, err := idx.GetLatestCheckpoint()
	if err != nil {
		slog.Warn("[AgentNew] Failed to get latest checkpoint, creating new session", "error", err)
		return createNewSession(sessionDir, model, apiKey, eventEmitter)
	}
	agentctx.LogCheckpointLoaded(ctx, latestCheckpoint.Path, latestCheckpoint.MessageIndex, latestCheckpoint.Turn)

	// 3. Load checkpoint data
	snapshot, err := agentctx.LoadCheckpoint(sessionDir, latestCheckpoint)
	if err != nil {
		slog.Warn("[AgentNew] Failed to load checkpoint, creating new session", "error", err)
		return createNewSession(sessionDir, model, apiKey, eventEmitter)
	}

	// 4. Open journal
	journal, err := agentctx.OpenJournal(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open journal: %w", err)
	}

	// 5. Read full journal and reconstruct from checkpoint.MessageIndex once.
	entries, err := journal.ReadAll()
	if err != nil {
		slog.Warn("[AgentNew] Failed to read journal, using checkpoint only", "error", err)
		// Continue with checkpoint data only
		entries = []agentctx.JournalEntry{}
	}

	// 6. Reconstruct snapshot from journal entries
	if len(entries) > 0 {
		snapshot, err = agentctx.ReconstructSnapshotWithCheckpoint(sessionDir, latestCheckpoint, entries)
		if err != nil {
			slog.Warn("[AgentNew] Failed to reconstruct snapshot, using checkpoint only", "error", err)
			// Reload checkpoint to get clean state
			snapshot, err = agentctx.LoadCheckpoint(sessionDir, latestCheckpoint)
			if err != nil {
				return nil, fmt.Errorf("failed to reload checkpoint: %w", err)
			}
		}
		agentctx.LogSnapshotReconstructed(ctx, latestCheckpoint.Path, len(entries), len(snapshot.RecentMessages))
	}

	// 7. Create agent
	agent := &AgentNew{
		snapshot:       snapshot,
		sessionDir:     sessionDir,
		sessionID:      snapshot.AgentState.SessionID,
		journal:        journal,
		model:          model,
		apiKey:         apiKey,
		triggerChecker: agentctx.NewTriggerChecker(),
		eventEmitter:   eventEmitter,
		allTools:       tools.GetAllTools(),
		thinkingLevel:  "high", // Default; caller should override via SetThinkingLevel
	}

	// Update context window if model provides it
	if model.ContextWindow > 0 {
		snapshot.AgentState.TokensLimit = model.ContextWindow
	}

	slog.Info("[AgentNew] Session loaded successfully",
		"sessionID", snapshot.AgentState.SessionID,
		"turns", snapshot.AgentState.TotalTurns,
		"messages", len(snapshot.RecentMessages),
	)

	return agent, nil
}

// createNewSession creates a new session.
func createNewSession(sessionDir string, model *ModelSpec, apiKey string, eventEmitter EventEmitter) (*AgentNew, error) {
	sessionID := generateSessionID()

	// Create session directory structure
	if err := createSessionDir(sessionDir); err != nil {
		return nil, fmt.Errorf("failed to create session dir: %w", err)
	}

	// Create initial snapshot
	snapshot := agentctx.NewContextSnapshot(sessionID, sessionDir)

	// Set context window from model
	if model.ContextWindow > 0 {
		snapshot.AgentState.TokensLimit = model.ContextWindow
	} else {
		snapshot.AgentState.TokensLimit = 200000 // Default
	}

	// Open journal
	journal, err := agentctx.OpenJournal(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open journal: %w", err)
	}

	// Create initial checkpoint
	snapshot.AgentState.TotalTurns = 0
	checkpointInfo, err := agentctx.SaveCheckpoint(
		sessionDir,
		snapshot,
		0,
		0,
	)
	if err != nil {
		slog.Warn("[AgentNew] Failed to create initial checkpoint", "error", err)
	} else {
		slog.Info("[AgentNew] Initial checkpoint created",
			"path", checkpointInfo.Path,
		)
	}

	// Create agent
	agent := &AgentNew{
		snapshot:       snapshot,
		sessionDir:     sessionDir,
		sessionID:      sessionID,
		journal:        journal,
		model:          model,
		apiKey:         apiKey,
		triggerChecker: agentctx.NewTriggerChecker(),
		eventEmitter:   eventEmitter,
		allTools:       tools.GetAllTools(),
	}

	slog.Info("[AgentNew] New session created",
		"sessionID", sessionID,
		"sessionDir", sessionDir,
	)

	return agent, nil
}

// GetSessionDir returns the session directory for a given working directory.
func GetSessionDir(cwd string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create session directory name from working directory
	// Format: ~/.ai/sessions/--<cwd-hash>--
	sessionName := fmt.Sprintf("--%s--", filepath.Base(cwd))
	sessionDir := filepath.Join(homeDir, ".ai", "sessions", sessionName)

	return sessionDir, nil
}

// SaveSession saves the current session state.
func (a *AgentNew) SaveSession(ctx context.Context) error {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	// Create checkpoint with current state
	checkpointInfo, err := agentctx.SaveCheckpoint(
		a.sessionDir,
		a.snapshot,
		a.snapshot.AgentState.TotalTurns,
		a.journal.GetLength(),
	)

	if err != nil {
		return fmt.Errorf("failed to save checkpoint: %w", err)
	}

	slog.Info("[AgentNew] Session saved",
		"turn", checkpointInfo.Turn,
		"path", checkpointInfo.Path,
	)

	return nil
}

// Close closes the agent and releases resources.
func (a *AgentNew) Close() error {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	if a.journal != nil {
		if err := a.journal.Close(); err != nil {
			slog.Warn("[AgentNew] Failed to close journal", "error", err)
		}
		a.journal = nil
	}

	return nil
}
