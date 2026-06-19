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

func TestRegistryListCommands(t *testing.T) {
	r := New()
	r.Register("bravo", "second command", func(args string) (any, error) { return nil, nil })
	r.Register("alpha", "first command", func(args string) (any, error) { return nil, nil })

	infos := r.ListCommands()
	if len(infos) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(infos))
	}
	if infos[0].Name != "alpha" || infos[0].Description != "first command" {
		t.Fatalf("expected alpha/first command, got %s/%s", infos[0].Name, infos[0].Description)
	}
	if infos[1].Name != "bravo" || infos[1].Description != "second command" {
		t.Fatalf("expected bravo/second command, got %s/%s", infos[1].Name, infos[1].Description)
	}
}

func TestRegistryOverwrite(t *testing.T) {
	r := New()
	r.Register("cmd", "v1 desc", func(args string) (any, error) { return "v1", nil })
	r.Register("cmd", "v2 desc", func(args string) (any, error) { return "v2", nil })

	handler, _ := r.Get("cmd")
	result, _ := handler("")
	if result != "v2" {
		t.Fatalf("expected overwritten handler to return v2, got %v", result)
	}

	infos := r.ListCommands()
	if len(infos) != 1 || infos[0].Description != "v2 desc" {
		t.Fatalf("expected description to be overwritten, got %v", infos)
	}
}

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		input    string
		wantCmd  string
		wantArgs string
		wantErr  bool
	}{
		{"/model gpt-4", "model", "gpt-4", false},
		{"/compact", "compact", "", false},
		{"/set_model provider id", "set_model", "provider id", false},
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

func TestRegistryListCommandsHiddenExcluded(t *testing.T) {
	r := New()
	r.Register("alpha", "first command", func(args string) (any, error) { return nil, nil })
	r.RegisterHidden("bravo", "hidden command", func(args string) (any, error) { return nil, nil })
	r.Register("charlie", "third command", func(args string) (any, error) { return nil, nil })

	// ListCommands should exclude hidden
	infos := r.ListCommands()
	if len(infos) != 2 {
		t.Fatalf("expected 2 visible commands, got %d: %v", len(infos), infos)
	}
	if infos[0].Name != "alpha" || infos[1].Name != "charlie" {
		t.Fatalf("expected [alpha, charlie], got %v", infos)
	}

	// Hidden command should still be callable
	_, ok := r.Get("bravo")
	if !ok {
		t.Fatal("hidden command should still be callable via Get")
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
