package context

import "context"

// AgentMode represents the operational mode of the agent.
type AgentMode string

const (
	// ModeNormal is the default mode for task execution.
	ModeNormal AgentMode = "normal"

	// ModeContextMgmt is the mode for context management operations.
	ModeContextMgmt AgentMode = "context_management"
)

// Tool is the interface that all tools must implement.
type Tool interface {
	// Name returns the tool name.
	Name() string
	// Description returns a description of what the tool does.
	Description() string
	// Parameters returns the JSON schema for the tool's parameters.
	Parameters() map[string]any
	// Execute executes the tool with the given parameters.
	Execute(ctx context.Context, params map[string]any) ([]ContentBlock, error)
}
