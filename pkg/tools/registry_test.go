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

func (t *mockTool) Name() string                                       { return t.name }
func (t *mockTool) Description() string                                 { return t.description }
func (t *mockTool) Parameters() map[string]any                          { return t.parameters }
func (t *mockTool) Execute(_ context.Context, _ map[string]any) ([]agentctx.ContentBlock, error) {
	return nil, nil
}

// Verify mockTool satisfies the interface
var _ agentctx.Tool = (*mockTool)(nil)

// ---------------------------------------------------------------------------
// NewRegistry
// ---------------------------------------------------------------------------

func TestNewRegistry_Empty(t *testing.T) {
	r := NewRegistry()
	if len(r.All()) != 0 {
		t.Error("new registry should be empty")
	}
}

// ---------------------------------------------------------------------------
// Register / Get
// ---------------------------------------------------------------------------

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	tool := &mockTool{name: "bash", description: "run commands"}
	r.Register(tool)

	got, ok := r.Get("bash")
	if !ok {
		t.Fatal("Get(bash) should find registered tool")
	}
	if got.Name() != "bash" {
		t.Errorf("Get(bash).Name() = %q, want %q", got.Name(), "bash")
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("Get(nonexistent) should return false")
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

	for _, name := range []string{"bash", "grep", "read"} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("Get(%q) should find tool", name)
		}
	}
}

func TestRegistry_RegisterOverwrites(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "tool", description: "v1"})
	r.Register(&mockTool{name: "tool", description: "v2"})

	got, ok := r.Get("tool")
	if !ok {
		t.Fatal("Get(tool) should find tool")
	}
	if got.Description() != "v2" {
		t.Errorf("Description() = %q, want %q (second registration)", got.Description(), "v2")
	}
}

// ---------------------------------------------------------------------------
// All
// ---------------------------------------------------------------------------

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

// All should return a copy, not the internal map
func TestRegistry_AllReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "x"})

	all := r.All()
	all[0] = &mockTool{name: "y"} // mutate the slice

	// Original should be unchanged
	got, _ := r.Get("x")
	if got.Name() != "x" {
		t.Error("mutating All() slice should not affect registry")
	}
}

// ---------------------------------------------------------------------------
// ToLLMTools
// ---------------------------------------------------------------------------

func TestRegistry_ToLLMTools(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{
		name:        "bash",
		description: "Run a command",
		parameters:  map[string]any{"type": "object"},
	})

	llmTools := r.ToLLMTools()
	if len(llmTools) != 1 {
		t.Fatalf("ToLLMTools() count = %d, want 1", len(llmTools))
	}

	tool := llmTools[0]
	if tool["type"] != "function" {
		t.Errorf("type = %v, want function", tool["type"])
	}

	fn, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatal("function should be a map")
	}
	if fn["name"] != "bash" {
		t.Errorf("name = %v, want bash", fn["name"])
	}
	if fn["description"] != "Run a command" {
		t.Errorf("description = %v, want 'Run a command'", fn["description"])
	}
	if fn["parameters"] == nil {
		t.Error("parameters should not be nil")
	}
}

func TestRegistry_ToLLMTools_Empty(t *testing.T) {
	r := NewRegistry()
	llmTools := r.ToLLMTools()
	if len(llmTools) != 0 {
		t.Errorf("ToLLMTools() count = %d, want 0", len(llmTools))
	}
}

func TestRegistry_ToLLMTools_Multiple(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockTool{name: "bash", description: "shell", parameters: map[string]any{"type": "object"}})
	r.Register(&mockTool{name: "grep", description: "search", parameters: map[string]any{"type": "object"}})

	llmTools := r.ToLLMTools()
	if len(llmTools) != 2 {
		t.Fatalf("ToLLMTools() count = %d, want 2", len(llmTools))
	}

	names := map[string]bool{}
	for _, tool := range llmTools {
		fn := tool["function"].(map[string]any)
		names[fn["name"].(string)] = true
	}
	if !names["bash"] || !names["grep"] {
		t.Errorf("tool names = %v, want {bash, grep}", names)
	}
}