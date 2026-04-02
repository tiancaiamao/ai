package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/tools"
	"github.com/tiancaiamao/ai/pkg/traceevent"
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
	thinkingLevel  string
	maxTurns       int // 0 means unlimited, only used in headless mode

	// Event emission
	eventEmitter EventEmitter

	// Tools
	allTools []agentctx.Tool

	// LLM configuration
	apiKey string

	// Retry configuration
	maxLLMRetries  int           // Maximum retries for LLM calls (0 = use default)
	retryBaseDelay time.Duration // Base delay for exponential backoff

	// Pending inputs (for steer/follow-up)
	pendingInputMu sync.RWMutex
	pendingInput   string // For steer: new message to process
	hasPendingInput bool
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
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		cwd = sessionDir
	}
	snapshot := agentctx.NewContextSnapshot(sessionID, cwd)

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
		thinkingLevel:  "medium", // Default thinking level
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

// SetPendingInput sets a pending input message (for steer/follow-up).
// This is called when the user sends new input during tool execution.
func (a *AgentNew) SetPendingInput(message string) {
	a.pendingInputMu.Lock()
	defer a.pendingInputMu.Unlock()
	a.pendingInput = message
	a.hasPendingInput = true
	slog.Info("[AgentNew] Pending input set",
		"message", message,
		"hasPendingInput", a.hasPendingInput,
	)
}

// GetAndClearPendingInput gets and clears any pending input message.
// This is called by the conversation loop after each tool execution.
// Returns (message, hasPending) where hasPending indicates if there was a pending input.
func (a *AgentNew) GetAndClearPendingInput() (string, bool) {
	a.pendingInputMu.Lock()
	defer a.pendingInputMu.Unlock()
	message := a.pendingInput
	hasPending := a.hasPendingInput
	a.pendingInput = ""
	a.hasPendingInput = false
	if hasPending {
		slog.Info("[AgentNew] Pending input retrieved", "message", message)
	}
	return message, hasPending
}

// HasPendingInput checks if there's a pending input message.
func (a *AgentNew) HasPendingInput() bool {
	a.pendingInputMu.RLock()
	defer a.pendingInputMu.RUnlock()
	return a.hasPendingInput
}

// GetSessionDir returns the session directory.
func (a *AgentNew) GetSessionDir() string {
	return a.sessionDir
}

// SetThinkingLevel sets the thinking level for LLM responses.
func (a *AgentNew) SetThinkingLevel(level string) (string, error) {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	level = strings.ToLower(strings.TrimSpace(level))
	valid := map[string]bool{
		"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true,
	}
	if !valid[level] {
		return "", fmt.Errorf("invalid thinking level: %s", level)
	}
	a.thinkingLevel = level
	return level, nil
}

// GetThinkingLevel returns the current thinking level.
func (a *AgentNew) GetThinkingLevel() string {
	a.snapshotMu.RLock()
	defer a.snapshotMu.RUnlock()
	return a.thinkingLevel
}

// SetMaxTurns sets the maximum number of turns allowed (0 means unlimited).
// This is typically used in headless mode to prevent infinite loops.
func (a *AgentNew) SetMaxTurns(maxTurns int) {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()
	a.maxTurns = maxTurns
}

// GetMaxTurns returns the current maximum turns setting (0 means unlimited).
func (a *AgentNew) GetMaxTurns() int {
	a.snapshotMu.RLock()
	defer a.snapshotMu.RUnlock()
	return a.maxTurns
}

// SetMaxLLMRetries sets the maximum number of retries for LLM calls.
// If set to 0, uses the default (1 for regular errors, 8 for rate limits).
// If set to -1, retry is disabled entirely.
func (a *AgentNew) SetMaxLLMRetries(maxRetries int) {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()
	a.maxLLMRetries = maxRetries
}

// GetMaxLLMRetries returns the current max LLM retries setting.
func (a *AgentNew) GetMaxLLMRetries() int {
	a.snapshotMu.RLock()
	defer a.snapshotMu.RUnlock()
	return a.maxLLMRetries
}

// loadAllTools loads all available tools from the tools package.
func loadAllTools() []agentctx.Tool {
	return tools.GetAllTools()
}

