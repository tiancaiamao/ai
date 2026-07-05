package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// ModelInfoFromSpec converts a ModelSpec to a ModelInfo for display.
func ModelInfoFromSpec(spec ModelSpec) ModelInfo {
	name := spec.Name
	if name == "" {
		name = spec.ID
	}
	input := spec.Input
	if len(input) == 0 {
		input = []string{"text"}
	}
	return ModelInfo{
		ID:            spec.ID,
		Name:          name,
		Provider:      spec.Provider,
		API:           spec.API,
		Reasoning:     spec.Reasoning,
		Input:         input,
		ContextWindow: spec.ContextWindow,
		MaxTokens:     spec.MaxTokens,
	}
}

// ModelSpecFromConfig builds a ModelSpec from the active config's model settings.
func ModelSpecFromConfig(cfg *Config) ModelSpec {
	return ModelSpec{
		ID:        cfg.Model.ID,
		Name:      cfg.Model.ID,
		Provider:  cfg.Model.Provider,
		BaseURL:   cfg.Model.BaseURL,
		API:       cfg.Model.API,
		Input:     []string{"text"},
		MaxTokens: cfg.Model.MaxTokens,
	}
}

// LoadModelSpecs loads model specs from models.json, falling back to config on not-exist.
func LoadModelSpecsFromConfig(cfg *Config) ([]ModelSpec, string, error) {
	modelsPath, err := ResolveModelsPath()
	if err != nil {
		return nil, "", err
	}

	specs, err := LoadModelSpecs(modelsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ModelSpec{ModelSpecFromConfig(cfg)}, modelsPath, nil
		}
		return nil, modelsPath, err
	}

	if len(specs) == 0 {
		return nil, modelsPath, fmt.Errorf("no models defined in %s", modelsPath)
	}

	return specs, modelsPath, nil
}

// FilterModelSpecsWithKeys returns only specs whose provider has an API key configured.
func FilterModelSpecsWithKeys(specs []ModelSpec) []ModelSpec {
	var result []ModelSpec
	for _, spec := range specs {
		if _, err := ResolveAPIKey(spec.Provider); err == nil {
			result = append(result, spec)
		}
	}
	return result
}

// FindModelSpec looks up a spec by provider and model ID.
func FindModelSpec(specs []ModelSpec, provider, modelID string) (ModelSpec, bool) {
	for _, spec := range specs {
		if spec.Provider == provider && spec.ID == modelID {
			return spec, true
		}
	}
	return ModelSpec{}, false
}

// ResolveActiveModelSpec finds the matching spec from models.json, falling back to config.
func ResolveActiveModelSpec(cfg *Config) (ModelSpec, error) {
	specs, modelsPath, err := LoadModelSpecsFromConfig(cfg)
	if err != nil {
		return ModelSpecFromConfig(cfg), fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	if spec, ok := FindModelSpec(specs, cfg.Model.Provider, cfg.Model.ID); ok {
		return spec, nil
	}
	return ModelSpecFromConfig(cfg), nil
}

// ApplyModelLimitsFromSpec fills in zero-valued model fields from the spec.
func ApplyModelLimitsFromSpec(model llm.Model, spec ModelSpec) llm.Model {
	if model.ContextWindow <= 0 && spec.ContextWindow > 0 {
		model.ContextWindow = spec.ContextWindow
	}
	if model.MaxTokens <= 0 && spec.MaxTokens > 0 {
		model.MaxTokens = spec.MaxTokens
	}
	if spec.Reasoning {
		model.Reasoning = true
	}
	return model
}

// countModelMatches counts how many providers have a given model ID.
func countModelMatches(specs []ModelSpec, modelID string) int {
	n := 0
	for _, spec := range specs {
		if spec.ID == modelID {
			n++
		}
	}
	return n
}

// ApplyModelOverride sets the model ID from the CLI --model flag.
//
// Two formats are supported:
//
//  1. "provider/id" — exact match: split on '/', resolve via FindModelSpec.
//  2. bare "id" — unique match: only auto-fills Provider/BaseURL/API when
//     exactly one provider has this model ID. If multiple providers share the
//     same ID, the ambiguity is reported and the original config is preserved.
func ApplyModelOverride(cfg *Config, modelOverride string) {
	cfg.Model.ID = modelOverride
	specs, _, specErr := LoadModelSpecsFromConfig(cfg)
	if specErr != nil {
		slog.Warn("Model override: could not load model specs, using raw ID", "id", modelOverride, "error", specErr)
		return
	}

	// Format 1: "provider/id" — exact match.
	if provider, id, ok := strings.Cut(modelOverride, "/"); ok && provider != "" && id != "" {
		if spec, ok := FindModelSpec(specs, provider, id); ok {
			cfg.Model.ID = id
			cfg.Model.Provider = spec.Provider
			cfg.Model.BaseURL = spec.BaseURL
			cfg.Model.API = spec.API
			slog.Info("Model override applied", "id", id, "provider", spec.Provider)
			return
		}
		slog.Warn("Model override: provider/id not found in models.json, using raw ID with existing config",
			"provider", provider, "id", id)
		return
	}

	// Format 2: bare "id" — only accept when exactly one provider has it.
	switch n := countModelMatches(specs, modelOverride); {
	case n == 0:
		slog.Warn("Model override: model ID not found in models.json, using raw ID with existing config", "id", modelOverride)
	case n == 1:
		for _, spec := range specs {
			if spec.ID == modelOverride {
				cfg.Model.Provider = spec.Provider
				cfg.Model.BaseURL = spec.BaseURL
				cfg.Model.API = spec.API
				slog.Info("Model override applied", "id", modelOverride, "provider", spec.Provider)
				return
			}
		}
	default: // n > 1
		slog.Warn("Model override: ambiguous model ID found in multiple providers, using raw ID with existing config. Use \"provider/id\" syntax to disambiguate.",
			"id", modelOverride, "matches", n)
	}
}
