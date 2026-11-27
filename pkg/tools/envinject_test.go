package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestEnvInjectorDoesNotMutateProcessEnv(t *testing.T) {
	const name = "AI_TEST_INJECTOR_SET"
	t.Cleanup(func() { _ = os.Unsetenv(name) })

	inj := NewEnvInjector()
	inj.Set(name, "secret")

	if got, ok := inj.Lookup(name); !ok || got != "secret" {
		t.Fatalf("Lookup = %q, %v, want secret, true", got, ok)
	}
	if _, ok := os.LookupEnv(name); ok {
		t.Fatalf("%s leaked into the agent process environment", name)
	}
}

func TestEnvInjectorUnsetHidesInheritedValue(t *testing.T) {
	const name = "AI_TEST_INJECTOR_INHERITED"
	t.Cleanup(func() { _ = os.Unsetenv(name) })
	if err := os.Setenv(name, "from-process"); err != nil {
		t.Fatalf("set process env: %v", err)
	}

	inj := NewEnvInjector()
	inj.Unset(name)

	if _, ok := inj.Lookup(name); ok {
		t.Errorf("%s still visible after Unset", name)
	}
	if got := os.Getenv(name); got != "from-process" {
		t.Errorf("agent process %s = %q, want from-process", name, got)
	}
	for _, entry := range inj.Environ() {
		if strings.HasPrefix(entry, name+"=") {
			t.Errorf("Environ still contains %s: %q", name, entry)
		}
	}
}

func TestEnvInjectorSetClearsDeletion(t *testing.T) {
	const name = "AI_TEST_INJECTOR_TOGGLE"
	t.Cleanup(func() { _ = os.Unsetenv(name) })

	inj := NewEnvInjector()
	inj.Unset(name)
	inj.Set(name, "new-value")

	if got, ok := inj.Lookup(name); !ok || got != "new-value" {
		t.Fatalf("Lookup = %q, %v, want new-value, true", got, ok)
	}
}

func TestBashToolUsesInjectedEnvironment(t *testing.T) {
	const name = "AI_TEST_BASH_INJECTED"
	t.Cleanup(func() { _ = os.Unsetenv(name) })

	inj := NewEnvInjector()
	inj.Set(name, "hello world")
	ws, err := NewWorkspace("/tmp")
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	tool := NewBashToolWithEnv(ws, inj)

	blocks, err := tool.Execute(context.Background(), map[string]any{
		"command": `printf '%s' "$AI_TEST_BASH_INJECTED"`,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("no output blocks")
	}
	text, ok := blocks[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("unexpected block type %T", blocks[0])
	}
	if !strings.Contains(text.Text, "hello world") {
		t.Errorf("bash output = %q, want it to contain hello world", text.Text)
	}
	if _, ok := os.LookupEnv(name); ok {
		t.Errorf("%s leaked into the agent process environment", name)
	}
}
