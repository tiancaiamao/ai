package middlewares

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// ---------------------------------------------------------------------------
// Helper: build a tool-result AgentMessage with text content
// ---------------------------------------------------------------------------
func makeToolResult(toolName, text string) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role:     "toolResult",
		ToolName: toolName,
		Content:  []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: text}},
	}
}

func makeHookContext() agent.HookContext {
	return agent.HookContext{
		Ctx: context.Background(),
	}
}

// ===========================================================================
// AC1: Registry supports Register(name, factory) and Lookup(name) → factory
// ===========================================================================

func TestRegisterAndLookup(t *testing.T) {
	// We can't easily reset the global registry (it has a sync.Mutex and init
	// already registers "destructive_guard"). So test the existing registration
	// works and we can register new ones with unique names.

	name := "test_register_lookup_hook"
	called := false
	spec := MiddlewareSpec{
		Name: name,
		AfterTool: func(params map[string]any) (agent.AfterToolHook, error) {
			called = true
			return func(hctx agent.HookContext, toolName string, result agentctx.AgentMessage) (agentctx.AgentMessage, error) {
				return result, nil
			}, nil
		},
	}

	// Register should succeed (panic if dup, so use a unique name)
	Register(spec)

	// Lookup must return the spec
	found := Lookup(name)
	if found == nil {
		t.Fatalf("Lookup(%q) returned nil, expected non-nil", name)
	}
	if found.Name != name {
		t.Fatalf("Lookup returned wrong spec: got %q, want %q", found.Name, name)
	}

	// Factory should be callable
	if found.AfterTool == nil {
		t.Fatal("AfterTool factory is nil")
	}
	h, err := found.AfterTool(nil)
	if err != nil {
		t.Fatalf("AfterTool factory returned error: %v", err)
	}
	if !called {
		t.Fatal("AfterTool factory was not called")
	}
	if h == nil {
		t.Fatal("AfterTool factory returned nil hook")
	}

	// Cleanup: remove from registry for test isolation
	registryMu.Lock()
	delete(registry, name)
	registryMu.Unlock()
}

// ===========================================================================
// AC2: Supports 3 Factory types: BeforeModelFactory, AfterToolFactory,
//      AfterAgentFactory
// ===========================================================================

func TestThreeFactoryTypes(t *testing.T) {
	name := "test_three_factories"

	spec := MiddlewareSpec{
		Name: name,
		BeforeModel: func(params map[string]any) (agent.BeforeModelHook, error) {
			return func(hctx agent.HookContext, msgs []agentctx.AgentMessage) ([]agentctx.AgentMessage, error) {
				return nil, nil
			}, nil
		},
		AfterTool: func(params map[string]any) (agent.AfterToolHook, error) {
			return func(hctx agent.HookContext, toolName string, result agentctx.AgentMessage) (agentctx.AgentMessage, error) {
				return result, nil
			}, nil
		},
		AfterAgent: func(params map[string]any) (agent.AfterAgentHook, error) {
			return func(hctx agent.HookContext) {}, nil
		},
	}

	Register(spec)
	defer func() {
		registryMu.Lock()
		delete(registry, name)
		registryMu.Unlock()
	}()

	found := Lookup(name)
	if found == nil {
		t.Fatal("Lookup returned nil")
	}
	if found.BeforeModel == nil {
		t.Error("BeforeModel factory is nil")
	}
	if found.AfterTool == nil {
		t.Error("AfterTool factory is nil")
	}
	if found.AfterAgent == nil {
		t.Error("AfterAgent factory is nil")
	}
}

// ===========================================================================
// AC3: Lookup non-existent name returns nil
// ===========================================================================

func TestLookupNonExistent(t *testing.T) {
	result := Lookup("absolutely_does_not_exist_middleware_xyz_12345")
	if result != nil {
		t.Fatalf("Lookup of non-existent name should return nil, got %+v", result)
	}
}

// ===========================================================================
// AC4: DestructiveCommandGuard detects destructive commands in bash tool
// ===========================================================================

