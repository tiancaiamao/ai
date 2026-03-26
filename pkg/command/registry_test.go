package command

import (
	"context"
	"sync"
	"testing"
)

// mockAgent is a simple mock for testing
type mockAgent struct {
	name string
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	registry := NewRegistry()
	ctx := context.Background()
	agent := &mockAgent{name: "test"}
	sessionKey := "test-session"
	cmdCtx := NewSimpleCommandContext(agent, sessionKey)

	handler := func(ctx context.Context, cmdCtx CommandContext, args string) (string, error) {
		return "hello " + args, nil
	}

	// Test Register
	registry.Register("test", "test command", handler)

	// Test Get
	h, exists := registry.Get("test")
	if !exists {
		t.Fatalf("expected command to exist")
	}

	result, err := h(ctx, cmdCtx, "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello world" {
		t.Fatalf("expected 'hello world', got '%s'", result)
	}
}

func TestRegistry_List(t *testing.T) {
	registry := NewRegistry()

	registry.Register("cmd1", "command 1", func(_ context.Context, _ CommandContext, _ string) (string, error) { return "", nil })
	registry.Register("cmd2", "command 2", func(_ context.Context, _ CommandContext, _ string) (string, error) { return "", nil })
	registry.Register("cmd3", "command 3", func(_ context.Context, _ CommandContext, _ string) (string, error) { return "", nil })

	list := registry.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(list))
	}

	// Check that all commands are present
	cmdMap := make(map[string]bool)
	for _, cmd := range list {
		cmdMap[cmd] = true
	}

	for _, cmd := range []string{"cmd1", "cmd2", "cmd3"} {
		if !cmdMap[cmd] {
			t.Fatalf("expected command %s to be in list", cmd)
		}
	}
}

func TestRegistry_ListDescriptors(t *testing.T) {
	registry := NewRegistry()

	registry.Register("help", "Show help", func(_ context.Context, _ CommandContext, _ string) (string, error) { return "", nil })
	registry.Register("clear", "Clear context", func(_ context.Context, _ CommandContext, _ string) (string, error) { return "", nil })

	descriptors := registry.ListDescriptors()
	if len(descriptors) != 2 {
		t.Fatalf("expected 2 descriptors, got %d", len(descriptors))
	}

	// Check descriptor content
	descMap := make(map[string]Descriptor)
	for _, desc := range descriptors {
		descMap[desc.Name] = desc
	}

	if descMap["help"].Description != "Show help" {
		t.Fatalf("unexpected description for help: %s", descMap["help"].Description)
	}
	if descMap["clear"].Description != "Clear context" {
		t.Fatalf("unexpected description for clear: %s", descMap["clear"].Description)
	}
}

func TestRegistry_Handle(t *testing.T) {
	registry := NewRegistry()
	ctx := context.Background()
	agent := &mockAgent{name: "test"}
	sessionKey := "test-session"
	cmdCtx := NewSimpleCommandContext(agent, sessionKey)

	handler := func(ctx context.Context, cmdCtx CommandContext, args string) (string, error) {
		return "result: " + args, nil
	}

	registry.Register("test", "test command", handler)

	result, err := registry.Handle(ctx, "test", "arg1", cmdCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "result: arg1" {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestRegistry_Handle_NotFound(t *testing.T) {
	registry := NewRegistry()
	ctx := context.Background()
	cmdCtx := NewSimpleCommandContext(nil, "")

	_, err := registry.Handle(ctx, "nonexistent", "args", cmdCtx)
	if err == nil {
		t.Fatalf("expected error for nonexistent command")
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	registry := NewRegistry()

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "cmd" + string(rune(i))
			registry.Register(name, "test", func(_ context.Context, _ CommandContext, _ string) (string, error) {
				return "", nil
			})
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			registry.List()
			registry.ListDescriptors()
		}()
	}

	wg.Wait()

	// Verify that all commands are registered (at least some)
	list := registry.List()
	if len(list) < numGoroutines/2 {
		t.Fatalf("expected at least %d commands to be registered, got %d", numGoroutines/2, len(list))
	}
}

// TestSimpleCommandContext verifies that SimpleCommandContext works correctly
func TestSimpleCommandContext(t *testing.T) {
	agent := &mockAgent{name: "test"}
	sessionKey := "test-session"
	cmdCtx := NewSimpleCommandContext(agent, sessionKey)

	retrievedAgent := cmdCtx.GetAgent()
	retrievedSessionKey := cmdCtx.GetSessionKey()

	if retrievedAgent != agent {
		t.Fatalf("expected agent to be the same instance")
	}
	if retrievedSessionKey != sessionKey {
		t.Fatalf("expected session key %s, got %s", sessionKey, retrievedSessionKey)
	}
}