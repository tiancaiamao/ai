package rpc

// Model catalog reporting and switching over ACP.
//
// ACP hosts (AionUi via aioncore, Zed, agent-shell) render a model selector
// from the session/new / set_config handshake payloads (aioncore captures
// them into agent_metadata.handshake). We advertise a single config option —
// category "model", type "select" — built from the same model registry the
// RPC slash commands use (loadModelSpecs + filterModelSpecsWithKeys), so the
// dropdown matches /model exactly.
//
// The wire emits BOTH spellings of every field for client compatibility:
//   - result.configOptions  (official ACP v1 field, required by the schema)
//   - result.config_options (snake_case spelling some hosts read instead)
//   - option.currentValue   (spec) alongside option.current_value (host)
//
// Switching accepts the official session/set_config_option method plus three
// defensive aliases, with lenient param parsing (see parseSetConfigParams).

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tiancaiamao/ai/pkg/config"
)

// acpConfigOptionID is the id of the model selector we advertise.
const acpConfigOptionID = "model"

// acpSelectOption is one entry of a select config option's dropdown.
type acpSelectOption struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// acpConfigOption is a session configuration option selector (ACP spec:
// SessionConfigOption). currentValue is emitted twice under both the spec
// camelCase key and the snake_case key hosts have been observed reading.
type acpConfigOption struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Category          string            `json:"category,omitempty"`
	Type              string            `json:"type"`
	CurrentValue      string            `json:"currentValue,omitempty"`
	CurrentValueSnake string            `json:"current_value,omitempty"`
	Options           []acpSelectOption `json:"options,omitempty"`
}

// acpModelCatalog builds the "model" select config option from the shared
// model registry: only models whose provider has an API key are listed
// (identical to what /model offers). Returns nil when nothing can be offered,
// in which case callers simply omit config options from their payload.
func (app *rpcApp) acpModelCatalog() []acpConfigOption {
	specs, modelsPath, err := loadModelSpecs(app.cfg)
	if err != nil {
		slog.Warn("[ACP] failed to load model registry, omitting model selector",
			"error", err, "modelsPath", modelsPath)
		return nil
	}
	filtered := filterModelSpecsWithKeys(specs)

	current := app.model.Provider + "/" + app.model.ID
	options := make([]acpSelectOption, 0, len(filtered)+1)
	hasCurrent := false
	for _, spec := range filtered {
		value := spec.Provider + "/" + spec.ID
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			name = spec.ID
		}
		if value == current {
			hasCurrent = true
		}
		options = append(options, acpSelectOption{Value: value, Name: name})
	}
	// The active model may be outside the registry (config fallback when
	// models.json is missing): still offer it so currentValue always resolves.
	if !hasCurrent && app.model.ID != "" {
		options = append(options, acpSelectOption{
			Value:       current,
			Name:        app.model.ID,
			Description: "active model",
		})
	}
	if len(options) == 0 {
		return nil
	}

	return []acpConfigOption{{
		ID:                acpConfigOptionID,
		Name:              "Model",
		Category:          "model",
		Type:              "select",
		CurrentValue:      current,
		CurrentValueSnake: current,
		Options:           options,
	}}
}

// handleSetConfig switches the active model on behalf of an ACP host.
// Accepts multiple method aliases (mapped in handleRequest) and tolerant
// params shapes; see parseSetConfigParams. The response carries the updated
// catalog under both configOptions (spec-required) and config_options, plus
// a _meta mirror for capture-style clients like aioncore.
func (s *acpServer) handleSetConfig(req acpRequest) {
	optionID, value, ok := parseSetConfigParams(req.Params)
	if !ok {
		s.sendError(req.ID, acpErrInvalidParams,
			fmt.Sprintf("invalid %s params: need a config option id and value", req.Method))
		return
	}
	if !strings.EqualFold(strings.TrimSpace(optionID), acpConfigOptionID) {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("unknown config option: %q", optionID))
		return
	}

	provider, modelID, err := s.app.resolveModelOptionValue(value)
	if err != nil {
		s.sendError(req.ID, acpErrInvalidParams, err.Error())
		return
	}

	args, err := json.Marshal(map[string]string{"provider": provider, "modelId": modelID})
	if err != nil {
		s.sendError(req.ID, acpErrInvalidRequest, fmt.Sprintf("encode set_model args: %v", err))
		return
	}
	// Single source of truth for switching: the same path /set_model uses
	// (spec validation, compactor rebuild, config persistence).
	if _, err := s.app.handleModelSet(string(args)); err != nil {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("set model %s/%s: %v", provider, modelID, err))
		return
	}
	slog.Info("[ACP] model switched by host", "provider", provider, "modelId", modelID)

	catalog := s.app.acpModelCatalog()
	s.sendResult(req.ID, map[string]any{
		"configOptions":  catalog,
		"config_options": catalog,
		"_meta":          map[string]any{"config_options": catalog},
	})
}

