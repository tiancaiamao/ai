package tools

import (
	"context"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// mockTool implements agentctx.Tool for testing.
type mockTool struct {
	name        string
	description string
	parameters  map[string]any
}

func (t *mockTool) Name() string               { return t.name }
func (t *mockTool) Description() string        { return t.description }
func (t *mockTool) Parameters() map[string]any { return t.parameters }
func (t *mockTool) Execute(_ context.Context, _ map[string]any) ([]agentctx.ContentBlock, error) {
	return nil, nil
}

// Verify mockTool satisfies the interface
var _ agentctx.Tool = (*mockTool)(nil)

func TestNewRegistry_Empty(t *testing.T) {
	r := NewRegistry()
	if len(r.All()) != 0 {
		t.Error("new registry should be empty")
	}
}

func TestRegistry_RegisterMultiple(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "bash"})
	r.Register(&mockTool{name: "grep"})
	r.Register(&mockTool{name: "read"})

	if len(r.All()) != 3 {
		t.Errorf("All() count = %d, want 3", len(r.All()))
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "a"})
	r.Register(&mockTool{name: "b"})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("All() count = %d, want 2", len(all))
	}

	names := map[string]bool{}
	for _, tool := range all {
		names[tool.Name()] = true
	}
	if !names["a"] || !names["b"] {
		t.Errorf("All() names = %v, want {a, b}", names)
	}
}
