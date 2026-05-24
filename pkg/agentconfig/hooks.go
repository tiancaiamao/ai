package agentconfig

import (
	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/middlewares"
)

// BuildHooks creates a HookRegistry from the configured middleware entries.
// Middleware entries with enabled=false or unknown names are silently skipped.
func (c *AgentConfig) BuildHooks() *agent.HookRegistry {
	// Filter to enabled entries only.
	enabled := make([]middlewares.MiddlewareEntry, 0, len(c.Middlewares))
	for _, m := range c.Middlewares {
		if !m.Enabled {
			continue
		}
		// Silently skip unknown middleware names.
		if middlewares.Lookup(m.Name) == nil {
			continue
		}
		enabled = append(enabled, middlewares.MiddlewareEntry{
			Name:   m.Name,
			Params: m.Params,
		})
	}

	if len(enabled) == 0 {
		return nil
	}

	registry, err := middlewares.BuildHooks(enabled)
	if err != nil {
		// Log but don't fail — degraded mode is acceptable.
		return nil
	}
	return registry
}