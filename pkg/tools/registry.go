package tools

import (
	"github.com/tiancaiamao/ai/pkg/agent"
)

// Registry manages tool registration and lookup.
type Registry struct {
	tools map[string]agent.Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]agent.Tool),
	}
}

// Register registers a tool.
func (r *Registry) Register(tool agent.Tool) {
	r.tools[tool.Name()] = tool
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (agent.Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// All returns all registered tools.
func (r *Registry) All() []agent.Tool {
	tools := make([]agent.Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// ToLLMTools converts all tools to LLM format.
func (r *Registry) ToLLMTools() []map[string]any {
	tools := make([]map[string]any, 0)
	for _, tool := range r.tools {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name(),
				"description": tool.Description(),
				"parameters":  tool.Parameters(),
			},
		})
	}
	return tools
}

// RegisterSubagent registers the subagent tool with the given runner function.
// This is a convenience method that handles the dependency injection.
func (r *Registry) RegisterSubagent(cwd string, runSubagent RunSubagentFunc) {
	tool := NewSubagentTool(cwd, nil, r, runSubagent)
	r.Register(tool)
}
