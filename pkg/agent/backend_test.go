package agent

import (
	"context"
	"encoding/json"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// ---------------------------------------------------------------------------
// CommandRegistry tests
// ---------------------------------------------------------------------------

func TestCommandRegistry_RegisterAndLookup(t *testing.T) {
	r := NewCommandRegistry()

	r.Register("prompt", func(ctx context.Context, cmd Command) (any, error) {
		return "prompt handled", nil
	}, CommandMeta{Name: "prompt", Description: "send a prompt", Source: "builtin"})

	handler, ok := r.Lookup("prompt")
	if !ok {
		t.Fatal("expected to find 'prompt' handler")
	}
	result, err := handler(context.Background(), Command{Name: "prompt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "prompt handled" {
		t.Fatalf("expected 'prompt handled', got %v", result)
	}
}

func TestCommandRegistry_Handle(t *testing.T) {
	r := NewCommandRegistry()

	r.Register("echo", func(ctx context.Context, cmd Command) (any, error) {
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(cmd.Payload, &payload); err != nil {
			return nil, err
		}
		return payload.Text, nil
	}, CommandMeta{Name: "echo"})

	payload, _ := json.Marshal(map[string]string{"text": "hello"})
	result, err := r.Handle(context.Background(), Command{Name: "echo", Payload: payload})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Fatalf("expected 'hello', got %v", result)
	}
}

func TestCommandRegistry_HandleNotFound(t *testing.T) {
	r := NewCommandRegistry()

	_, err := r.Handle(context.Background(), Command{Name: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unregistered command")
	}

	var notFound ErrCommandNotFound
	if !isErrCommandNotFound(err, &notFound) {
		t.Fatalf("expected ErrCommandNotFound, got %T: %v", err, err)
	}
	if notFound.Command != "nonexistent" {
		t.Fatalf("expected command name 'nonexistent', got %q", notFound.Command)
	}
}

func isErrCommandNotFound(err error, target *ErrCommandNotFound) bool {
	if e, ok := err.(ErrCommandNotFound); ok {
		*target = e
		return true
	}
	return false
}

func TestCommandRegistry_ListCommands(t *testing.T) {
	r := NewCommandRegistry()

	r.Register("zebra", nil, CommandMeta{Name: "zebra", Source: "builtin"})
	r.Register("alpha", nil, CommandMeta{Name: "alpha", Source: "builtin"})
	r.Register("middle", nil, CommandMeta{Name: "middle", Source: "skill"})

	commands := r.ListCommands()
	if len(commands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(commands))
	}

	// Should be sorted by name
	if commands[0].Name != "alpha" || commands[1].Name != "middle" || commands[2].Name != "zebra" {
		t.Fatalf("expected sorted order, got %v", commands)
	}
}

func TestCommandRegistry_ListNames(t *testing.T) {
	r := NewCommandRegistry()

	r.Register("c", nil, CommandMeta{Name: "c"})
	r.Register("a", nil, CommandMeta{Name: "a"})
	r.Register("b", nil, CommandMeta{Name: "b"})

	names := r.ListNames()
	expected := []string{"a", "b", "c"}
	for i, n := range expected {
		if names[i] != n {
			t.Fatalf("expected names[%d] = %q, got %q", i, n, names[i])
		}
	}
}

func TestCommandRegistry_Overwrite(t *testing.T) {
	r := NewCommandRegistry()

	called := ""
	r.Register("cmd", func(ctx context.Context, cmd Command) (any, error) {
		called = "first"
		return nil, nil
	}, CommandMeta{Name: "cmd"})

	r.Register("cmd", func(ctx context.Context, cmd Command) (any, error) {
		called = "second"
		return nil, nil
	}, CommandMeta{Name: "cmd"})

	handler, _ := r.Lookup("cmd")
	handler(context.Background(), Command{Name: "cmd"})
	if called != "second" {
		t.Fatalf("expected 'second' (overwrite), got %q", called)
	}
}

// ---------------------------------------------------------------------------
// ToolRegistry tests
// ---------------------------------------------------------------------------

type mockTool struct {
	name   string
	desc   string
	params map[string]any
}

func (m *mockTool) Name() string               { return m.name }
func (m *mockTool) Description() string        { return m.desc }
func (m *mockTool) Parameters() map[string]any { return m.params }
func (m *mockTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	return nil, nil
}

func TestToolRegistry_RegisterAndGet(t *testing.T) {
	r := NewToolRegistry()

	tool := &mockTool{name: "read", desc: "read a file"}
	r.Register(tool)

	got, ok := r.Get("read")
	if !ok {
		t.Fatal("expected to find 'read' tool")
	}
	if got.Name() != "read" {
		t.Fatalf("expected 'read', got %q", got.Name())
	}
}

func TestToolRegistry_GetNotFound(t *testing.T) {
	r := NewToolRegistry()

	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("expected not to find 'nonexistent'")
	}
}

func TestToolRegistry_RegisterAll(t *testing.T) {
	r := NewToolRegistry()

	r.RegisterAll([]agentctx.Tool{
		&mockTool{name: "read"},
		&mockTool{name: "write"},
		&mockTool{name: "bash"},
	})

	if len(r.All()) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(r.All()))
	}
}

func TestToolRegistry_Names(t *testing.T) {
	r := NewToolRegistry()

	r.Register(&mockTool{name: "grep"})
	r.Register(&mockTool{name: "edit"})
	r.Register(&mockTool{name: "bash"})

	names := r.Names()
	expected := []string{"bash", "edit", "grep"}
	for i, n := range expected {
		if names[i] != n {
			t.Fatalf("expected names[%d] = %q, got %q", i, n, names[i])
		}
	}
}
