package config

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestModelInfoFromSpec(t *testing.T) {
	t.Run("with name", func(t *testing.T) {
		info := ModelInfoFromSpec(ModelSpec{ID: "gpt-4", Name: "GPT-4", Provider: "openai"})
		if info.Name != "GPT-4" {
			t.Errorf("Name = %q, want GPT-4", info.Name)
		}
	})
	t.Run("fallback to ID", func(t *testing.T) {
		info := ModelInfoFromSpec(ModelSpec{ID: "gpt-4"})
		if info.Name != "gpt-4" {
			t.Errorf("Name = %q, want gpt-4", info.Name)
		}
	})
	t.Run("default input", func(t *testing.T) {
		info := ModelInfoFromSpec(ModelSpec{ID: "gpt-4"})
		if len(info.Input) != 1 || info.Input[0] != "text" {
			t.Errorf("Input = %v, want [text]", info.Input)
		}
	})
	t.Run("custom input", func(t *testing.T) {
		info := ModelInfoFromSpec(ModelSpec{ID: "gpt-4", Input: []string{"text", "image"}})
		if len(info.Input) != 2 {
			t.Errorf("Input len = %d, want 2", len(info.Input))
		}
	})
}

func TestModelSpecFromConfig(t *testing.T) {
	cfg := &Config{
		Model: ModelConfig{
			ID:        "gpt-4",
			Provider:  "openai",
			BaseURL:   "https://api.openai.com",
			API:       "openai",
			MaxTokens: 4096,
		},
	}
	spec := ModelSpecFromConfig(cfg)
	if spec.ID != "gpt-4" {
		t.Errorf("ID = %q, want gpt-4", spec.ID)
	}
	if spec.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", spec.Provider)
	}
	if spec.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", spec.MaxTokens)
	}
	if len(spec.Input) != 1 || spec.Input[0] != "text" {
		t.Errorf("Input = %v, want [text]", spec.Input)
	}
}

func TestApplyModelLimitsFromSpec(t *testing.T) {
	tests := []struct {
		name   string
		model  llm.Model
		spec   ModelSpec
		wantCW int
		wantMT int
		wantR  bool
	}{
		{
			name:   "fills zeros",
			model:  llm.Model{},
			spec:   ModelSpec{ContextWindow: 128000, MaxTokens: 4096, Reasoning: true},
			wantCW: 128000,
			wantMT: 4096,
			wantR:  true,
		},
		{
			name:   "preserves non-zero",
			model:  llm.Model{ContextWindow: 8000, MaxTokens: 2048},
			spec:   ModelSpec{ContextWindow: 128000, MaxTokens: 4096},
			wantCW: 8000,
			wantMT: 2048,
			wantR:  false,
		},
		{
			name:   "reasoning sticky",
			model:  llm.Model{Reasoning: true},
			spec:   ModelSpec{Reasoning: false},
			wantCW: 0,
			wantMT: 0,
			wantR:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyModelLimitsFromSpec(tt.model, tt.spec)
			if got.ContextWindow != tt.wantCW {
				t.Errorf("ContextWindow = %d, want %d", got.ContextWindow, tt.wantCW)
			}
			if got.MaxTokens != tt.wantMT {
				t.Errorf("MaxTokens = %d, want %d", got.MaxTokens, tt.wantMT)
			}
			if got.Reasoning != tt.wantR {
				t.Errorf("Reasoning = %v, want %v", got.Reasoning, tt.wantR)
			}
		})
	}
}

func TestFindModelSpec(t *testing.T) {
	specs := []ModelSpec{
		{ID: "gpt-4", Provider: "openai"},
		{ID: "claude-3", Provider: "anthropic"},
	}

	tests := []struct {
		provider string
		modelID  string
		found    bool
	}{
		{"openai", "gpt-4", true},
		{"anthropic", "claude-3", true},
		{"openai", "claude-3", false},
		{"", "", false},
	}
	for _, tt := range tests {
		_, ok := FindModelSpec(specs, tt.provider, tt.modelID)
		if ok != tt.found {
			t.Errorf("FindModelSpec(%q, %q) found=%v, want %v", tt.provider, tt.modelID, ok, tt.found)
		}
	}
}
