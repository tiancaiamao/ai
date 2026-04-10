package main

import (
	"fmt"

	"github.com/tiancaiamao/ai/pkg/config"
)

// resolveModelSpec looks up a model spec from models.json.
func resolveModelSpec(provider, modelID string) (config.ModelSpec, error) {
	modelsPath, err := config.ResolveModelsPath()
	if err != nil {
		return config.ModelSpec{}, fmt.Errorf("resolve models path: %w", err)
	}

	specs, err := config.LoadModelSpecs(modelsPath)
	if err != nil {
		return config.ModelSpec{}, fmt.Errorf("load models: %w", err)
	}

	for _, spec := range specs {
		if spec.Provider == provider && spec.ID == modelID {
			return spec, nil
		}
	}

	return config.ModelSpec{}, fmt.Errorf("model not found: %s/%s", provider, modelID)
}