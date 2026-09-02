package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
		Proxy:     cfg.Model.Proxy,
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
		if _, err := ResolveAPIKeyWithProxy(spec.Provider, spec.Proxy); err == nil {
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

// ResolveActiveModelSpec finds the matching spec from models.json, falling
// back to config. The second return value reports whether a models.json
// entry matched cfg.Model; when false (or err != nil) the returned spec was
// built from cfg.Model itself.
func ResolveActiveModelSpec(cfg *Config) (ModelSpec, bool, error) {
	specs, modelsPath, err := LoadModelSpecsFromConfig(cfg)
	if err != nil {
		return ModelSpecFromConfig(cfg), false, fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	if spec, ok := FindModelSpec(specs, cfg.Model.Provider, cfg.Model.ID); ok {
		return spec, true, nil
	}
	return ModelSpecFromConfig(cfg), false, nil
}

// ResolveModel resolves the LLM model for a run together with its active
// spec.
//
// config.json references a model by provider+ID; models.json owns the model
// facts (baseUrl, api, proxy, maxTokens, context window, ...). When the
// referenced provider+ID matches a models.json entry, the spec is
// authoritative: stale endpoint fields in config.json are ignored (a warning
// is logged). Only when no entry matches do config.json's own endpoint
// fields apply, which supports custom endpoints not registered in
// models.json.
func ResolveModel(cfg *Config) (llm.Model, ModelSpec, error) {
	spec, matched, err := ResolveActiveModelSpec(cfg)
	model := cfg.GetLLMModel()
	if err != nil {
		return model, spec, err
	}
	if matched {
		model = applySpecEndpoints(model, cfg.Model, spec)
	}
	return ApplyModelLimitsFromSpec(model, spec), spec, nil
}

// applySpecEndpoints overwrites the model's endpoint fields with the spec's
// values. Empty spec fields fall back to the config value so sparse
// models.json entries (e.g. api omitted at both provider and model level)
// keep working.
func applySpecEndpoints(model llm.Model, mc ModelConfig, spec ModelSpec) llm.Model {
	ignore := func(field, cfgVal string, specVal string) {
		if cfgVal != "" && cfgVal != specVal {
			slog.Warn("config.json model field ignored: models.json entry is authoritative",
				"field", field, "config", cfgVal, "spec", specVal)
		}
	}
	if spec.BaseURL != "" {
		ignore("baseUrl", mc.BaseURL, spec.BaseURL)
		model.BaseURL = spec.BaseURL
	}
	if spec.API != "" {
		ignore("api", mc.API, spec.API)
		model.API = spec.API
	}
	if spec.Proxy != "" {
		ignore("proxy", mc.Proxy, spec.Proxy)
		model.Proxy = spec.Proxy
	}
	if spec.MaxTokens > 0 {
		ignore("maxTokens", strconv.Itoa(mc.MaxTokens), strconv.Itoa(spec.MaxTokens))
		model.MaxTokens = spec.MaxTokens
	}
	return model
}

// ApplyModelLimitsFromSpec fills in zero-valued model fields from the spec,
// and caps MaxTokens/ContextWindow when the spec is more restrictive.
func ApplyModelLimitsFromSpec(model llm.Model, spec ModelSpec) llm.Model {
	if strings.TrimSpace(model.Proxy) == "" && strings.TrimSpace(spec.Proxy) != "" {
		model.Proxy = spec.Proxy
	}
	if model.ContextWindow <= 0 && spec.ContextWindow > 0 {
		model.ContextWindow = spec.ContextWindow
	}
	if model.MaxTokens <= 0 && spec.MaxTokens > 0 {
		model.MaxTokens = spec.MaxTokens
	} else if spec.MaxTokens > 0 && model.MaxTokens > spec.MaxTokens {
		// Spec has a lower max — cap to spec value (e.g. config from a
		// different model may have a higher maxTokens that doesn't apply).
		model.MaxTokens = spec.MaxTokens
	}
	if spec.Reasoning {
		model.Reasoning = true
	}
	if len(spec.ReasoningEfforts) > 0 {
		model.ReasoningEfforts = spec.ReasoningEfforts
	}
	if model.ReasoningContext == "" && spec.ReasoningContext != "" {
		model.ReasoningContext = spec.ReasoningContext
	}
	if spec.SupportsVision {
		model.SupportsVision = true
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
			cfg.Model.Proxy = spec.Proxy
			cfg.Model.MaxTokens = spec.MaxTokens
			slog.Info("Model override applied", "id", id, "provider", spec.Provider)
			return
		}
		// Not found in models.json — still strip the provider/ prefix so the
		// model ID is clean (e.g. "opencode/minimax-m2.5" → "minimax-m2.5").
		// Keep existing Provider/BaseURL/API from config as fallback.
		cfg.Model.ID = id
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
				cfg.Model.Proxy = spec.Proxy
				cfg.Model.MaxTokens = spec.MaxTokens
				slog.Info("Model override applied", "id", modelOverride, "provider", spec.Provider)
				return
			}
		}
	default: // n > 1
		slog.Warn("Model override: ambiguous model ID found in multiple providers, using raw ID with existing config. Use \"provider/id\" syntax to disambiguate.",
			"id", modelOverride, "matches", n)
	}
}
