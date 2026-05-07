package command

import (
	"strings"
	"testing"
)

func TestRegistryRegisterAndGet(t *testing.T) {
	r := New()

	called := false
	r.Register("test", "test command", func(args string) (any, error) {
		called = true
		return "result:" + args, nil
	})

	handler, ok := r.Get("test")
	if !ok {
		t.Fatal("expected handler to be found")
	}

	result, err := handler("hello")
	if err != nil {
		t.Fatal(err)
	}
	if result != "result:hello" {
		t.Fatalf("unexpected result: %v", result)
	}
	if !called {
		t.Fatal("handler was not called")
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := New()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("expected handler not to be found")
	}
}

func TestRegistryList(t *testing.T) {
	r := New()
	r.Register("charlie", "", func(args string) (any, error) { return nil, nil })
	r.Register("alpha", "", func(args string) (any, error) { return nil, nil })
	r.Register("bravo", "", func(args string) (any, error) { return nil, nil })

	names := r.List()
	expected := []string{"alpha", "bravo", "charlie"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d names, got %d", len(expected), len(names))
	}
	for i, name := range names {
		if name != expected[i] {
			t.Fatalf("expected names[%d] = %q, got %q", i, expected[i], name)
		}
	}
}

func TestRegistryOverwrite(t *testing.T) {
	r := New()
	r.Register("cmd", "", func(args string) (any, error) { return "v1", nil })
	r.Register("cmd", "", func(args string) (any, error) { return "v2", nil })

	handler, _ := r.Get("cmd")
	result, _ := handler("")
	if result != "v2" {
		t.Fatalf("expected overwritten handler to return v2, got %v", result)
	}
}

func TestRegistryListCommands(t *testing.T) {
	r := New()
	r.Register("alpha", "first command", func(args string) (any, error) { return nil, nil })
	r.Register("beta", "second command", func(args string) (any, error) { return nil, nil })

	cmds := r.ListCommands()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}
	if cmds[0].Name != "alpha" || cmds[0].Description != "first command" {
		t.Fatalf("unexpected first command: %+v", cmds[0])
	}
	if cmds[1].Name != "beta" || cmds[1].Description != "second command" {
		t.Fatalf("unexpected second command: %+v", cmds[1])
	}
}

func TestRegistryDispatch(t *testing.T) {
	r := New()
	r.Register("echo", "echo args", func(args string) (any, error) {
		return "echo:" + args, nil
	})

	handled, result, err := r.Dispatch("/echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("expected handled")
	}
	if result != "echo:hello" {
		t.Fatalf("unexpected result: %v", result)
	}

	// Unknown command
	handled, _, err = r.Dispatch("/unknown")
	if handled {
		t.Fatal("expected not handled")
	}
}

func TestRegistrySetSubcommands(t *testing.T) {
	r := New()
	r.RegisterSetSub("busy-mode", "Set busy mode", func(args string) (any, error) {
		return "busy:" + args, nil
	})
	r.RegisterSetSub("thinking-level", "Set thinking level", func(args string) (any, error) {
		return "thinking:" + args, nil
	})

	// Dispatch /set busy-mode steer
	result, err := r.DispatchSet("busy-mode steer")
	if err != nil {
		t.Fatal(err)
	}
	if result != "busy:steer" {
		t.Fatalf("unexpected result: %v", result)
	}

	// List set options
	result, err = r.DispatchSet("")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("expected map result")
	}
	opts, ok := m["set-options"].([]CommandInfo)
	if !ok {
		t.Fatal("expected set-options")
	}
	if len(opts) != 2 {
		t.Fatalf("expected 2 set options, got %d", len(opts))
	}
	if opts[0].Name != "busy-mode" {
		t.Fatalf("expected first option 'busy-mode', got %q", opts[0].Name)
	}

	// Unknown setting
	_, err = r.DispatchSet("unknown-key value")
	if err == nil {
		t.Fatal("expected error for unknown setting")
	}
}

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		input   string
		wantCmd string
		wantArgs string
		wantErr bool
	}{
		{"/model gpt-4", "model", "gpt-4", false},
		{"/compact", "compact", "", false},
		{"/set busy-mode steer", "set", "busy-mode steer", false},
		{"/  ", "", "", true},
		{"hello", "", "", true},
		{"", "", "", true},
		{"/thinking high", "thinking", "high", false},
		{"/help", "help", "", false},
	}

	for _, tt := range tests {
		cmd, args, err := ParseSlashCommand(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseSlashCommand(%q): expected error, got cmd=%q args=%q", tt.input, cmd, args)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSlashCommand(%q): unexpected error: %v", tt.input, err)
			continue
		}
		if cmd != tt.wantCmd {
			t.Errorf("ParseSlashCommand(%q): cmd = %q, want %q", tt.input, cmd, tt.wantCmd)
		}
		if args != tt.wantArgs {
			t.Errorf("ParseSlashCommand(%q): args = %q, want %q", tt.input, args, tt.wantArgs)
		}
	}
}

func TestParseSlashCommandWhitespace(t *testing.T) {
	cmd, args, err := ParseSlashCommand("/model   gpt-4  o3")
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "model" {
		t.Fatalf("cmd = %q, want model", cmd)
	}
	// Leading whitespace after command should be trimmed, but internal/trailing preserved
	if !strings.HasPrefix(args, "gpt-4") {
		t.Fatalf("args = %q, want to start with 'gpt-4'", args)
	}
}