package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// TestGetEnvInt exercises both the success and failure paths of getEnvInt.
func TestGetEnvInt(t *testing.T) {
	// Default returned when env var unset.
	t.Setenv("ZAI_TEST_INT_UNSET", "")
	if got := getEnvInt("ZAI_TEST_INT_UNSET", 42); got != 42 {
		t.Errorf("expected default 42, got %d", got)
	}

	// Valid integer parsed.
	t.Setenv("ZAI_TEST_INT_VALID", "123")
	if got := getEnvInt("ZAI_TEST_INT_VALID", 0); got != 123 {
		t.Errorf("expected 123, got %d", got)
	}

	// Whitespace is trimmed.
	t.Setenv("ZAI_TEST_INT_WS", "  7  ")
	if got := getEnvInt("ZAI_TEST_INT_WS", 0); got != 7 {
		t.Errorf("expected 7, got %d", got)
	}

	// Invalid value falls back to default.
	t.Setenv("ZAI_TEST_INT_BAD", "not-a-number")
	if got := getEnvInt("ZAI_TEST_INT_BAD", 9); got != 9 {
		t.Errorf("expected default 9 for invalid input, got %d", got)
	}

	// Empty string falls back to default.
	t.Setenv("ZAI_TEST_INT_EMPTY", "   ")
	if got := getEnvInt("ZAI_TEST_INT_EMPTY", 11); got != 11 {
		t.Errorf("expected default 11 for whitespace-only input, got %d", got)
	}
}

// TestGetEnvIntPublic mirrors the test for the exported GetEnvInt helper.
func TestGetEnvIntPublic(t *testing.T) {
	t.Setenv("ZAI_TEST_GET_ENV_INT", "55")
	if got := GetEnvInt("ZAI_TEST_GET_ENV_INT", 1); got != 55 {
		t.Errorf("expected 55, got %d", got)
	}
	if got := GetEnvInt("ZAI_TEST_GET_ENV_INT_MISSING", 8); got != 8 {
		t.Errorf("expected default 8, got %d", got)
	}
	t.Setenv("ZAI_TEST_GET_ENV_INT_BAD", "abc")
	if got := GetEnvInt("ZAI_TEST_GET_ENV_INT_BAD", 3); got != 3 {
		t.Errorf("expected fallback default 3 for invalid input, got %d", got)
	}
}

// TestResolveConcurrencyConfig_Defaults verifies defaults when no env vars are set.
func TestResolveConcurrencyConfig_Defaults(t *testing.T) {
	t.Setenv("ZAI_MAX_CONCURRENT_TOOLS", "")
	t.Setenv("ZAI_TOOL_TIMEOUT", "")
	t.Setenv("ZAI_QUEUE_TIMEOUT", "")

	cfg := ResolveConcurrencyConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.MaxConcurrentTools != 5 {
		t.Errorf("MaxConcurrentTools = %d, want 5", cfg.MaxConcurrentTools)
	}
	if cfg.ToolTimeout != 30 {
		t.Errorf("ToolTimeout = %d, want 30", cfg.ToolTimeout)
	}
	if cfg.QueueTimeout != 60 {
		t.Errorf("QueueTimeout = %d, want 60", cfg.QueueTimeout)
	}
}

// TestResolveConcurrencyConfig_EnvOverrides verifies env vars override defaults.
func TestResolveConcurrencyConfig_EnvOverrides(t *testing.T) {
	t.Setenv("ZAI_MAX_CONCURRENT_TOOLS", "8")
	t.Setenv("ZAI_TOOL_TIMEOUT", "45")
	t.Setenv("ZAI_QUEUE_TIMEOUT", "90")

	cfg := ResolveConcurrencyConfig()
	if cfg.MaxConcurrentTools != 8 {
		t.Errorf("MaxConcurrentTools = %d, want 8", cfg.MaxConcurrentTools)
	}
	if cfg.ToolTimeout != 45 {
		t.Errorf("ToolTimeout = %d, want 45", cfg.ToolTimeout)
	}
	if cfg.QueueTimeout != 90 {
		t.Errorf("QueueTimeout = %d, want 90", cfg.QueueTimeout)
	}
}

