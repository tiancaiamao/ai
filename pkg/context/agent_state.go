package context

import "time"

// AgentState represents system-maintained metadata about the agent state.
type AgentState struct {
	// Workspace
	WorkspaceRoot     string
	CurrentWorkingDir string

	// Statistics
	TotalTurns   int
	TokensUsed   int
	TokensLimit  int

	// Tracking
	LastLLMContextUpdate       int // Last turn when LLMContext was updated
	LastCheckpoint             int // Last turn when checkpoint was created
	LastTriggerTurn            int // Last turn when context management was triggered
	TurnsSinceLastTrigger      int // Turns elapsed since last trigger (legacy)
	ToolCallsSinceLastTrigger  int // Tool calls elapsed since last trigger

	// Active tool calls (for pairing protection)
	ActiveToolCalls []string

	// Metadata
	SessionID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewAgentState creates a new AgentState
func NewAgentState(sessionID, cwd string) *AgentState {
	now := time.Now()
	return &AgentState{
		WorkspaceRoot:        cwd,
		CurrentWorkingDir:    cwd,
		TotalTurns:           0,
		TokensUsed:           0,
		TokensLimit:          200000, // Default, will be updated
		LastLLMContextUpdate: 0,
		LastCheckpoint:       0,
		LastTriggerTurn:      0,
		TurnsSinceLastTrigger: 0,
		ActiveToolCalls:      []string{},
		SessionID:            sessionID,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

// Clone creates a deep copy of the AgentState.
func (a *AgentState) Clone() *AgentState {
	if a == nil {
		return nil
	}
	// Copy ActiveToolCalls slice
	activeToolCalls := make([]string, len(a.ActiveToolCalls))
	copy(activeToolCalls, a.ActiveToolCalls)

	return &AgentState{
		WorkspaceRoot:            a.WorkspaceRoot,
		CurrentWorkingDir:        a.CurrentWorkingDir,
		TotalTurns:               a.TotalTurns,
		TokensUsed:               a.TokensUsed,
		TokensLimit:              a.TokensLimit,
		LastLLMContextUpdate:     a.LastLLMContextUpdate,
		LastCheckpoint:           a.LastCheckpoint,
		LastTriggerTurn:          a.LastTriggerTurn,
		TurnsSinceLastTrigger:    a.TurnsSinceLastTrigger,
		ToolCallsSinceLastTrigger: a.ToolCallsSinceLastTrigger,
		ActiveToolCalls:          activeToolCalls,
		SessionID:                a.SessionID,
		CreatedAt:                a.CreatedAt,
		UpdatedAt:                a.UpdatedAt,
	}
}
