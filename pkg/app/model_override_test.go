package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tiancaiamao/ai/pkg/config"
)

// --- applyModelOverride tests ---

// TestApplyModelOverride_EmptySetsEmptyID verifies the function behavior with
// an empty string. In practice the caller guards against this with
// `if params.modelOverride != ""`, but the function itself does not guard.
func TestApplyModelOverride_EmptySetsEmptyID(t *testing.T) {
	cfg := &config.Config{
		Model: config.ModelConfig{
			ID:       "original-model",
			Provider: "openai",
			BaseURL:  "https://api.openai.com",
			API:      "chat",
		},
	}
	applyModelOverride(cfg, "")
	// Function does not guard against empty — caller's responsibility.
	if cfg.Model.ID != "" {
		t.Errorf("expected empty ID after override with empty string, got %q", cfg.Model.ID)
	}
}

// TestApplyModelOverride_RawIDWithoutSpecs verifies that --model with an
// unknown model ID sets cfg.Model.ID but does not crash.
func TestApplyModelOverride_RawIDWithoutSpecs(t *testing.T) {
	// Create a temporary models.json with a known model.
	tmpDir := t.TempDir()
	modelsPath := filepath.Join(tmpDir, "models.json")
	modelsData := map[string]interface{}{
		"providers": map[string]interface{}{
			"test-provider": map[string]interface{}{
				"baseUrl": "https://test.example.com",
				"api":     "chat",
				"models": []map[string]interface{}{
					{
						"id":            "test-model-1",
						"contextWindow": 128000,
						"maxTokens":     4096,
					},
				},
			},
		},
	}
	data, _ := json.Marshal(modelsData)
	if err := os.WriteFile(modelsPath, data, 0644); err != nil {
		t.Fatalf("write models.json: %v", err)
	}

	// Override AI_MODELS_PATH to point to our temp file.
	originalModelsPath := os.Getenv("AI_MODELS_PATH")
	os.Setenv("AI_MODELS_PATH", modelsPath)
	defer os.Setenv("AI_MODELS_PATH", originalModelsPath)

	cfg := &config.Config{
		Model: config.ModelConfig{
			ID:       "original-model",
			Provider: "openai",
			BaseURL:  "https://api.openai.com",
			API:      "chat",
		},
	}

	// Override with a model NOT in models.json.
	applyModelOverride(cfg, "nonexistent-model-xyz")

	// ID should be overridden.
	if cfg.Model.ID != "nonexistent-model-xyz" {
		t.Errorf("expected ID 'nonexistent-model-xyz', got %q", cfg.Model.ID)
	}
	// Provider/BaseURL/API should remain from original config (not found in specs).
	if cfg.Model.Provider != "openai" {
		t.Errorf("expected Provider unchanged 'openai', got %q", cfg.Model.Provider)
	}
	if cfg.Model.BaseURL != "https://api.openai.com" {
		t.Errorf("expected BaseURL unchanged, got %q", cfg.Model.BaseURL)
	}
}

// TestApplyModelOverride_FoundInSpecs verifies that --model with a known model ID
// auto-fills Provider/BaseURL/API from models.json.
func TestApplyModelOverride_FoundInSpecs(t *testing.T) {
	tmpDir := t.TempDir()
	modelsPath := filepath.Join(tmpDir, "models.json")
	modelsData := map[string]interface{}{
		"providers": map[string]interface{}{
			"zai": map[string]interface{}{
				"baseUrl": "https://api.zai.example.com",
				"api":     "responses",
				"models": []map[string]interface{}{
					{
						"id":            "claude-sonnet-4-20250514",
						"contextWindow": 200000,
						"maxTokens":     16384,
					},
				},
			},
		},
	}
	data, _ := json.Marshal(modelsData)
	if err := os.WriteFile(modelsPath, data, 0644); err != nil {
		t.Fatalf("write models.json: %v", err)
	}

	originalModelsPath := os.Getenv("AI_MODELS_PATH")
	os.Setenv("AI_MODELS_PATH", modelsPath)
	defer os.Setenv("AI_MODELS_PATH", originalModelsPath)

	cfg := &config.Config{
		Model: config.ModelConfig{
			ID:       "original-model",
			Provider: "openai",
			BaseURL:  "https://api.openai.com",
			API:      "chat",
		},
	}

	// Override with a model that IS in models.json.
	applyModelOverride(cfg, "claude-sonnet-4-20250514")

	if cfg.Model.ID != "claude-sonnet-4-20250514" {
		t.Errorf("expected ID 'claude-sonnet-4-20250514', got %q", cfg.Model.ID)
	}
	if cfg.Model.Provider != "zai" {
		t.Errorf("expected Provider 'zai', got %q", cfg.Model.Provider)
	}
	if cfg.Model.BaseURL != "https://api.zai.example.com" {
		t.Errorf("expected BaseURL 'https://api.zai.example.com', got %q", cfg.Model.BaseURL)
	}
	if cfg.Model.API != "responses" {
		t.Errorf("expected API 'responses', got %q", cfg.Model.API)
	}
}

// TestApplyModelOverride_NoModelsFile verifies that --model works when models.json
// doesn't exist (should log warning and proceed with raw ID).
func TestApplyModelOverride_NoModelsFile(t *testing.T) {
	tmpDir := t.TempDir()
	modelsPath := filepath.Join(tmpDir, "nonexistent-models.json")

	originalModelsPath := os.Getenv("AI_MODELS_PATH")
	os.Setenv("AI_MODELS_PATH", modelsPath)
	defer os.Setenv("AI_MODELS_PATH", originalModelsPath)

	cfg := &config.Config{
		Model: config.ModelConfig{
			ID:       "original-model",
			Provider: "openai",
			BaseURL:  "https://api.openai.com",
			API:      "chat",
		},
	}

	// Should not panic when models.json doesn't exist.
	applyModelOverride(cfg, "some-model")

	if cfg.Model.ID != "some-model" {
		t.Errorf("expected ID 'some-model', got %q", cfg.Model.ID)
	}
	// Provider/BaseURL/API should remain from original config.
	if cfg.Model.Provider != "openai" {
		t.Errorf("expected Provider 'openai', got %q", cfg.Model.Provider)
	}
}