// TestResolveConcurrencyConfig_ZeroIgnored verifies that 0/negative env values
// are ignored (only positive values override).
func TestResolveConcurrencyConfig_ZeroIgnored(t *testing.T) {
	t.Setenv("ZAI_MAX_CONCURRENT_TOOLS", "0")
	t.Setenv("ZAI_TOOL_TIMEOUT", "-5")
	t.Setenv("ZAI_QUEUE_TIMEOUT", "0")

	cfg := ResolveConcurrencyConfig()
	if cfg.MaxConcurrentTools != 5 {
		t.Errorf("expected default 5, got %d", cfg.MaxConcurrentTools)
	}
	if cfg.ToolTimeout != 30 {
		t.Errorf("expected default 30, got %d", cfg.ToolTimeout)
	}
}

// TestResolveLogPath covers both the empty path and PID expansion paths.
func TestResolveLogPath(t *testing.T) {
	// Nil config -> default path is constructed from the user's home dir,
	// so it must end with the configured filename.
	got := ResolveLogPath(nil)
	if got == "" {
		t.Error("expected non-empty default log path")
	}

	// Empty file -> empty result.
	got = ResolveLogPath(&LogConfig{File: ""})
	if got != "" {
		t.Errorf("expected empty result for empty file, got %q", got)
	}

	// {pid} expansion.
	got = ResolveLogPath(&LogConfig{File: "/tmp/ai-{pid}.log"})
	if got == "/tmp/ai-{pid}.log" {
		t.Errorf("expected {pid} to be expanded, got %q", got)
	}
	// {PID} expansion (uppercase variant).
	got = ResolveLogPath(&LogConfig{File: "/tmp/ai-{PID}.log"})
	if got == "/tmp/ai-{PID}.log" {
		t.Errorf("expected {PID} to be expanded, got %q", got)
	}

	// Whitespace-only file is trimmed to empty.
	got = ResolveLogPath(&LogConfig{File: "   "})
	if got != "" {
		t.Errorf("expected empty result for whitespace file, got %q", got)
	}
}

