package tools

import (
	"os"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// Registry manages tool registration and lookup.
// It wraps agentctx.ToolRegistry with convenience methods for tool setup.
type Registry struct {
	inner *agentctx.ToolRegistry
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		inner: agentctx.NewToolRegistry(),
	}
}

// Register registers a tool.
func (r *Registry) Register(tool agentctx.Tool) {
	r.inner.Register(tool)
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (agentctx.Tool, bool) {
	return r.inner.Get(name)
}

// All returns all registered tools.
func (r *Registry) All() []agentctx.Tool {
	return r.inner.All()
}

// Inner returns the underlying agentctx.ToolRegistry.
func (r *Registry) Inner() *agentctx.ToolRegistry {
	return r.inner
}

// GetAllTools returns all default tools for the agent.
func GetAllTools() []agentctx.Tool {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		cwd = "."
	}
	ws, _ := NewWorkspace(cwd)

	registry := NewRegistry()

	// Register all standard tools
	registry.Register(NewReadTool(ws))
	registry.Register(NewWriteTool(ws))
	registry.Register(NewEditTool(ws))
	registry.Register(NewBashTool(ws))
	registry.Register(NewGrepTool(ws))
	registry.Register(NewChangeWorkspaceTool(ws))

	return registry.All()
}

// ToLLMTools converts all tools to LLM format.
func (r *Registry) ToLLMTools() []map[string]any {
	tools := r.inner.All()
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name(),
				"description": tool.Description(),
				"parameters":  tool.Parameters(),
			},
		})
	}
	return result
}
