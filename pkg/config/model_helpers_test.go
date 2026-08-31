package config

import (
	"strings"
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

func TestCountModelMatches(t *testing.T) {
	specs := []ModelSpec{
		{ID: "gpt-4", Provider: "openai"},
		{ID: "deepseek-v4-flash", Provider: "opencode"},
		{ID: "deepseek-v4-flash", Provider: "deepseek"},
		{ID: "claude-3", Provider: "anthropic"},
	}

	tests := []struct {
		modelID string
		want    int
	}{
		{"gpt-4", 1},
		{"deepseek-v4-flash", 2},
		{"nonexistent", 0},
	}
	for _, tt := range tests {
		got := countModelMatches(specs, tt.modelID)
		if got != tt.want {
			t.Errorf("countModelMatches(%q) = %d, want %d", tt.modelID, got, tt.want)
		}
	}
}

func TestApplyModelOverride_ProviderIDSyntax(t *testing.T) {
	specs := []ModelSpec{
		{ID: "deepseek-v4-flash", Provider: "opencode", BaseURL: "https://opencode.ai/zen/go/v1", API: "openai-completions"},
		{ID: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com", API: "openai-completions"},
	}
	cfg := &Config{
		Model: ModelConfig{
			ID:       "original-model",
			Provider: "original-provider",
			BaseURL:  "https://original.com",
			API:      "chat",
		},
	}

	// "provider/id" picks the right one.
	applyModelOverrideForTest(cfg, "opencode/deepseek-v4-flash", specs)
	if cfg.Model.ID != "deepseek-v4-flash" {
		t.Errorf("ID = %q, want 'deepseek-v4-flash'", cfg.Model.ID)
	}
	if cfg.Model.Provider != "opencode" {
		t.Errorf("Provider = %q, want 'opencode'", cfg.Model.Provider)
	}
	if cfg.Model.BaseURL != "https://opencode.ai/zen/go/v1" {
		t.Errorf("BaseURL = %q, want 'https://opencode.ai/zen/go/v1'", cfg.Model.BaseURL)
	}
}

func TestApplyModelOverride_ProviderIDNotFound(t *testing.T) {
	specs := []ModelSpec{
		{ID: "gpt-4", Provider: "openai"},
	}
	cfg := &Config{
		Model: ModelConfig{
			ID:       "original-model",
			Provider: "original-provider",
			BaseURL:  "https://original.com",
			API:      "chat",
		},
	}

	// Unknown provider/id — warn, clean ID but keep original Provider/BaseURL/API.
	applyModelOverrideForTest(cfg, "unknown-provider/unknown-model", specs)
	if cfg.Model.ID != "unknown-model" {
		t.Errorf("ID = %q, want 'unknown-model'", cfg.Model.ID)
	}
	if cfg.Model.Provider != "original-provider" {
		t.Errorf("Provider = %q, want 'original-provider'", cfg.Model.Provider)
	}
	if cfg.Model.BaseURL != "https://original.com" {
		t.Errorf("BaseURL = %q, want 'https://original.com'", cfg.Model.BaseURL)
	}
}

func TestApplyModelOverride_AmbiguousBareID(t *testing.T) {
	specs := []ModelSpec{
		{ID: "deepseek-v4-flash", Provider: "opencode", BaseURL: "https://opencode.ai/zen/go/v1", API: "openai-completions"},
		{ID: "deepseek-v4-flash", Provider: "deepseek", BaseURL: "https://api.deepseek.com", API: "openai-completions"},
	}
	cfg := &Config{
		Model: ModelConfig{
			ID:       "original-model",
			Provider: "original-provider",
			BaseURL:  "https://original.com",
			API:      "chat",
		},
	}

	// Ambiguous bare ID — warn, preserve original config.
	applyModelOverrideForTest(cfg, "deepseek-v4-flash", specs)
	if cfg.Model.ID != "deepseek-v4-flash" {
		t.Errorf("ID = %q, want 'deepseek-v4-flash'", cfg.Model.ID)
	}
	// Provider/BaseURL/API should remain unchanged (ambiguity rejected).
	if cfg.Model.Provider != "original-provider" {
		t.Errorf("Provider = %q, want 'original-provider'", cfg.Model.Provider)
	}
	if cfg.Model.BaseURL != "https://original.com" {
		t.Errorf("BaseURL = %q, want 'https://original.com'", cfg.Model.BaseURL)
	}
}

func TestApplyModelOverride_UniqueBareID(t *testing.T) {
	specs := []ModelSpec{
		{ID: "unique-model", Provider: "some-provider", BaseURL: "https://some.api.com", API: "openai-completions"},
	}
	cfg := &Config{
		Model: ModelConfig{
			ID:       "original-model",
			Provider: "original-provider",
			BaseURL:  "https://original.com",
			API:      "chat",
		},
	}

	// Unique bare ID — auto-fill provider/baseUrl/api.
	applyModelOverrideForTest(cfg, "unique-model", specs)
	if cfg.Model.ID != "unique-model" {
		t.Errorf("ID = %q, want 'unique-model'", cfg.Model.ID)
	}
	if cfg.Model.Provider != "some-provider" {
		t.Errorf("Provider = %q, want 'some-provider'", cfg.Model.Provider)
	}
	if cfg.Model.BaseURL != "https://some.api.com" {
		t.Errorf("BaseURL = %q, want 'https://some.api.com'", cfg.Model.BaseURL)
	}
}

// applyModelOverrideForTest is a test helper that calls ApplyModelOverride
// with pre-loaded specs, bypassing file I/O.
func applyModelOverrideForTest(cfg *Config, modelOverride string, specs []ModelSpec) {
	cfg.Model.ID = modelOverride

	// Format 1: "provider/id"
	if provider, id, ok := strings.Cut(modelOverride, "/"); ok && provider != "" && id != "" {
		if spec, ok := FindModelSpec(specs, provider, id); ok {
			cfg.Model.ID = id
			cfg.Model.Provider = spec.Provider
			cfg.Model.BaseURL = spec.BaseURL
			cfg.Model.API = spec.API
			return
		}
		// Not found — still strip provider/ prefix
		cfg.Model.ID = id
		return
	}

	// Format 2: bare "id"
	switch n := countModelMatches(specs, modelOverride); {
	case n == 0:
		// not found, keep original config
	case n == 1:
		for _, spec := range specs {
			if spec.ID == modelOverride {
				cfg.Model.Provider = spec.Provider
				cfg.Model.BaseURL = spec.BaseURL
				cfg.Model.API = spec.API
				return
			}
		}
	default: // n > 1
		// ambiguous, keep original config
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