func TestDestructiveGuardDetectsRmRf(t *testing.T) {
	guard, err := newDestructiveGuard(nil)
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	result := makeToolResult("bash", "executing: rm -rf /tmp/test")
	modified, err := guard.afterTool(makeHookContext(), "bash", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	text := modified.ExtractText()
	if !strings.Contains(text, "WARNING") && !strings.Contains(text, "Destructive") {
		t.Errorf("expected warning in output, got: %q", text)
	}
}

func TestDestructiveGuardDetectsKill9(t *testing.T) {
	guard, err := newDestructiveGuard(nil)
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	result := makeToolResult("bash", "running: kill -9 1234")
	modified, err := guard.afterTool(makeHookContext(), "bash", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	text := modified.ExtractText()
	if !strings.Contains(text, "WARNING") {
		t.Errorf("expected warning for kill -9, got: %q", text)
	}
}

// ===========================================================================
// AC5: bash with destructive command: warning appended, original output preserved
// ===========================================================================

func TestDestructiveGuardOriginalPreserved(t *testing.T) {
	guard, err := newDestructiveGuard(nil)
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	originalText := "file1.txt\nfile2.txt\nremoved everything with rm -rf /home"
	result := makeToolResult("bash", originalText)
	modified, err := guard.afterTool(makeHookContext(), "bash", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	text := modified.ExtractText()

	// Original text must be preserved
	if !strings.Contains(text, originalText) {
		t.Errorf("original text not preserved.\nGot: %q\nWant substring: %q", text, originalText)
	}

	// Warning must be appended
	if !strings.Contains(text, "WARNING") || !strings.Contains(text, "Destructive") {
		t.Errorf("warning text not appended, got: %q", text)
	}

	// Warning must come AFTER the original text
	origIdx := strings.Index(text, originalText)
	warnIdx := strings.Index(text, "WARNING")
	if origIdx >= warnIdx {
		t.Errorf("warning should come after original text: origIdx=%d, warnIdx=%d", origIdx, warnIdx)
	}
}

func TestDestructiveGuardContentBlocksPreserved(t *testing.T) {
	guard, err := newDestructiveGuard(nil)
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	result := makeToolResult("bash", "rm -rf /something")
	modified, err := guard.afterTool(makeHookContext(), "bash", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	// First content block should be the original text
	if len(modified.Content) < 2 {
		t.Fatalf("expected at least 2 content blocks, got %d", len(modified.Content))
	}

	orig, ok := modified.Content[0].(agentctx.TextContent)
	if !ok {
		t.Fatal("first content block is not TextContent")
	}
	if !strings.Contains(orig.Text, "rm -rf /something") {
		t.Errorf("original content block text wrong: %q", orig.Text)
	}

	warn, ok := modified.Content[len(modified.Content)-1].(agentctx.TextContent)
	if !ok {
		t.Fatal("last content block is not TextContent")
	}
	if !strings.Contains(warn.Text, "WARNING") {
		t.Errorf("last content block should be warning, got: %q", warn.Text)
	}
}

// ===========================================================================
// AC6: Non-bash tool: returns original result unmodified
// ===========================================================================

func TestDestructiveGuardPassthrough(t *testing.T) {
	guard, err := newDestructiveGuard(nil)
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	// Use a non-bash tool with destructive-looking text
	result := makeToolResult("read", "rm -rf /something dangerous")
	modified, err := guard.afterTool(makeHookContext(), "read", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	text := modified.ExtractText()
	if strings.Contains(text, "WARNING") {
		t.Errorf("non-bash tool should not trigger warning, got: %q", text)
	}

	// Must be exactly the same content
	if len(modified.Content) != len(result.Content) {
		t.Errorf("content blocks changed: orig=%d, modified=%d", len(result.Content), len(modified.Content))
	}
}

func TestDestructiveGuardPassthroughWrite(t *testing.T) {
	guard, err := newDestructiveGuard(nil)
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	result := makeToolResult("write", "rm -rf / everything")
	modified, err := guard.afterTool(makeHookContext(), "write", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	text := modified.ExtractText()
	if strings.Contains(text, "WARNING") {
		t.Errorf("write tool should not trigger warning, got: %q", text)
	}
}

// ===========================================================================
// AC7: Custom protected_patterns
// ===========================================================================

func TestCustomProtectedPatterns(t *testing.T) {
	customPatterns := []string{`dangerzone_\d+`}
	guard, err := newDestructiveGuard(customPatterns)
	if err != nil {
		t.Fatalf("newDestructiveGuard with custom patterns: %v", err)
	}

	// Should match custom pattern
	result := makeToolResult("bash", "output: dangerzone_42 was here")
	modified, err := guard.afterTool(makeHookContext(), "bash", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	text := modified.ExtractText()
	if !strings.Contains(text, "WARNING") {
		t.Errorf("custom pattern should trigger warning, got: %q", text)
	}

	// Should NOT match default pattern (rm -rf) when custom patterns override
	result2 := makeToolResult("bash", "rm -rf /tmp/test")
	modified2, err := guard.afterTool(makeHookContext(), "bash", result2)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	text2 := modified2.ExtractText()
	if strings.Contains(text2, "WARNING") {
		t.Errorf("default patterns should not apply when custom patterns provided, got: %q", text2)
	}
}

func TestCustomProtectedPatternsViaParams(t *testing.T) {
	// Test the factory function path (newDestructiveGuardFromParams)
	params := map[string]any{
		"protected_patterns": []string{`custom_delete_\w+`},
	}

	hook, err := newDestructiveGuardFromParams(params)
	if err != nil {
		t.Fatalf("newDestructiveGuardFromParams: %v", err)
	}

	result := makeToolResult("bash", "custom_delete_all records")
	modified, err := hook(makeHookContext(), "bash", result)
	if err != nil {
		t.Fatalf("hook error: %v", err)
	}

	text := modified.ExtractText()
	if !strings.Contains(text, "WARNING") {
		t.Errorf("custom pattern via params should trigger warning, got: %q", text)
	}
}

func TestCustomProtectedPatternsEmptyUsesDefault(t *testing.T) {
	// Empty patterns slice should fall back to defaults
	guard, err := newDestructiveGuard([]string{})
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	// Verify defaults are used by checking rm -rf is detected
	result := makeToolResult("bash", "rm -rf /tmp")
	modified, err := guard.afterTool(makeHookContext(), "bash", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	text := modified.ExtractText()
	if !strings.Contains(text, "WARNING") {
		t.Errorf("empty patterns should use defaults (rm -rf should be caught), got: %q", text)
	}
}

// ===========================================================================
// Additional edge case tests
// ===========================================================================

func TestBashWithNoDestructiveCommand(t *testing.T) {
	guard, err := newDestructiveGuard(nil)
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	result := makeToolResult("bash", "ls -la /tmp")
	modified, err := guard.afterTool(makeHookContext(), "bash", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	text := modified.ExtractText()
	if strings.Contains(text, "WARNING") {
		t.Errorf("benign bash command should not trigger warning, got: %q", text)
	}
}

func TestConcurrentLookup(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found := Lookup("destructive_guard")
			if found == nil {
				t.Error("Lookup(destructive_guard) returned nil")
			}
		}()
	}
	wg.Wait()
}

func TestBashEmptyOutput(t *testing.T) {
	guard, err := newDestructiveGuard(nil)
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	result := agentctx.AgentMessage{
		Role:     "toolResult",
		ToolName: "bash",
		Content:  []agentctx.ContentBlock{},
	}
	modified, err := guard.afterTool(makeHookContext(), "bash", result)
	if err != nil {
		t.Fatalf("afterTool error: %v", err)
	}

	if len(modified.Content) != 0 {
		t.Errorf("empty bash output should not trigger warning, got content: %v", modified.Content)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	name := "test_duplicate_panic"
	spec := MiddlewareSpec{
		Name: name,
		AfterTool: func(params map[string]any) (agent.AfterToolHook, error) {
			return func(hctx agent.HookContext, toolName string, result agentctx.AgentMessage) (agentctx.AgentMessage, error) {
				return result, nil
			}, nil
		},
	}

	Register(spec)
	defer func() {
		registryMu.Lock()
		delete(registry, name)
		registryMu.Unlock()
	}()

	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic on duplicate Register, got none")
		}
		if !strings.Contains(fmt.Sprintf("%v", r), "already registered") {
			t.Errorf("panic message unexpected: %v", r)
		}
	}()
	Register(spec)
}

func TestDestructiveGuardRegisteredInInit(t *testing.T) {
	found := Lookup("destructive_guard")
	if found == nil {
		t.Fatal("destructive_guard should be auto-registered via init()")
	}
	if found.AfterTool == nil {
		t.Error("destructive_guard AfterTool factory is nil")
	}
}

func TestDefaultPatternsCompile(t *testing.T) {
	// All default patterns should be valid regexes
	for _, p := range defaultProtectedPatterns {
		_, err := regexp.Compile(p)
		if err != nil {
			t.Errorf("default pattern %q failed to compile: %v", p, err)
		}
	}
}

// Test all default patterns match their intended commands
func TestDefaultPatternsCoverage(t *testing.T) {
	cases := []struct {
		input   string
		matches bool
	}{
		{"rm -rf /", true},
		{"rm -fr /home", true},
		{"rm -r /something ", true},
		{"kill -9 1234", true},
		{"mkfs.ext4 /dev/sda1", true},
		{"dd if=/dev/zero of=/dev/sda", true},
		{"ls -la /tmp", false},
		{"echo hello", false},
		{"rm file.txt", false}, // no -r or -rf
	}

	guard, err := newDestructiveGuard(nil)
	if err != nil {
		t.Fatalf("newDestructiveGuard: %v", err)
	}

	for _, tc := range cases {
		got := guard.matches(tc.input)
		if got != tc.matches {
			t.Errorf("matches(%q) = %v, want %v", tc.input, got, tc.matches)
		}
	}
}

func TestBuildAfterToolHooks(t *testing.T) {
	// Register a temporary middleware for the test.
	const name = "test-after-tool"
	Register(MiddlewareSpec{
		Name: name,
		AfterTool: func(params map[string]any) (agent.AfterToolHook, error) {
			if v, _ := params["fail"].(bool); v {
				return nil, fmt.Errorf("injected failure at construction")
			}
			return func(_ agent.HookContext, _ string, _ agentctx.AgentMessage) (agentctx.AgentMessage, error) {
				return agentctx.AgentMessage{}, nil
			}, nil
		},
	})
	defer func() {
		registryMu.Lock()
		delete(registry, name)
		registryMu.Unlock()
	}()

	// Success + error mix
	hooks, err := buildAfterToolHooks([]MiddlewareEntry{
		{Name: "unknown"},                                  // skipped
		{Name: name, Params: map[string]any{}},             // ok
		{Name: name, Params: map[string]any{"fail": true}}, // factory error
	})
	if err == nil {
		t.Errorf("expected error from fail=true, got hooks=%v", hooks)
	}

	// Pure success path
	hooks, err = buildAfterToolHooks([]MiddlewareEntry{
		{Name: name, Params: map[string]any{}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}

	// Empty entries -> nil/empty
	hooks, err = buildAfterToolHooks(nil)
	if err != nil {
		t.Fatalf("unexpected error on nil entries: %v", err)
	}
	if hooks != nil {
		t.Errorf("expected nil hooks for nil entries, got %v", hooks)
	}
}

func TestBuildBeforeModelHooks(t *testing.T) {
	const name = "test-before-model"
	Register(MiddlewareSpec{
		Name: name,
		BeforeModel: func(params map[string]any) (agent.BeforeModelHook, error) {
			if v, _ := params["fail"].(bool); v {
				return nil, fmt.Errorf("injected failure")
			}
			return func(_ agent.HookContext, _ []agentctx.AgentMessage) ([]agentctx.AgentMessage, error) {
				return nil, nil
			}, nil
		},
	})
	defer func() {
		registryMu.Lock()
		delete(registry, name)
		registryMu.Unlock()
	}()

	hooks, err := buildBeforeModelHooks([]MiddlewareEntry{{Name: name, Params: map[string]any{}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}

	_, err = buildBeforeModelHooks([]MiddlewareEntry{{Name: name, Params: map[string]any{"fail": true}}})
	if err == nil {
		t.Error("expected error from fail=true, got nil")
	}

	// Unknown middleware -> empty
	hooks, err = buildBeforeModelHooks([]MiddlewareEntry{{Name: "does-not-exist"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooks) != 0 {
		t.Errorf("expected 0 hooks for unknown, got %d", len(hooks))
	}
}

func TestBuildAfterAgentHooks(t *testing.T) {
	const name = "test-after-agent"
	Register(MiddlewareSpec{
		Name: name,
		AfterAgent: func(params map[string]any) (agent.AfterAgentHook, error) {
			if v, _ := params["fail"].(bool); v {
				return nil, fmt.Errorf("injected failure")
			}
			return func(_ agent.HookContext) {}, nil
		},
	})
	defer func() {
		registryMu.Lock()
		delete(registry, name)
		registryMu.Unlock()
	}()

	hooks, err := buildAfterAgentHooks([]MiddlewareEntry{{Name: name, Params: map[string]any{}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(hooks) != 1 {
		t.Fatalf("expected 1 hook, got %d", len(hooks))
	}

	_, err = buildAfterAgentHooks([]MiddlewareEntry{{Name: name, Params: map[string]any{"fail": true}}})
	if err == nil {
		t.Error("expected error, got nil")
	}
}

func TestBuildHooks(t *testing.T) {
	// BuildHooks combines all three. Register private middlewares so the
	// test does not depend on execution order of other tests.
	const (
		toolName  = "test-buildhooks-tool"
		modelName = "test-buildhooks-model"
		agentName = "test-buildhooks-agent"
	)
	Register(MiddlewareSpec{
		Name: toolName,
		AfterTool: func(params map[string]any) (agent.AfterToolHook, error) {
			return func(_ agent.HookContext, _ string, _ agentctx.AgentMessage) (agentctx.AgentMessage, error) {
				return agentctx.AgentMessage{}, nil
			}, nil
		},
	})
	Register(MiddlewareSpec{
		Name: modelName,
		BeforeModel: func(params map[string]any) (agent.BeforeModelHook, error) {
			return func(_ agent.HookContext, _ []agentctx.AgentMessage) ([]agentctx.AgentMessage, error) {
				return nil, nil
			}, nil
		},
	})
	Register(MiddlewareSpec{
		Name: agentName,
		AfterAgent: func(params map[string]any) (agent.AfterAgentHook, error) {
			return func(_ agent.HookContext) {}, nil
		},
	})
	defer func() {
		registryMu.Lock()
		delete(registry, toolName)
		delete(registry, modelName)
		delete(registry, agentName)
		registryMu.Unlock()
	}()

	entries := []MiddlewareEntry{
		{Name: toolName, Params: map[string]any{}},
		{Name: modelName, Params: map[string]any{}},
		{Name: agentName, Params: map[string]any{}},
	}
	reg, err := BuildHooks(entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	if len(reg.BeforeModelHooks) != 1 || len(reg.AfterToolHooks) != 1 || len(reg.AfterAgentHooks) != 1 {
		t.Errorf("unexpected counts: bm=%d at=%d aa=%d",
			len(reg.BeforeModelHooks), len(reg.AfterToolHooks), len(reg.AfterAgentHooks))
	}

	// Empty entries -> empty registry
	reg, err = BuildHooks(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registry even for empty entries")
	}
}

func TestEnsureContentSlice(t *testing.T) {
	msg := &agentctx.AgentMessage{}
	ensureContentSlice(msg)
	if msg.Content == nil {
		t.Error("expected non-nil Content after ensureContentSlice")
	}

	// Already-set content should not be replaced.
	pre := []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "pre"}}
	msg2 := &agentctx.AgentMessage{Content: pre}
	ensureContentSlice(msg2)
	if len(msg2.Content) != 1 {
		t.Errorf("expected preserved 1-element content, got %d", len(msg2.Content))
	}
}

func TestExtractStringSlice(t *testing.T) {
	// Key missing
	if got := extractStringSlice(map[string]any{}, "k"); got != nil {
		t.Errorf("expected nil for missing key, got %v", got)
	}
	// []string passthrough
	if got := extractStringSlice(map[string]any{"k": []string{"a", "b"}}, "k"); len(got) != 2 || got[0] != "a" {
		t.Errorf("[]string passthrough wrong: %v", got)
	}
	// []any with mixed types (strings filtered through)
	if got := extractStringSlice(map[string]any{"k": []any{"x", 42, "y"}}, "k"); len(got) != 2 || got[0] != "x" || got[1] != "y" {
		t.Errorf("[]any filter wrong: %v", got)
	}
	// Other type -> nil
	if got := extractStringSlice(map[string]any{"k": 123}, "k"); got != nil {
		t.Errorf("expected nil for int value, got %v", got)
	}
}
