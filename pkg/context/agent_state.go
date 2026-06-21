package context

import "time"

// AgentState represents system-maintained metadata about the agent state.
type AgentState struct {
	// Workspace
	WorkspaceRoot     string
	CurrentWorkingDir string

	// Statistics
	TotalTurns  int
	TokensUsed  int
	TokensLimit int

	// Tracking
	LastCheckpoint            int // Last turn when checkpoint was created
	LastTriggerTurn           int // Last turn when context management was triggered
	TurnsSinceLastTrigger     int // Turns elapsed since last trigger
	ToolCallsSinceLastTrigger int // Tool calls elapsed since last trigger

	// Context management statistics
	TotalTruncations int // Total number of truncate_messages operations
	TotalCompactions int // Total number of full compact operations
	LastCompactTurn  int // Last turn when full compact was executed

	// Active tool calls (for pairing protection)
	ActiveToolCalls []string

	// Runtime metadata (for telemetry snapshot)
	RuntimeMetaTurns    int    // Turns since last runtime metadata refresh
	RuntimeMetaSnapshot string // Cached runtime metadata snapshot
	RuntimeMetaBand     string // Current token usage band

	// Metadata
	SessionID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewAgentState creates a new AgentState.
func NewAgentState(sessionID, cwd string) *AgentState {
	now := time.Now()
	return &AgentState{
		WorkspaceRoot:             cwd,
		CurrentWorkingDir:         cwd,
		TotalTurns:                0,
		TokensUsed:                0,
		TokensLimit:               200000,
		LastCheckpoint:            0,
		LastTriggerTurn:           0,
		TurnsSinceLastTrigger:     0,
		ToolCallsSinceLastTrigger: 0,
		ActiveToolCalls:           []string{},
				RuntimeMetaTurns:          0,
		RuntimeMetaSnapshot:       "",
		RuntimeMetaBand:           "",
		SessionID:                 sessionID,
		CreatedAt:                 now,
		UpdatedAt:                 now,
	}
}
