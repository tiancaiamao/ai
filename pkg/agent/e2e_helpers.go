package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// NewAgentForE2E creates a new agent instance specifically for end-to-end testing.
// It wraps the existing NewAgentNew function with a simpler interface.
func NewAgentForE2E(
	sessionDir string,
	sessionID string,
	model *llm.Model,
	apiKey string,
) (*AgentNew, error) {
	// Convert llm.Model to ModelSpec
	var modelSpec ModelSpec = *model

	// Create agent using existing constructor
	ag, err := NewAgentNew(sessionDir, sessionID, &modelSpec, apiKey, nil)
	if err != nil {
		return nil, err
	}

	return ag, nil
}

// ResumeAgentForE2E resumes an existing session for end-to-end testing.
func ResumeAgentForE2E(
	ctx context.Context,
	sessionsDir string,
	sessionID string,
) (*AgentNew, error) {
	// Build session path - sessionID is the actual directory name in our tests
	sessionDir := filepath.Join(sessionsDir, sessionID)

	// Check if session exists
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("session not found: %s (path: %s)", sessionID, sessionDir)
	}

	// Load checkpoint
	latestCheckpoint, err := agentctx.LoadLatestCheckpoint(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load latest checkpoint: %w", err)
	}

	snapshot, err := agentctx.LoadCheckpoint(sessionDir, latestCheckpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint data: %w", err)
	}

	// Load journal and replay entries
	journal, err := agentctx.OpenJournal(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open journal: %w", err)
	}

	entries, err := journal.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to read journal: %w", err)
	}

	// Replay all journal entries to reconstruct RecentMessages
	// The checkpoint only has AgentState and LLMContext, not RecentMessages
	if len(entries) > 0 {
		snapshot, err = agentctx.ReconstructSnapshotWithCheckpoint(sessionDir, latestCheckpoint, entries)
		if err != nil {
			return nil, fmt.Errorf("failed to reconstruct snapshot: %w", err)
		}
	}

	// Load real config for model and API key
	configPath, err := getConfigPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get config path: %w", err)
	}

	cfg, err := loadConfigFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	model := cfg.GetLLMModel()
	apiKey, err := resolveAPIKeyFromConfig(model.Provider)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve API key: %w", err)
	}

	// Convert llm.Model to ModelSpec
	var modelSpec ModelSpec = model

	// Create agent from resumed state
	ag := &AgentNew{
		snapshot:       snapshot,
		sessionDir:     sessionDir,
		sessionID:      sessionID,
		journal:        journal,
		model:          &modelSpec,
		apiKey:         apiKey,
		allTools:       loadAllTools(),
		triggerChecker: agentctx.NewTriggerChecker(),
	}

	return ag, nil
}

// WriteTruncateEventToJournal manually writes a truncate event for testing.
func WriteTruncateEventToJournal(sessionDir, sessionID, toolCallID string, turn int) error {
	journal, err := agentctx.OpenJournal(filepath.Join(sessionDir, sessionID))
	if err != nil {
		return err
	}
	defer journal.Close()

	truncateEvent := agentctx.TruncateEvent{
		ToolCallID: toolCallID,
		Turn:       turn,
		Trigger:    "context_management",
	}

	return journal.AppendTruncate(truncateEvent)
}

// GetCurrentWorkingDir gets the current working directory for testing.
func GetCurrentWorkingDir() (string, error) {
	return os.Getwd()
}

// NewContextSnapshotForE2E creates a test context snapshot.
func NewContextSnapshotForE2E(sessionID string) *agentctx.ContextSnapshot {
	cwd, _ := os.Getwd()
	if cwd == "" {
		cwd = "/tmp"
	}

	return &agentctx.ContextSnapshot{
		LLMContext:     "",
		RecentMessages: []agentctx.AgentMessage{},
		AgentState: agentctx.AgentState{
			WorkspaceRoot:     cwd,
			CurrentWorkingDir: cwd,
			TotalTurns:        0,
			TokensUsed:        200,
			TokensLimit:       200000,
			SessionID:         sessionID,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		},
	}
}

// EstimateTokenPercentForE2E estimates token usage percentage.
func EstimateTokenPercentForE2E(snapshot *agentctx.ContextSnapshot) float64 {
	if snapshot.AgentState.TokensLimit == 0 {
		return 0.0
	}
	return float64(snapshot.AgentState.TokensUsed) / float64(snapshot.AgentState.TokensLimit)
}

// Helper functions for loading config in tests
func getConfigPath() (string, error) {
	return config.GetDefaultConfigPath()
}

func loadConfigFromFile(path string) (*config.Config, error) {
	return config.LoadConfig(path)
}

func resolveAPIKeyFromConfig(provider string) (string, error) {
	return config.ResolveAPIKey(provider)
}
