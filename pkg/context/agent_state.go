package context

// AgentState represents system-maintained metadata about the agent state.
// Most fields are recomputed from RecentMessages every turn
// (see injectRuntimeMeta); persistence is not required.
type AgentState struct {
	// Workspace
	WorkspaceRoot     string
	CurrentWorkingDir string

	// Statistics (recomputed each turn from RecentMessages)
	TotalTurns  int
	TokensUsed  int
	TokensLimit int

	// Tracking
	// ToolCallsSinceLastTrigger drives the LLMDecide ask interval.
	ToolCallsSinceLastTrigger int

	// Runtime metadata (cached snapshot, rebuilt on band heartbeat)
	RuntimeMetaTurns    int
	RuntimeMetaSnapshot string
	RuntimeMetaBand     string
}

// NewAgentState creates a new AgentState rooted at cwd.
func NewAgentState(cwd string) *AgentState {
	return &AgentState{
		WorkspaceRoot:             cwd,
		CurrentWorkingDir:         cwd,
		ToolCallsSinceLastTrigger: 0,
	}
}
