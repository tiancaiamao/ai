package agent

import (
	"context"

	"github.com/tiancaiamao/ai/pkg/skill"
)

// AgentContext represents the context for agent execution.
type AgentContext struct {
	SystemPrompt             string                `json:"systemPrompt,omitempty"`
	Messages                 []AgentMessage        `json:"messages"`
	Tools                    []Tool                `json:"tools,omitempty"`
	Skills                   []skill.Skill         `json:"skills,omitempty"` // Loaded skills
	LastCompactionSummary    string                `json:"lastCompactionSummary,omitempty"` // Last compaction summary for incremental updates

	// allowedTools restricts which tools can be executed.
	// nil means all tools are allowed, non-nil is a whitelist.
	allowedTools map[string]bool `json:"-"`
}

// Tool represents a tool that can be called by the agent.
type Tool interface {
	// Name returns the tool name.
	Name() string

	// Description returns a description of what the tool does.
	Description() string

	// Parameters returns the JSON Schema for the tool parameters.
	Parameters() map[string]any

	// Execute executes the tool with the given arguments.
	Execute(ctx context.Context, args map[string]any) ([]ContentBlock, error)
}

// NewAgentContext creates a new AgentContext with the given system prompt.
func NewAgentContext(systemPrompt string) *AgentContext {
	return &AgentContext{
		SystemPrompt: systemPrompt,
		Messages:     make([]AgentMessage, 0),
		Tools:        make([]Tool, 0),
		Skills:       make([]skill.Skill, 0),
	}
}

// NewAgentContextWithSkills creates a new AgentContext with skills.
func NewAgentContextWithSkills(systemPrompt string, skills []skill.Skill) *AgentContext {
	ctx := NewAgentContext(systemPrompt)
	ctx.Skills = skills

	// Append skills to system prompt
	if len(skills) > 0 {
		skillsText := skill.FormatForPrompt(skills)
		if skillsText != "" {
			ctx.SystemPrompt = systemPrompt + skillsText
		}
	}

	return ctx
}

// AddMessage adds a message to the context.
func (c *AgentContext) AddMessage(message AgentMessage) {
	c.Messages = append(c.Messages, message)
}

// AddTool adds a tool to the context.
func (c *AgentContext) AddTool(tool Tool) {
	if tool == nil {
		return
	}
	name := tool.Name()
	for _, existing := range c.Tools {
		if existing == nil {
			continue
		}
		if existing.Name() == name {
			return
		}
	}
	c.Tools = append(c.Tools, tool)
}

// GetTool returns a tool by name, or nil if not found.
func (c *AgentContext) GetTool(name string) Tool {
	for _, tool := range c.Tools {
		if tool.Name() == name {
			return tool
		}
	}
	return nil
}

// SetAllowedTools sets the whitelist of allowed tools.
// Pass nil to allow all tools (default behavior).
func (c *AgentContext) SetAllowedTools(tools []string) {
	if tools == nil {
		c.allowedTools = nil
		return
	}
	c.allowedTools = make(map[string]bool)
	for _, name := range tools {
		c.allowedTools[name] = true
	}
}

// IsToolAllowed checks if a tool is allowed to be executed.
// Returns true if allowedTools is nil (all allowed) or if the tool is in the whitelist.
func (c *AgentContext) IsToolAllowed(name string) bool {
	if c.allowedTools == nil {
		return true
	}
	return c.allowedTools[name]
}

// GetAllowedTools returns the list of allowed tool names.
// Returns nil if all tools are allowed.
func (c *AgentContext) GetAllowedTools() []string {
	if c.allowedTools == nil {
		return nil
	}
	result := make([]string, 0, len(c.allowedTools))
	for name := range c.allowedTools {
		result = append(result, name)
	}
	return result
}

// GetAllowedToolsMap returns the internal allowed tools map for efficient lookup.
// Returns nil if all tools are allowed.
func (c *AgentContext) GetAllowedToolsMap() map[string]bool {
	return c.allowedTools
}