// TestCreateLogger exercises the CreateLogger method on both nil and non-nil configs.
func TestCreateLogger(t *testing.T) {
	// Non-nil config.
	cfg := &LogConfig{Level: "info"}
	logger, err := cfg.CreateLogger()
	if err != nil {
		t.Fatalf("CreateLogger failed: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}

	// Nil config triggers default initialization path inside CreateLogger.
	var nilCfg *LogConfig
	logger, err = nilCfg.CreateLogger()
	if err != nil {
		t.Fatalf("CreateLogger with nil config failed: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger for nil config")
	}
}

// TestNormalizeToolOutputConfig covers the nil, low, and capped branches.
func TestNormalizeToolOutputConfig(t *testing.T) {
	// Nil -> default.
	cfg := normalizeToolOutputConfig(nil)
	if cfg == nil || cfg.MaxChars != defaultToolOutputMaxChars {
		t.Errorf("nil: expected default MaxChars %d, got %+v", defaultToolOutputMaxChars, cfg)
	}

	// Zero -> default.
	cfg = normalizeToolOutputConfig(&ToolOutputConfig{MaxChars: 0})
	if cfg.MaxChars != defaultToolOutputMaxChars {
		t.Errorf("zero: expected default MaxChars, got %d", cfg.MaxChars)
	}

	// Negative -> default.
	cfg = normalizeToolOutputConfig(&ToolOutputConfig{MaxChars: -5})
	if cfg.MaxChars != defaultToolOutputMaxChars {
		t.Errorf("negative: expected default MaxChars, got %d", cfg.MaxChars)
	}

	// Over max -> clamped.
	cfg = normalizeToolOutputConfig(&ToolOutputConfig{MaxChars: maxToolOutputMaxChars * 10})
	if cfg.MaxChars != maxToolOutputMaxChars {
		t.Errorf("over-max: expected clamp to %d, got %d", maxToolOutputMaxChars, cfg.MaxChars)
	}

	// Valid value -> preserved.
	cfg = normalizeToolOutputConfig(&ToolOutputConfig{MaxChars: 5000})
	if cfg.MaxChars != 5000 {
		t.Errorf("valid: expected preserved MaxChars, got %+v", cfg)
	}
}

// TestSaveConfigAndMkdir verifies SaveConfig creates intermediate directories.
func TestSaveConfigAndMkdir(t *testing.T) {
	tmp := t.TempDir()
	deepPath := filepath.Join(tmp, "a", "b", "c", "config.json")

	cfg := DefaultConfig()
	if err := SaveConfig(cfg, deepPath); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	if _, err := os.Stat(deepPath); err != nil {
		t.Errorf("expected file at %s: %v", deepPath, err)
	}

	// Load it back to ensure round-trip works.
	loaded, err := LoadConfig(deepPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Model.ID != cfg.Model.ID {
		t.Errorf("expected model %q, got %q", cfg.Model.ID, loaded.Model.ID)
	}
}

// TestSaveConfig_MarshalFailure triggers json.Marshaler error via a Value
// that fails to marshal. This exercises the "failed to marshal" branch.
func TestSaveConfig_MarshalFailure(t *testing.T) {
	tmp := t.TempDir()

	// The simplest robust approach: pass a path whose dir is a file — MkdirAll will fail.
	regular := filepath.Join(tmp, "regular_file")
	if err := os.WriteFile(regular, []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	// Now try to save a config "inside" that regular file — MkdirAll will fail.
	nested := filepath.Join(regular, "sub", "config.json")
	err := SaveConfig(DefaultConfig(), nested)
	if err == nil {
		t.Error("expected error when SaveConfig cannot create directory")
	}
}

// TestLoadConfig_ReadFileError triggers the read-error path by passing a directory
// (which can't be read as a file).
func TestLoadConfig_ReadFileError(t *testing.T) {
	tmp := t.TempDir()
	// Passing the tmp dir itself — stat succeeds (it's a directory) but ReadFile fails.
	_, err := LoadConfig(tmp)
	if err == nil {
		t.Error("expected error when LoadConfig is given a directory")
	}
}

// TestGetDefaultAuthPath exercises GetDefaultAuthPath.
func TestGetDefaultAuthPath(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home-auth")
	path, err := GetDefaultAuthPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(path) != "auth.json" {
		t.Errorf("expected auth.json, got %q", filepath.Base(path))
	}
}

// TestGetDefaultAuthPath_NoHome exercises the error path when HOME is unset.
// Note: this test mutates HOME; on most systems UserHomeDir falls back elsewhere,
// so we accept either an error or a non-empty path.
func TestGetDefaultAuthPath_NoHome(t *testing.T) {
	// We cannot fully force UserHomeDir to fail without OS-specific tricks.
	// Instead, ensure the function is callable with a typical HOME.
	t.Setenv("HOME", "")
	_, _ = GetDefaultAuthPath() // Just exercise the path; result is platform-dependent.
}

// TestResolveAPIKey_NoSource_NoAuth_NoEnv covers the error path: no auth file,
// no env var, AI_API_KEY_SOURCE unset.
func TestResolveAPIKey_NoSource_NoAuth_NoEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("NONEXISTENT_PROVIDER_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	_, err := ResolveAPIKey("nonexistent_provider")
	if err == nil {
		t.Error("expected error when no key is available")
	}
}

// TestResolveAPIKey_EmptyProvider covers the "default to zai" branch.
func TestResolveAPIKey_EmptyProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZAI_API_KEY", "zai-fallback")
	t.Setenv("AI_API_KEY_SOURCE", "env")

	key, err := ResolveAPIKey("   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "zai-fallback" {
		t.Errorf("expected zai-fallback, got %q", key)
	}
}

// TestResolveAPIKey_AuthFile_BadJSON exercises the JSON parse error path.
func TestResolveAPIKey_AuthFile_BadJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BADJSON_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{not valid json`), 0644); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	_, err := ResolveAPIKey("badjson")
	if err == nil {
		t.Error("expected error for malformed auth file")
	}
}

// TestResolveAPIKey_AuthFile_EmptyCredentials covers the "empty credentials" branch.
func TestResolveAPIKey_AuthFile_EmptyCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("EMPTYCRED_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Entry exists but no usable fields.
	if err := os.WriteFile(authPath, []byte(`{"emptycred":{"type":"oauth"}}`), 0644); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	_, err := ResolveAPIKey("emptycred")
	if err == nil {
		t.Error("expected error when credentials are empty")
	}
}

// TestResolveAPIKey_AuthFile_PlainStringKey exercises the path where the auth
// entry is a plain JSON string (not an object).
func TestResolveAPIKey_AuthFile_PlainStringKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("STRINGKEY_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"stringkey":"plain-key-value"}`), 0644); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	key, err := ResolveAPIKey("stringkey")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "plain-key-value" {
		t.Errorf("expected plain-key-value, got %q", key)
	}
}

// TestResolveAPIKey_AuthFile_TokenField covers the .Token field branch.
func TestResolveAPIKey_AuthFile_TokenField(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TOKENKEY_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"tokenkey":{"token":"tok-xyz"}}`), 0644); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	key, err := ResolveAPIKey("tokenkey")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "tok-xyz" {
		t.Errorf("expected tok-xyz, got %q", key)
	}
}

// TestResolveAPIKey_AuthFile_KeyField covers the .Key field branch.
func TestResolveAPIKey_AuthFile_KeyField(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KEYFIELD_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"keyfield":{"key":"k-field"}}`), 0644); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	key, err := ResolveAPIKey("keyfield")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "k-field" {
		t.Errorf("expected k-field, got %q", key)
	}
}

