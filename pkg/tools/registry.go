package tools

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// Registry manages tool registration and lookup.
type Registry struct {
	tools map[string]agentctx.Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]agentctx.Tool),
	}
}

// Register registers a tool.
func (r *Registry) Register(tool agentctx.Tool) {
	r.tools[tool.Name()] = tool
}

// All returns all registered tools.
func (r *Registry) All() []agentctx.Tool {
	tools := make([]agentctx.Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}
