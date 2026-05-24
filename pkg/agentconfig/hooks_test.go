package agentconfig

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/middlewares"
)

// --- Criterion 6: middleware name 不在注册表时 BuildHooks 静默跳过 ---

func TestUnknownMiddleware(t *testing.T) {
	cfg := &AgentConfig{
		Version: 1,
		Middlewares: []MiddlewareEntry{
			{Name: "totally_unknown_middleware_xyz", Enabled: true},
		},
	}
	// Should not panic and should return nil (no hooks to register).
	result := cfg.BuildHooks()
	if result != nil {
		t.Fatalf("expected nil HookRegistry for unknown middleware, got %+v", result)
	}
}

// --- Criterion 7: enabled=false 的 middleware 不注册 ---

func TestDisabledMiddleware(t *testing.T) {
	cfg := &AgentConfig{
		Version: 1,
		Middlewares: []MiddlewareEntry{
			{Name: "destructive_guard", Enabled: false},
		},
	}
	// disabled middleware should be skipped, resulting in nil.
	result := cfg.BuildHooks()
	if result != nil {
		t.Fatalf("expected nil HookRegistry for disabled middleware, got %+v", result)
	}
}

// --- Enabled known middleware should produce a non-nil registry ---

func TestEnabledKnownMiddleware(t *testing.T) {
	// destructive_guard is auto-registered via init() in destructive_guard.go
	cfg := &AgentConfig{
		Version: 1,
		Middlewares: []MiddlewareEntry{
			{Name: "destructive_guard", Enabled: true},
		},
	}
	result := cfg.BuildHooks()
	if result == nil {
		t.Fatal("expected non-nil HookRegistry for enabled known middleware")
	}
	// Should have at least one AfterTool hook (destructive_guard implements AfterToolHook)
	if len(result.AfterToolHooks) == 0 {
		t.Fatal("expected at least one AfterToolHook for destructive_guard")
	}
}

// --- Mixed: one unknown, one disabled, one enabled known ---

func TestMixedMiddlewares(t *testing.T) {
	cfg := &AgentConfig{
		Version: 1,
		Middlewares: []MiddlewareEntry{
			{Name: "unknown_middleware_abc", Enabled: true},
			{Name: "destructive_guard", Enabled: false},
			{Name: "destructive_guard", Enabled: true},
		},
	}
	result := cfg.BuildHooks()
	if result == nil {
		t.Fatal("expected non-nil HookRegistry because destructive_guard is enabled")
	}
	if len(result.AfterToolHooks) != 1 {
		t.Fatalf("expected exactly 1 AfterToolHook, got %d", len(result.AfterToolHooks))
	}
}

// --- Verify no panics with empty middleware list ---

func TestEmptyMiddlewares(t *testing.T) {
	cfg := &AgentConfig{
		Version:     1,
		Middlewares: []MiddlewareEntry{},
	}
	result := cfg.BuildHooks()
	if result != nil {
		t.Fatalf("expected nil for empty middleware list, got %+v", result)
	}
}

// --- Criterion 8: verify middlewares package is imported only via agentconfig ---

func TestMiddlewarePackageIndirectImport(t *testing.T) {
	// This test verifies that the agentconfig package correctly uses the middlewares package
	// BuildHooks calls middlewares.Lookup and middlewares.BuildHooks
	// If middlewares is not imported, this would fail at compile time
	_ = middlewares.Lookup("destructive_guard")
	// The destructive_guard should be registered via init()
	spec := middlewares.Lookup("destructive_guard")
	if spec == nil {
		t.Fatal("destructive_guard should be registered in the middleware registry")
	}
}