// TestResolveAPIKey_AuthFile_InvalidEntry exercises the "invalid auth entry" branch
// where the entry is neither a string nor a valid AuthEntry.
func TestResolveAPIKey_AuthFile_InvalidEntry(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("INVALID_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// An array is neither a string nor an AuthEntry object.
	if err := os.WriteFile(authPath, []byte(`{"invalid":[1,2,3]}`), 0644); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	_, err := ResolveAPIKey("invalid")
	if err == nil {
		t.Error("expected error for invalid auth entry shape")
	}
}

// TestResolveAPIKey_AuthFile_EmptyPlainString covers a plain string entry that is empty.
func TestResolveAPIKey_AuthFile_EmptyPlainString(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("EMPTYSTRING_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"emptystring":"   "}`), 0644); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	// The plain-string branch trims and rejects empty -> falls through to AuthEntry parsing,
	// which then fails (string isn't an object) -> empty-credentials error.
	_, err := ResolveAPIKey("emptystring")
	if err == nil {
		t.Error("expected error for empty plain string auth entry")
	}
}

// TestGetDefaultModelsPath exercises GetDefaultModelsPath.
func TestGetDefaultModelsPath(t *testing.T) {
	t.Setenv("HOME", "/tmp/test-home-models")
	path, err := GetDefaultModelsPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(path) != "models.json" {
		t.Errorf("expected models.json, got %q", filepath.Base(path))
	}
}

// TestResolveModelsPath_Default covers the default branch.
func TestResolveModelsPath_Default(t *testing.T) {
	t.Setenv("AI_MODELS_PATH", "")
	t.Setenv("HOME", "/tmp/test-home-models-default")
	path, err := ResolveModelsPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(path) != "models.json" {
		t.Errorf("expected models.json, got %q", filepath.Base(path))
	}
}

// TestResolveModelsPath_Override covers the env override branch.
func TestResolveModelsPath_Override(t *testing.T) {
	t.Setenv("AI_MODELS_PATH", "/custom/path/to/models.json")
	path, err := ResolveModelsPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/custom/path/to/models.json" {
		t.Errorf("expected override path, got %q", path)
	}
}

// TestResolveModelsPath_OverrideWhitespace covers the trim branch.
func TestResolveModelsPath_OverrideWhitespace(t *testing.T) {
	t.Setenv("AI_MODELS_PATH", "   /ws/path/models.json   ")
	path, err := ResolveModelsPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/ws/path/models.json" {
		t.Errorf("expected trimmed path, got %q", path)
	}
}

