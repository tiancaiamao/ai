package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadModelSpecs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	data := `{
  "providers": {
    "zai": {
      "baseUrl": "https://api.z.ai/api/coding/paas/v4",
      "api": "openai-completions",
      "models": [
        { "id": "glm-4.5-air", "name": "GLM 4.5 Air", "reasoning": true, "input": ["text"] }
      ]
    }
  }
}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write models.json: %v", err)
	}

	specs, err := LoadModelSpecs(path)
	if err != nil {
		t.Fatalf("LoadModelSpecs error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	spec := specs[0]
	if spec.Provider != "zai" {
		t.Errorf("provider = %q, want %q", spec.Provider, "zai")
	}
	if spec.ID != "glm-4.5-air" {
		t.Errorf("id = %q, want %q", spec.ID, "glm-4.5-air")
	}
	if spec.BaseURL != "https://api.z.ai/api/coding/paas/v4" {
		t.Errorf("baseUrl = %q", spec.BaseURL)
	}
	if spec.API != "openai-completions" {
		t.Errorf("api = %q", spec.API)
	}
	if spec.Name != "GLM 4.5 Air" {
		t.Errorf("name = %q", spec.Name)
	}
	if !spec.Reasoning {
		t.Errorf("reasoning = false, want true")
	}
}

func TestLoadModelSpecsOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	data := `{
  "providers": {
    "custom": {
      "baseUrl": "https://provider.example/v1",
      "api": "openai-completions",
      "models": [
        { "id": "model-a", "api": "anthropic-messages", "baseUrl": "https://model.example/v1" }
      ]
    }
  }
}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write models.json: %v", err)
	}

	specs, err := LoadModelSpecs(path)
	if err != nil {
		t.Fatalf("LoadModelSpecs error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	spec := specs[0]
	if spec.BaseURL != "https://model.example/v1" {
		t.Errorf("baseUrl = %q, want %q", spec.BaseURL, "https://model.example/v1")
	}
	if spec.API != "anthropic-messages" {
		t.Errorf("api = %q, want %q", spec.API, "anthropic-messages")
	}
}
