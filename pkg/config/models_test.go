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

func TestParseCapabilities(t *testing.T) {
	tests := []struct {
		name        string
		input       []string
		caps        []string
		expected    ModelCapability
		description string
	}{
		{
			name:        "text only from input",
			input:       []string{"text"},
			caps:        nil,
			expected:    CapabilityText,
			description: "text only model should have text capability",
		},
		{
			name:        "vision from input",
			input:       []string{"text", "image"},
			caps:        nil,
			expected:    CapabilityText | CapabilityVision,
			description: "model with image input should have text and vision",
		},
		{
			name:        "vision from input with vision keyword",
			input:       []string{"text", "vision"},
			caps:        nil,
			expected:    CapabilityText | CapabilityVision,
			description: "model with vision input should have text and vision",
		},
		{
			name:        "explicit capabilities",
			input:       []string{"text"},
			caps:        []string{"vision"},
			expected:    CapabilityText | CapabilityVision,
			description: "explicit capabilities should be added",
		},
		{
			name:        "function calling from input",
			input:       []string{"text", "function_calling"},
			caps:        nil,
			expected:    CapabilityText | CapabilityFunctionCalling,
			description: "model with function_calling input should have function calling",
		},
		{
			name:        "combined all",
			input:       []string{"text", "image", "function_calling"},
			caps:        nil,
			expected:    CapabilityText | CapabilityVision | CapabilityFunctionCalling,
			description: "all capabilities from input",
		},
		{
			name:        "empty defaults to text",
			input:       nil,
			caps:        nil,
			expected:    CapabilityText,
			description: "empty input should default to text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCapabilities(tt.caps, tt.input)
			if result != tt.expected {
				t.Errorf("%s: parseCapabilities(%v, %v) = %v, want %v", tt.description, tt.caps, tt.input, result, tt.expected)
			}
		})
	}
}

func TestLoadModelSpecsWithCapabilities(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	data := `{
  "providers": {
    "test": {
      "models": [
        { "id": "text-only", "name": "Text Only", "input": ["text"] },
        { "id": "vision-model", "name": "Vision Model", "input": ["text", "image"] },
        { "id": "explicit-vision", "name": "Explicit Vision", "input": ["text"], "capabilities": ["vision"] }
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

	// Find and check text-only model
	var textOnly *ModelSpec
	var visionModel *ModelSpec
	var explicitVision *ModelSpec

	for i := range specs {
		switch specs[i].ID {
		case "text-only":
			textOnly = &specs[i]
		case "vision-model":
			visionModel = &specs[i]
		case "explicit-vision":
			explicitVision = &specs[i]
		}
	}

	if textOnly == nil {
		t.Fatal("text-only model not found")
	}
	if textOnly.Capabilities != CapabilityText {
		t.Errorf("text-only model capabilities = %v, want %v", textOnly.Capabilities, CapabilityText)
	}

	if visionModel == nil {
		t.Fatal("vision-model not found")
	}
	if visionModel.Capabilities != (CapabilityText | CapabilityVision) {
		t.Errorf("vision-model capabilities = %v, want %v", visionModel.Capabilities, CapabilityText|CapabilityVision)
	}

	if explicitVision == nil {
		t.Fatal("explicit-vision not found")
	}
	if explicitVision.Capabilities != (CapabilityText | CapabilityVision) {
		t.Errorf("explicit-vision capabilities = %v, want %v", explicitVision.Capabilities, CapabilityText|CapabilityVision)
	}
}