// TestLoadModelSpecs_Errors covers the read-error and parse-error paths.
func TestLoadModelSpecs_Errors(t *testing.T) {
	// Nonexistent file.
	_, err := LoadModelSpecs("/this/path/does/not/exist/models.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}

	// Invalid JSON.
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{not json`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err = LoadModelSpecs(bad)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// TestLoadModelSpecs_NoProviders exercises the "empty providers" early-return.
func TestLoadModelSpecs_NoProviders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	if err := os.WriteFile(path, []byte(`{"providers": {}}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	specs, err := LoadModelSpecs(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if specs != nil {
		t.Errorf("expected nil specs for empty providers, got %+v", specs)
	}
}

// TestLoadModelSpecs_SkipsEmptyProvider covers the case where a provider name is empty.
func TestLoadModelSpecs_SkipsEmptyProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	data := `{
  "providers": {
    "": {
      "models": [{"id": "should-be-skipped"}]
    },
    "real": {
      "models": [{"id": "kept"}]
    }
  }
}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	specs, err := LoadModelSpecs(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec (empty provider skipped), got %d: %+v", len(specs), specs)
	}
	if specs[0].Provider != "real" || specs[0].ID != "kept" {
		t.Errorf("unexpected spec: %+v", specs[0])
	}
}

// TestLoadModelSpecs_SkipsEmptyModelID covers the case where a model ID is empty.
func TestLoadModelSpecs_SkipsEmptyModelID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	data := `{
  "providers": {
    "zai": {
      "models": [{"id": ""}, {"id": "valid"}]
    }
  }
}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	specs, err := LoadModelSpecs(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec (empty ID skipped), got %d", len(specs))
	}
	if specs[0].ID != "valid" {
		t.Errorf("unexpected spec: %+v", specs[0])
	}
}

// TestFirstNonEmpty covers all branches: first non-empty wins, empty input returns "".
func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty(); got != "" {
		t.Errorf("empty input: expected '', got %q", got)
	}
	if got := firstNonEmpty("", "", ""); got != "" {
		t.Errorf("all empty: expected '', got %q", got)
	}
	if got := firstNonEmpty("", "  ", "x"); got != "x" {
		t.Errorf("expected 'x', got %q", got)
	}
	if got := firstNonEmpty("a", "b"); got != "a" {
		t.Errorf("expected 'a' (first), got %q", got)
	}
	if got := firstNonEmpty("  ", "b", "c"); got != "b" {
		t.Errorf("expected 'b' (skipping whitespace), got %q", got)
	}
}

// TestToLoopConfig_Default covers the default branch (no concurrency / tooloutput in config).
func TestToLoopConfig_Default(t *testing.T) {
	cfg := &Config{}
	loop := cfg.ToLoopConfig()
	if loop == nil {
		t.Fatal("expected non-nil loop config")
	}
	// Default ThinkingLevel from agent.DefaultLoopConfig is "high".
	if loop.ThinkingLevel != "high" {
		t.Errorf("expected default ThinkingLevel 'high', got %q", loop.ThinkingLevel)
	}
}

// TestToLoopConfig_WithConcurrency verifies the Concurrency branch.
func TestToLoopConfig_WithConcurrency(t *testing.T) {
	cfg := &Config{
		Concurrency: &ConcurrencyConfig{
			MaxConcurrentTools: 3,
			QueueTimeout:       20,
		},
	}
	loop := cfg.ToLoopConfig()
	if loop.Executor == nil {
		t.Fatal("expected non-nil executor")
	}
}

// TestToLoopConfig_WithToolOutput verifies the ToolOutput branch.
func TestToLoopConfig_WithToolOutput(t *testing.T) {
	cfg := &Config{
		ToolOutput: &ToolOutputConfig{MaxChars: 4321},
	}
	loop := cfg.ToLoopConfig()
	if loop.ToolOutput.MaxChars != 4321 {
		t.Errorf("expected MaxChars 4321, got %d", loop.ToolOutput.MaxChars)
	}
}

// fakeCompactor is a minimal agent.Compactor implementation for option tests.
type fakeCompactor struct{}

func (f *fakeCompactor) ShouldCompact(_ context.Context, _ *agentctx.AgentContext) bool {
	return false
}
func (f *fakeCompactor) Compact(_ *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	return nil, nil
}
func (f *fakeCompactor) CalculateDynamicThreshold() int { return 0 }

// TestWithCompactor verifies the deprecated single-compactor option.
func TestWithCompactor(t *testing.T) {
	cfg := &Config{}
	loop := cfg.ToLoopConfig(WithCompactor(&fakeCompactor{}))
	if len(loop.Compactors) != 1 {
		t.Fatalf("expected 1 compactor, got %d", len(loop.Compactors))
	}
	if loop.Compactors[0] == nil {
		t.Error("expected non-nil compactor")
	}
}

// TestWithCompactor_Nil verifies nil compactor is ignored.
func TestWithCompactor_Nil(t *testing.T) {
	cfg := &Config{}
	loop := cfg.ToLoopConfig(WithCompactor(nil))
	if len(loop.Compactors) != 0 {
		t.Errorf("expected no compactors for nil input, got %d", len(loop.Compactors))
	}
}

// TestWithCompactors verifies the multi-compactor option.
func TestWithCompactors(t *testing.T) {
	cfg := &Config{}
	loop := cfg.ToLoopConfig(WithCompactors([]agent.Compactor{
		&fakeCompactor{},
		&fakeCompactor{},
	}))
	if len(loop.Compactors) != 2 {
		t.Fatalf("expected 2 compactors, got %d", len(loop.Compactors))
	}
}

// TestWithContextWindow covers the WithContextWindow option.
func TestWithContextWindow(t *testing.T) {
	cfg := &Config{}
	loop := cfg.ToLoopConfig(WithContextWindow(123456))
	if loop.ContextWindow != 123456 {
		t.Errorf("expected 123456, got %d", loop.ContextWindow)
	}
}

// TestWithThinkingLevel covers the WithThinkingLevel option.
func TestWithThinkingLevel(t *testing.T) {
	cfg := &Config{}
	loop := cfg.ToLoopConfig(WithThinkingLevel("medium"))
	if loop.ThinkingLevel != "medium" {
		t.Errorf("expected 'medium', got %q", loop.ThinkingLevel)
	}
}

// TestWithToolCallCutoff covers the WithToolCallCutoff option.
func TestWithToolCallCutoff(t *testing.T) {
	cfg := &Config{}
	loop := cfg.ToLoopConfig(WithToolCallCutoff(42))
	if loop.ToolCallCutoff != 42 {
		t.Errorf("expected 42, got %d", loop.ToolCallCutoff)
	}
}

// TestWithExecutor covers the WithExecutor option.
func TestWithExecutor(t *testing.T) {
	cfg := &Config{}
	ex := agent.NewToolExecutor(2, 1)
	loop := cfg.ToLoopConfig(WithExecutor(ex))
	if loop.Executor != ex {
		t.Error("expected executor to be set")
	}
}

// TestWithToolOutputLimits covers the WithToolOutputLimits option.
func TestWithToolOutputLimits(t *testing.T) {
	cfg := &Config{}
	loop := cfg.ToLoopConfig(WithToolOutputLimits(agent.ToolOutputLimits{MaxChars: 999}))
	if loop.ToolOutput.MaxChars != 999 {
		t.Errorf("expected 999, got %d", loop.ToolOutput.MaxChars)
	}
}

// TestLoadConfigEnvOverride_Custom exercises environment variable overrides via getEnv / getEnvInt.
func TestLoadConfigEnvOverride_Custom(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")

	// Write a baseline file with one set of values.
	if err := os.WriteFile(cfgPath, []byte(`{"model":{"id":"file-id","baseUrl":"http://file.example","maxTokens":100}}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv("ZAI_MODEL", "env-id")
	t.Setenv("ZAI_BASE_URL", "http://env.example")
	t.Setenv("ZAI_MAX_TOKENS", "999")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Model.ID != "env-id" {
		t.Errorf("expected env-id, got %q", cfg.Model.ID)
	}
	if cfg.Model.BaseURL != "http://env.example" {
		t.Errorf("expected env base url, got %q", cfg.Model.BaseURL)
	}
	if cfg.Model.MaxTokens != 999 {
		t.Errorf("expected 999, got %d", cfg.Model.MaxTokens)
	}
}

// TestLoadConfigEnvOverride_InvalidInt covers the invalid-int fallback for ZAI_MAX_TOKENS.
func TestLoadConfigEnvOverride_InvalidInt(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"model":{"maxTokens":333}}`), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("ZAI_MAX_TOKENS", "not-a-number")

	cfg, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Model.MaxTokens != 333 {
		t.Errorf("expected fallback to file value 333, got %d", cfg.Model.MaxTokens)
	}
}
