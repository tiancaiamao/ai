package context

// AgentContext is a legacy compatibility structure used by claw adapter.
// New architecture code should prefer ContextSnapshot directly.
type AgentContext struct {
	SystemPrompt          string
	Messages              []AgentMessage
	LastCompactionSummary string
	Tools                 []Tool
}

// NewAgentContext creates a legacy agent context placeholder.
func NewAgentContext(systemPrompt string) *AgentContext {
	return &AgentContext{
		SystemPrompt: systemPrompt,
		Messages:     make([]AgentMessage, 0),
		Tools:        make([]Tool, 0),
	}
}

// AddMessage appends a message to the legacy context.
func (c *AgentContext) AddMessage(msg AgentMessage) {
	if c == nil {
		return
	}
	c.Messages = append(c.Messages, msg)
}

// AddTool appends a tool to the legacy context.
func (c *AgentContext) AddTool(tool Tool) {
	if c == nil {
		return
	}
	c.Tools = append(c.Tools, tool)
}
