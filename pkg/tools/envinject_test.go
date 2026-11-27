package tools

import (
	"context"
	"os"
	"path/filepath"
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

// fakeSudoInjector returns an injector whose PATH puts a fake sudo first, so
// the sudo rewrite can be tested without touching the host's sudo.
func fakeSudoInjector(t *testing.T) *EnvInjector {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"printf 'args:%s\\n' \"$*\"\n" +
		"printf 'envpwd:%s\\n' \"$SUDO_PASSWORD\"\n" +
		"if [ \"$1\" = \"-S\" ]; then IFS= read -r pwd; printf 'pwd:%s\\n' \"$pwd\"; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sudo: %v", err)
	}
	inj := NewEnvInjector()
	inj.Set("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return inj
}

func bashOutputText(t *testing.T, inj *EnvInjector, command string) string {
	t.Helper()
	ws, err := NewWorkspace("/tmp")
	if err != nil {
		t.Fatalf("new workspace: %v", err)
	}
	blocks, err := NewBashToolWithEnv(ws, inj).Execute(context.Background(), map[string]any{
		"command": command,
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
	return text.Text
}

func TestBashToolSudoUsesInjectedPassword(t *testing.T) {
	const name = "SUDO_PASSWORD"
	t.Cleanup(func() { _ = os.Unsetenv(name) })

	inj := fakeSudoInjector(t)
	inj.Set(name, "bbb")

	out := bashOutputText(t, inj, "sudo demo")
	for _, want := range []string{"-S -p", "envpwd:bbb", "pwd:bbb"} {
		if !strings.Contains(out, want) {
			t.Errorf("bash output = %q, want it to contain %q", out, want)
		}
	}
	if _, ok := os.LookupEnv(name); ok {
		t.Errorf("%s leaked into the agent process environment", name)
	}
}

func TestBashToolSudoUnsetHidesInheritedPassword(t *testing.T) {
	const name = "SUDO_PASSWORD"
	t.Cleanup(func() { _ = os.Unsetenv(name) })
	if err := os.Setenv(name, "inherited"); err != nil {
		t.Fatalf("set process env: %v", err)
	}

	inj := fakeSudoInjector(t)
	inj.Unset(name)

	out := bashOutputText(t, inj, "sudo demo")
	if !strings.Contains(out, "args:-n demo") {
		t.Errorf("bash output = %q, want -n rewrite", out)
	}
	for _, unwanted := range []string{"-S", "\npwd:", "envpwd:inherited"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("bash output = %q, should not contain %q", out, unwanted)
		}
	}
	if got := os.Getenv(name); got != "inherited" {
		t.Errorf("agent process %s = %q, want inherited", name, got)
	}
}