// createCheckpoint creates a new checkpoint from the current snapshot.
func (a *AgentNew) createCheckpoint(ctx context.Context) error {
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
	agentctx.LogCheckpointCreated(ctx, checkpointInfo)

	return nil
}

// performCompaction performs message compaction using the compact.Compactor.
// This updates the snapshot's RecentMessages and LLMContext, and appends a compact event to the journal.
// The caller must hold the snapshot lock.
func (a *AgentNew) performCompaction(ctx context.Context) error {
	beforeCount := len(a.snapshot.RecentMessages)
	beforeTokens := a.snapshot.EstimateTokens()

	slog.Info("[AgentNew] Starting compaction",
		"message_count", beforeCount,
		"tokens", beforeTokens,
	)

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_compaction_started",
		traceevent.Field{Key: "message_count", Value: beforeCount},
		traceevent.Field{Key: "tokens", Value: beforeTokens},
	)

	// Create compactor
	systemPrompt := prompt.BuildSystemPrompt(agentctx.ModeNormal)
	compactor := compact.NewCompactor(
		compact.DefaultConfig(),
		*a.model,  // Dereference pointer
		a.apiKey,
		systemPrompt,
		a.model.ContextWindow,
	)

	// Perform compaction
	result, err := compactor.Compact(a.snapshot.RecentMessages, a.snapshot.LLMContext)
	if err != nil {
		return fmt.Errorf("compaction failed: %w", err)
	}

	// Update snapshot
	a.snapshot.LLMContext = result.Summary
	a.snapshot.RecentMessages = result.Messages
	a.snapshot.AgentState.LastLLMContextUpdate = a.snapshot.AgentState.TotalTurns
	a.snapshot.AgentState.UpdatedAt = time.Now()

	// Append compact event to journal
	compactEvent := agentctx.CompactEvent{
		Summary:          result.Summary,
		KeptMessageCount: len(result.Messages),
		Turn:             a.snapshot.AgentState.TotalTurns,
		Timestamp:        time.Now().Format(time.RFC3339),
	}
	slog.Info("[AgentNew] Appending compact event to journal",
		"turn", compactEvent.Turn,
		"kept_message_count", compactEvent.KeptMessageCount,
		"summary_chars", len(compactEvent.Summary),
	)
	if err := a.journal.AppendCompact(compactEvent); err != nil {
		slog.Error("[AgentNew] Failed to append compact event to journal", "error", err)
		return fmt.Errorf("failed to append compact event: %w", err)
	}
	slog.Info("[AgentNew] Compact event appended to journal successfully",
		"journal_length", a.journal.GetLength(),
	)

	// Create checkpoint to persist LLMContext to llm_context.txt
	// This ensures that the summary is saved to disk for inspection and recovery
	messageIndex := a.journal.GetLength()
	checkpointInfo, err := agentctx.SaveCheckpoint(
		a.sessionDir,
		a.snapshot,
		a.snapshot.AgentState.TotalTurns,
		messageIndex,
	)
	if err != nil {
		return fmt.Errorf("failed to create checkpoint after compaction: %w", err)
	}

	slog.Info("[AgentNew] Compaction completed",
		"before_count", beforeCount,
		"after_count", len(result.Messages),
		"before_tokens", result.TokensBefore,
		"after_tokens", result.TokensAfter,
		"checkpoint", checkpointInfo.Path,
	)

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_compaction_completed",
		traceevent.Field{Key: "before_count", Value: beforeCount},
		traceevent.Field{Key: "after_count", Value: len(result.Messages)},
		traceevent.Field{Key: "before_tokens", Value: result.TokensBefore},
		traceevent.Field{Key: "after_tokens", Value: result.TokensAfter},
		traceevent.Field{Key: "summary_chars", Value: len(result.Summary)},
		traceevent.Field{Key: "checkpoint", Value: checkpointInfo.Path},
	)
	agentctx.LogCheckpointCreated(ctx, checkpointInfo)

	return nil
}

// Compact performs message compaction (callable from outside the agent).
// This is used by the /compact command and for auto-compaction.
func (a *AgentNew) Compact(ctx context.Context) error {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	return a.performCompaction(ctx)
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
	a.snapshot.AgentState.ToolCallsSinceLastTrigger = 0
}
