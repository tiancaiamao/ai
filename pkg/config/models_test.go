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

func TestLoadModelSpecsReasoningEfforts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	data := `{
  "providers": {
    "opencode": {
      "models": [
        { "id": "always-thinking", "reasoning": true, "reasoningEfforts": ["low", "high", "max"] },
        { "id": "unrestricted", "reasoning": true }
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
	byID := map[string]ModelSpec{}
	for _, spec := range specs {
		byID[spec.ID] = spec
	}
	got, ok := byID["always-thinking"]
	if !ok {
		t.Fatal("missing always-thinking spec")
	}
	want := []string{"low", "high", "max"}
	if len(got.ReasoningEfforts) != len(want) {
		t.Fatalf("reasoningEfforts = %v, want %v", got.ReasoningEfforts, want)
	}
	for i := range want {
		if got.ReasoningEfforts[i] != want[i] {
			t.Fatalf("reasoningEfforts = %v, want %v", got.ReasoningEfforts, want)
		}
	}
	if got := byID["unrestricted"].ReasoningEfforts; got != nil {
		t.Errorf("unrestricted reasoningEfforts = %v, want nil", got)
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

func TestLoadModelSpecsDeterministicSort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	data := `{
  "providers": {
    "zai": {
      "baseUrl": "https://api.z.ai/api/coding/paas/v4",
      "api": "openai-completions",
      "models": [
        { "id": "glm-5", "name": "GLM 5" },
        { "id": "glm-4.7", "name": "GLM 4.7" }
      ]
    },
    "anthropic": {
      "baseUrl": "https://api.anthropic.com/v1",
      "api": "anthropic-messages",
      "models": [
        { "id": "claude-sonnet-4-20250514", "name": "Claude Sonnet 4" }
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
	if len(specs) != 3 {
		t.Fatalf("expected 3 specs, got %d", len(specs))
	}

	got := []string{
		specs[0].Provider + "/" + specs[0].ID,
		specs[1].Provider + "/" + specs[1].ID,
		specs[2].Provider + "/" + specs[2].ID,
	}
	want := []string{
		"anthropic/claude-sonnet-4-20250514",
		"zai/glm-4.7",
		"zai/glm-5",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSupportsVision(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  bool
	}{
		{name: "text only", input: []string{"text"}, want: false},
		{name: "image input", input: []string{"text", "image"}, want: true},
		{name: "vision keyword", input: []string{"text", "vision"}, want: true},
		{name: "empty defaults to text", input: nil, want: false},
		{name: "case insensitive", input: []string{"TEXT", "Image"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supportsVision(tt.input); got != tt.want {
				t.Errorf("supportsVision(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoadModelSpecsSupportsVision(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	data := `{
  "providers": {
    "test": {
      "models": [
        { "id": "text-only", "name": "Text Only", "input": ["text"] },
        { "id": "vision-model", "name": "Vision Model", "input": ["text", "image"] }
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

	byID := make(map[string]ModelSpec)
	for _, spec := range specs {
		byID[spec.ID] = spec
	}

	if spec, ok := byID["text-only"]; !ok {
		t.Fatal("text-only model not found")
	} else if spec.SupportsVision {
		t.Errorf("text-only model SupportsVision = true, want false")
	}

	if spec, ok := byID["vision-model"]; !ok {
		t.Fatal("vision-model not found")
	} else if !spec.SupportsVision {
		t.Errorf("vision-model SupportsVision = false, want true")
	}
}
