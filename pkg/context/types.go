package context

// AgentMode represents the operational mode of the agent.
type AgentMode string

const (
	// ModeNormal is the default mode for task execution.
	ModeNormal AgentMode = "normal"

	// ModeContextMgmt is the mode for context management operations.
	ModeContextMgmt AgentMode = "context_management"
)
