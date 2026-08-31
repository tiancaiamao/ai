package config

import (
	"os"
	"path/filepath"
	"testing"
)

const testModelsJSON = `{
  "providers": {
    "zai": {
      "baseUrl": "https://api.z.ai/v1",
      "api": "openai",
      "models": [
        {"id": "glm-4.6", "name": "GLM 4.6", "contextWindow": 200000, "maxTokens": 128000},
        {"id": "glm-4.5-air", "reasoning": true, "input": ["text", "image"]}
      ]
    },
    "deepseek": {
      "models": [
        {"id": "deepseek-chat", "baseUrl": "https://api.deepseek.com", "api": "openai"}
      ]
    }
  }
}`

// writeModelsFile writes a models.json into a temp HOME and sets AI_MODELS_PATH.
func writeModelsFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write models file: %v", err)
	}
	t.Setenv("AI_MODELS_PATH", path)
	return path
}

func TestResolveModelsPath(t *testing.T) {
	// AI_MODELS_PATH override honored.
	t.Setenv("AI_MODELS_PATH", "/custom/path.json")
	got, err := ResolveModelsPath()
	if err != nil || got != "/custom/path.json" {
		t.Errorf("ResolveModelsPath override = %q, %v", got, err)
	}

	// Falls back to HOME/.ai/models.json.
	t.Setenv("AI_MODELS_PATH", "")
	t.Setenv("AI_MODELS_PATH", "  ") // whitespace treated as unset
	t.Setenv("AI_MODELS_PATH", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err = ResolveModelsPath()
	if err != nil {
		t.Fatalf("ResolveModelsPath error: %v", err)
	}
	if want := filepath.Join(home, ".ai", "models.json"); got != want {
		t.Errorf("ResolveModelsPath default = %q, want %q", got, want)
	}
}

func TestGetDefaultModelsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := GetDefaultModelsPath()
	if err != nil {
		t.Fatalf("GetDefaultModelsPath error: %v", err)
	}
	if want := filepath.Join(home, ".ai", "models.json"); got != want {
		t.Errorf("GetDefaultModelsPath = %q, want %q", got, want)
	}
}

func TestLoadModelSpecsFromConfig(t *testing.T) {
	// Success: specs loaded and sorted.
	path := writeModelsFile(t, testModelsJSON)
	specs, gotPath, err := LoadModelSpecsFromConfig(&Config{})
	if err != nil {
		t.Fatalf("LoadModelSpecsFromConfig error: %v", err)
	}
	if gotPath != path {
		t.Errorf("path = %q, want %q", gotPath, path)
	}
	if len(specs) != 3 {
		t.Fatalf("specs len = %d, want 3", len(specs))
	}

	// Missing file → fallback spec from config.
	writeModelsFile(t, "")
	t.Setenv("AI_MODELS_PATH", filepath.Join(t.TempDir(), "missing.json"))
	cfg := &Config{Model: ModelConfig{ID: "cfg-model", Provider: "p"}}
	specs, _, err = LoadModelSpecsFromConfig(cfg)
	if err != nil {
		t.Fatalf("missing file should fall back to config spec: %v", err)
	}
	if len(specs) != 1 || specs[0].ID != "cfg-model" {
		t.Errorf("fallback specs = %+v", specs)
	}

	// Empty models file (no providers) → error "no models defined".
	writeModelsFile(t, `{"providers": {}}`)
	_, _, err = LoadModelSpecsFromConfig(&Config{})
	if err == nil {
		t.Error("expected error for empty models file")
	}
}

func TestResolveActiveModelSpec(t *testing.T) {
	writeModelsFile(t, testModelsJSON)

	// Matching spec found in models.json.
	cfg := &Config{Model: ModelConfig{ID: "glm-4.6", Provider: "zai"}}
	spec, err := ResolveActiveModelSpec(cfg)
	if err != nil {
		t.Fatalf("ResolveActiveModelSpec error: %v", err)
	}
	if spec.ID != "glm-4.6" || spec.Provider != "zai" {
		t.Errorf("spec = %+v, want zai/glm-4.6", spec)
	}
	if spec.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", spec.ContextWindow)
	}

	// No matching spec → falls back to ModelSpecFromConfig.
	cfg = &Config{Model: ModelConfig{ID: "unknown-model", Provider: "zai", API: "openai"}}
	spec, err = ResolveActiveModelSpec(cfg)
	if err != nil {
		t.Fatalf("ResolveActiveModelSpec fallback error: %v", err)
	}
	if spec.ID != "unknown-model" {
		t.Errorf("fallback spec = %+v", spec)
	}

	// Load failure (invalid JSON) → error with fallback spec.
	writeModelsFile(t, `{invalid`)
	t.Setenv("AI_MODELS_PATH", func() string {
		dir := t.TempDir()
		p := filepath.Join(dir, "bad.json")
		_ = os.WriteFile(p, []byte(`{invalid`), 0644)
		return p
	}())
	cfg = &Config{Model: ModelConfig{ID: "m", Provider: "p"}}
	spec, err = ResolveActiveModelSpec(cfg)
	if err == nil {
		t.Error("expected error when models.json is invalid")
	}
	if spec.ID != "m" {
		t.Errorf("error path should still return fallback spec, got %+v", spec)
	}
}

func TestFilterModelSpecsWithKeys(t *testing.T) {
	// Set up auth so "authed-provider" resolves and "nokey-provider" does not.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AI_MODELS_PATH", "")
	authDir := filepath.Join(home, ".ai")
	if err := os.MkdirAll(authDir, 0755); err != nil {
		t.Fatal(err)
	}
	authJSON := `{"authed-provider":{"key":"k"}}`
	if err := os.WriteFile(filepath.Join(authDir, "auth.json"), []byte(authJSON), 0644); err != nil {
		t.Fatal(err)
	}
	// Ensure no env keys leak in for the unauthed provider.
	t.Setenv("NOKEYPROVIDER_API_KEY", "")

	specs := []ModelSpec{
		{ID: "a", Provider: "authed-provider"},
		{ID: "b", Provider: "nokey-provider"},
	}
	filtered := FilterModelSpecsWithKeys(specs)
	if len(filtered) != 1 || filtered[0].ID != "a" {
		t.Errorf("filtered = %+v, want only spec a", filtered)
	}
}

func TestApplyModelOverride_RealFile(t *testing.T) {
	writeModelsFile(t, testModelsJSON)

	cfg := &Config{Model: ModelConfig{ID: "old", Provider: "old-p", BaseURL: "https://old", API: "openai"}}
	ApplyModelOverride(cfg, "deepseek/deepseek-chat")

	if cfg.Model.ID != "deepseek-chat" {
		t.Errorf("ID = %q, want deepseek-chat", cfg.Model.ID)
	}
	if cfg.Model.Provider != "deepseek" {
		t.Errorf("Provider = %q, want deepseek", cfg.Model.Provider)
	}
	if cfg.Model.BaseURL != "https://api.deepseek.com" {
		t.Errorf("BaseURL = %q", cfg.Model.BaseURL)
	}
}
