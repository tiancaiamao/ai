package middlewares

import (
	"fmt"
	"sync"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// BeforeModelFactory creates a BeforeModelHook from params.
type BeforeModelFactory func(params map[string]any) (agent.BeforeModelHook, error)

// AfterToolFactory creates an AfterToolHook from params.
type AfterToolFactory func(params map[string]any) (agent.AfterToolHook, error)

// AfterAgentFactory creates an AfterAgentHook from params.
type AfterAgentFactory func(params map[string]any) (agent.AfterAgentHook, error)

// MiddlewareSpec describes a registered middleware.
// Any combination of the three factory fields may be set;
// unset factories are ignored during hook construction.
type MiddlewareSpec struct {
	Name        string
	BeforeModel BeforeModelFactory
	AfterTool   AfterToolFactory
	AfterAgent  AfterAgentFactory
}

var (
	registryMu sync.RWMutex
	registry   = map[string]MiddlewareSpec{}
)

// Register adds a MiddlewareSpec to the global registry.
// Panics if a middleware with the same name is already registered.
func Register(spec MiddlewareSpec) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[spec.Name]; exists {
		panic(fmt.Sprintf("middleware already registered: %s", spec.Name))
	}
	registry[spec.Name] = spec
}

// Lookup returns the MiddlewareSpec for the given name, or nil if not found.
func Lookup(name string) *MiddlewareSpec {
	registryMu.RLock()
	defer registryMu.RUnlock()
	if spec, ok := registry[name]; ok {
		return &spec
	}
	return nil
}

// buildAfterToolHooks is a convenience used by agentconfig to construct
// AfterToolHook instances from a list of (name, params) pairs.
// Unknown names are silently skipped.
func buildAfterToolHooks(entries []MiddlewareEntry) ([]agent.AfterToolHook, error) {
	var hooks []agent.AfterToolHook
	for _, e := range entries {
		spec := Lookup(e.Name)
		if spec == nil || spec.AfterTool == nil {
			continue
		}
		h, err := spec.AfterTool(e.Params)
		if err != nil {
			return nil, fmt.Errorf("middleware %q AfterTool init: %w", e.Name, err)
		}
		hooks = append(hooks, h)
	}
	return hooks, nil
}

// buildBeforeModelHooks constructs BeforeModelHook instances.
func buildBeforeModelHooks(entries []MiddlewareEntry) ([]agent.BeforeModelHook, error) {
	var hooks []agent.BeforeModelHook
	for _, e := range entries {
		spec := Lookup(e.Name)
		if spec == nil || spec.BeforeModel == nil {
			continue
		}
		h, err := spec.BeforeModel(e.Params)
		if err != nil {
			return nil, fmt.Errorf("middleware %q BeforeModel init: %w", e.Name, err)
		}
		hooks = append(hooks, h)
	}
	return hooks, nil
}

// buildAfterAgentHooks constructs AfterAgentHook instances.
func buildAfterAgentHooks(entries []MiddlewareEntry) ([]agent.AfterAgentHook, error) {
	var hooks []agent.AfterAgentHook
	for _, e := range entries {
		spec := Lookup(e.Name)
		if spec == nil || spec.AfterAgent == nil {
			continue
		}
		h, err := spec.AfterAgent(e.Params)
		if err != nil {
			return nil, fmt.Errorf("middleware %q AfterAgent init: %w", e.Name, err)
		}
		hooks = append(hooks, h)
	}
	return hooks, nil
}

// MiddlewareEntry represents a middleware reference from config.
type MiddlewareEntry struct {
	Name   string         `json:"name" yaml:"name"`
	Params map[string]any `json:"params,omitempty" yaml:"params,omitempty"`
}

// BuildHooks constructs a HookRegistry from a list of middleware entries.
// Unknown middleware names and disabled entries are silently skipped.
func BuildHooks(entries []MiddlewareEntry) (*agent.HookRegistry, error) {
	reg := &agent.HookRegistry{}

	bm, err := buildBeforeModelHooks(entries)
	if err != nil {
		return nil, err
	}
	reg.BeforeModelHooks = bm

	at, err := buildAfterToolHooks(entries)
	if err != nil {
		return nil, err
	}
	reg.AfterToolHooks = at

	aa, err := buildAfterAgentHooks(entries)
	if err != nil {
		return nil, err
	}
	reg.AfterAgentHooks = aa

	return reg, nil
}

// ensureContentSlice returns the content slice, allocating if nil.
func ensureContentSlice(msg *agentctx.AgentMessage) {
	if msg.Content == nil {
		msg.Content = []agentctx.ContentBlock{}
	}
}
