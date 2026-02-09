package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ModelSpec represents a resolved model entry from models.json.
type ModelSpec struct {
	ID            string
	Name          string
	Provider      string
	BaseURL       string
	API           string
	Reasoning     bool
	Input         []string
	ContextWindow int
	MaxTokens     int
}

type modelsFile struct {
	Providers map[string]providerConfig `json:"providers"`
}

type providerConfig struct {
	BaseURL string        `json:"baseUrl,omitempty"`
	API     string        `json:"api,omitempty"`
	Models  []modelConfig `json:"models,omitempty"`
}

type modelConfig struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	BaseURL       string   `json:"baseUrl,omitempty"`
	API           string   `json:"api,omitempty"`
	Reasoning     bool     `json:"reasoning,omitempty"`
	Input         []string `json:"input,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	MaxTokens     int      `json:"maxTokens,omitempty"`
}

// GetDefaultModelsPath returns the default models file path.
func GetDefaultModelsPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".ai", "models.json"), nil
}

// ResolveModelsPath returns the models file path, honoring AI_MODELS_PATH if set.
func ResolveModelsPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AI_MODELS_PATH")); override != "" {
		return override, nil
	}
	return GetDefaultModelsPath()
}

// LoadModelSpecs loads model specifications from a models.json file.
func LoadModelSpecs(path string) ([]ModelSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg modelsFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if len(cfg.Providers) == 0 {
		return nil, nil
	}

	providers := make([]string, 0, len(cfg.Providers))
	for provider := range cfg.Providers {
		providers = append(providers, provider)
	}
	sort.Strings(providers)

	specs := make([]ModelSpec, 0)
	for _, provider := range providers {
		pcfg := cfg.Providers[provider]
		provider = strings.TrimSpace(provider)
		baseURL := strings.TrimSpace(pcfg.BaseURL)
		api := strings.TrimSpace(pcfg.API)
		if provider == "" {
			continue
		}
		for _, model := range pcfg.Models {
			id := strings.TrimSpace(model.ID)
			if id == "" {
				continue
			}
			specs = append(specs, ModelSpec{
				ID:            id,
				Name:          strings.TrimSpace(model.Name),
				Provider:      provider,
				BaseURL:       firstNonEmpty(model.BaseURL, baseURL),
				API:           firstNonEmpty(model.API, api),
				Reasoning:     model.Reasoning,
				Input:         model.Input,
				ContextWindow: model.ContextWindow,
				MaxTokens:     model.MaxTokens,
			})
		}
	}

	return specs, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if v := strings.TrimSpace(value); v != "" {
			return v
		}
	}
	return ""
}