// resolveModelOptionValue maps a dropdown value ("provider/id" or bare
// "id") to (provider, modelID). Only keyed models are accepted; bare ids
// match the first keyed spec with that id. Unkeyed-but-existing models get a
// dedicated error so users know to configure credentials.
func (app *rpcApp) resolveModelOptionValue(value string) (provider, modelID string, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", fmt.Errorf("empty model value")
	}
	specs, modelsPath, err := loadModelSpecs(app.cfg)
	if err != nil {
		return "", "", fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	filtered := filterModelSpecsWithKeys(specs)

	if p, id, hasSlash := strings.Cut(value, "/"); hasSlash {
		if spec, ok := findModelSpec(filtered, p, id); ok {
			return spec.Provider, spec.ID, nil
		}
		if _, exists := findModelSpec(specs, p, id); exists {
			authPath, _ := config.GetDefaultAuthPath()

			envVar := strings.ToUpper(p) + "_API_KEY"
			return "", "", fmt.Errorf("no API key for %q (set %s or update %s)", p, envVar, authPath)
		}
	} else {
		for _, spec := range filtered {
			if spec.ID == value {
				return spec.Provider, spec.ID, nil
			}
		}
	}
	return "", "", fmt.Errorf("unknown model: %s", value)
}

// parseSetConfigParams extracts (optionID, value) from set-config request
// params. Hosts differ on both nesting and field names, so both are probed:
//
//	flat:            {"configId": "model", "value": "..."}      (ACP v1 shape)
//	field variants:  option_id | id | config_option_id | configId | optionId
//	                 value | model_id | new_value | newValue | modelId | model
//	nested one level {"configuration": {id/value}, "config": {...}, ...}
//
// Returns ok=false when either piece cannot be found as a non-empty scalar.
func parseSetConfigParams(params json.RawMessage) (optionID, value string, ok bool) {
	var m map[string]any
	if err := json.Unmarshal(params, &m); err != nil || m == nil {
		return "", "", false
	}
	idKeys := []string{"option_id", "id", "config_option_id", "configId", "optionId"}
	valKeys := []string{"value", "model_id", "new_value", "newValue", "modelId", "model"}

	var foundID, foundVal bool
	optionID, foundID = findStringKey(m, idKeys)
	value, foundVal = findStringKey(m, valKeys)
	if foundID && foundVal {
		return optionID, value, true
	}
	// One level of nesting under common wrapper keys.
	for _, wrapper := range []string{"configuration", "config", "params", "setting", "option"} {
		sub, isMap := m[wrapper].(map[string]any)
		if !isMap {
			continue
		}
		if !foundID {
			optionID, foundID = findStringKey(sub, idKeys)
		}
		if !foundVal {
			value, foundVal = findStringKey(sub, valKeys)
		}
		if foundID && foundVal {
			return optionID, value, true
		}
	}
	return "", "", false
}

// findStringKey returns the first key present in scope whose value is a
// non-empty scalar (string/number/bool are stringified).
func findStringKey(scope map[string]any, keys []string) (string, bool) {
	for _, k := range keys {
		v, exists := scope[k]
		if !exists {
			continue
		}
		switch s := v.(type) {
		case string:
			if strings.TrimSpace(s) != "" {
				return s, true
			}
		case float64:
			return fmt.Sprintf("%v", s), true
		case bool:
			return fmt.Sprintf("%v", s), true
		}
	}
	return "", false
}
