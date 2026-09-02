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
	spec, matched, err := ResolveActiveModelSpec(cfg)
	if err != nil {
		t.Fatalf("ResolveActiveModelSpec error: %v", err)
	}
	if !matched {
		t.Error("expected matched=true for zai/glm-4.6")
	}
	if spec.ID != "glm-4.6" || spec.Provider != "zai" {
		t.Errorf("spec = %+v, want zai/glm-4.6", spec)
	}
	if spec.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", spec.ContextWindow)
	}

	// No matching spec → falls back to ModelSpecFromConfig.
	cfg = &Config{Model: ModelConfig{ID: "unknown-model", Provider: "zai", API: "openai"}}
	spec, matched, err = ResolveActiveModelSpec(cfg)
	if err != nil {
		t.Fatalf("ResolveActiveModelSpec fallback error: %v", err)
	}
	if matched {
		t.Error("expected matched=false for unknown model")
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
	spec, matched, err = ResolveActiveModelSpec(cfg)
	if err == nil {
		t.Error("expected error when models.json is invalid")
	}
	if matched {
		t.Error("expected matched=false on load error")
	}
	if spec.ID != "m" {
		t.Errorf("error path should still return fallback spec, got %+v", spec)
	}
}

func TestResolveModel_SpecWinsOnMatch(t *testing.T) {
	writeModelsFile(t, testModelsJSON)

	// Config carries stale endpoint fields for a model that exists in
	// models.json — the spec must win.
	cfg := &Config{Model: ModelConfig{
		ID:        "glm-4.6",
		Provider:  "zai",
		BaseURL:   "https://chatgpt.com/backend-api",
		API:       "openai-codex-responses",
		MaxTokens: 999,
	}}
	model, spec, err := ResolveModel(cfg)
	if err != nil {
		t.Fatalf("ResolveModel error: %v", err)
	}
	if spec.ID != "glm-4.6" || spec.Provider != "zai" {
		t.Fatalf("spec = %+v, want zai/glm-4.6", spec)
	}
	if model.BaseURL != "https://api.z.ai/v1" {
		t.Errorf("BaseURL = %q, want spec value", model.BaseURL)
	}
	if model.API != "openai" {
		t.Errorf("API = %q, want spec value", model.API)
	}
	if model.MaxTokens != 128000 {
		t.Errorf("MaxTokens = %d, want spec value 128000", model.MaxTokens)
	}
	if model.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want spec value 200000", model.ContextWindow)
	}
}

func TestResolveModel_ConfigKeptWithoutMatch(t *testing.T) {
	writeModelsFile(t, testModelsJSON)

	// No models.json entry → config's own endpoint fields apply
	// (custom endpoints scenario).
	cfg := &Config{Model: ModelConfig{
		ID:        "custom-model",
		Provider:  "zai",
		BaseURL:   "http://localhost:8080/v1",
		API:       "anthropic-messages",
		MaxTokens: 4096,
	}}
	model, spec, err := ResolveModel(cfg)
	if err != nil {
		t.Fatalf("ResolveModel error: %v", err)
	}
	if spec.ID != "custom-model" {
		t.Errorf("spec = %+v, want config fallback", spec)
	}
	if model.BaseURL != "http://localhost:8080/v1" {
		t.Errorf("BaseURL = %q, want config value", model.BaseURL)
	}
	if model.API != "anthropic-messages" {
		t.Errorf("API = %q, want config value", model.API)
	}
	if model.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want config value", model.MaxTokens)
	}
}

func TestResolveModel_SparseSpecKeepsConfigFields(t *testing.T) {
	// Spec entry without baseUrl/api at any level → empty spec fields fall
	// back to the config values instead of wiping them.
	writeModelsFile(t, `{
  "providers": {
    "p": {
      "models": [
        {"id": "sparse-model", "contextWindow": 100000}
      ]
    }
  }
}`)
	cfg := &Config{Model: ModelConfig{
		ID:       "sparse-model",
		Provider: "p",
		BaseURL:  "http://localhost:8080/v1",
		API:      "anthropic-messages",
	}}
	model, _, err := ResolveModel(cfg)
	if err != nil {
		t.Fatalf("ResolveModel error: %v", err)
	}
	if model.BaseURL != "http://localhost:8080/v1" {
		t.Errorf("BaseURL = %q, want config value kept for sparse spec", model.BaseURL)
	}
	if model.API != "anthropic-messages" {
		t.Errorf("API = %q, want config value kept for sparse spec", model.API)
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
