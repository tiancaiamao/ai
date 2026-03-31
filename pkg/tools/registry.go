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

// Get returns a tool by name.
func (r *Registry) Get(name string) (agentctx.Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// All returns all registered tools.
func (r *Registry) All() []agentctx.Tool {
	tools := make([]agentctx.Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// GetAllTools returns all default tools for the agent.
func GetAllTools() []agentctx.Tool {
	registry := NewRegistry()

	// Register all standard tools
	registry.Register(&ReadTool{})
	registry.Register(&WriteTool{})
	registry.Register(&EditTool{})
	registry.Register(&BashTool{})
	registry.Register(&GrepTool{})
	registry.Register(&LLMContextRecallTool{})
	registry.Register(&ChangeWorkspaceTool{})
	registry.Register(&TaskTrackingTool{})

	return registry.All()
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
