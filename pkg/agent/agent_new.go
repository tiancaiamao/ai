package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/tools"
	"log/slog"
)

// AgentNew represents the new agent implementation with ContextSnapshot.
type AgentNew struct {
	// Core state
	snapshot   *agentctx.ContextSnapshot
	snapshotMu sync.RWMutex

	// Session
	sessionDir string
	sessionID  string
	journal    *agentctx.Journal

	// Configuration
	model          *ModelSpec
	triggerChecker *agentctx.TriggerChecker

	// Event emission
	eventEmitter EventEmitter

	// Tools
	allTools []agentctx.Tool

	// LLM configuration
	apiKey string
}

// ModelSpec wraps the model specification from config.
type ModelSpec = llm.Model

// EventEmitter is the interface for emitting events during agent execution.
// This is a placeholder - the actual implementation will use the existing event system.
type EventEmitter interface {
	Emit(event AgentEvent)
}

// NewAgentNew creates a new agent with ContextSnapshot architecture.
func NewAgentNew(sessionDir, sessionID string, model *ModelSpec, apiKey string, eventEmitter EventEmitter) (*AgentNew, error) {
	// 1. Load or create snapshot
	snapshot := agentctx.NewContextSnapshot(sessionID, sessionDir)

	// 2. Open journal
	journal, err := agentctx.OpenJournal(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open journal: %w", err)
	}

	// 3. Initialize trigger checker
	triggerChecker := agentctx.NewTriggerChecker()

	// 4. Load all tools
	allTools := loadAllTools()

	// 5. Create agent
	agent := &AgentNew{
		snapshot:       snapshot,
		sessionDir:     sessionDir,
		sessionID:      sessionID,
		journal:        journal,
		model:          model,
		apiKey:         apiKey,
		triggerChecker: triggerChecker,
		eventEmitter:   eventEmitter,
		allTools:       allTools,
	}

	// Update context window in snapshot
	snapshot.AgentState.TokensLimit = model.ContextWindow
	if snapshot.AgentState.TokensLimit == 0 {
		snapshot.AgentState.TokensLimit = 200000 // Default
	}

	return agent, nil
}

// GetSnapshot returns the current snapshot (for testing/inspection).
func (a *AgentNew) GetSnapshot() *agentctx.ContextSnapshot {
	a.snapshotMu.RLock()
	defer a.snapshotMu.RUnlock()
	return a.snapshot
}

// GetSessionID returns the session ID.
func (a *AgentNew) GetSessionID() string {
	return a.sessionID
}

// GetSessionDir returns the session directory.
func (a *AgentNew) GetSessionDir() string {
	return a.sessionDir
}

// loadAllTools loads all available tools from the tools package.
func loadAllTools() []agentctx.Tool {
	return tools.GetAllTools()
}

// createCheckpoint creates a new checkpoint from the current snapshot.
func (a *AgentNew) createCheckpoint(ctx context.Context) error {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	// Get message index from journal
	messageIndex := a.journal.GetLength()

	// Save checkpoint
	checkpointInfo, err := agentctx.SaveCheckpoint(
		a.sessionDir,
		a.snapshot,
		a.snapshot.AgentState.TotalTurns,
		messageIndex,
	)
	if err != nil {
		return err
	}

	// Update snapshot state
	a.snapshot.AgentState.LastCheckpoint = a.snapshot.AgentState.TotalTurns

	// Emit event
	slog.Info("[AgentNew] Checkpoint created",
		"turn", checkpointInfo.Turn,
		"path", checkpointInfo.Path,
	)

	return nil
}

// generateSessionID generates a unique session ID based on timestamp.
func generateSessionID() string {
	return fmt.Sprintf("sess_%d", time.Now().UnixNano())
}

// createSessionDir creates the session directory structure.
func createSessionDir(sessionDir string) error {
	// Create main session directory
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("failed to create session dir: %w", err)
	}

	// Create checkpoints directory
	checkpointsDir := filepath.Join(sessionDir, "checkpoints")
	if err := os.MkdirAll(checkpointsDir, 0755); err != nil {
		return fmt.Errorf("failed to create checkpoints dir: %w", err)
	}

	return nil
}

// updateTurnCount increments the turn counter in the snapshot.
func (a *AgentNew) updateTurnCount() {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	a.snapshot.AgentState.TotalTurns++
	a.snapshot.AgentState.TurnsSinceLastTrigger++
	a.snapshot.AgentState.UpdatedAt = time.Now()
}

// resetTurnTracking resets the turn tracking after a trigger.
func (a *AgentNew) resetTurnTracking() {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	a.snapshot.AgentState.LastTriggerTurn = a.snapshot.AgentState.TotalTurns
	a.snapshot.AgentState.TurnsSinceLastTrigger = 0
}
