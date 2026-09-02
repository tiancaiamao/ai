package protocol

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tiancaiamao/ai/pkg/config"
)

const acpConfigOptionID = "model"

type acpSelectOption struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type acpConfigOption struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Category          string            `json:"category,omitempty"`
	Type              string            `json:"type"`
	CurrentValue      string            `json:"currentValue,omitempty"`
	CurrentValueSnake string            `json:"current_value,omitempty"`
	Options           []acpSelectOption `json:"options,omitempty"`
}

func acpModelCatalog(runtime Runtime) []acpConfigOption {
	specs, current, err := runtime.ModelCatalog()
	if err != nil {
		slog.Warn("[ACP] failed to load model registry, omitting model selector", "error", err)
		return nil
	}
	currentValue := current.Provider + "/" + current.ID
	options := make([]acpSelectOption, 0, len(specs)+1)
	hasCurrent := false
	for _, spec := range specs {
		value := spec.Provider + "/" + spec.ID
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			name = spec.ID
		}
		if value == currentValue {
			hasCurrent = true
		}
		options = append(options, acpSelectOption{Value: value, Name: name})
	}
	if !hasCurrent && current.ID != "" {
		options = append(options, acpSelectOption{Value: currentValue, Name: current.ID, Description: "active model"})
	}
	if len(options) == 0 {
		return nil
	}
	return []acpConfigOption{{ID: acpConfigOptionID, Name: "Model", Category: "model", Type: "select", CurrentValue: currentValue, CurrentValueSnake: currentValue, Options: options}}
}

func acpResultWithCatalog(result map[string]any, catalog []acpConfigOption) map[string]any {
	if len(catalog) > 0 {
		result["configOptions"] = catalog
		result["config_options"] = catalog
		result["_meta"] = map[string]any{"config_options": catalog}
	}
	return result
}

func (s *acpServer) handleSetConfig(req acpRequest) {
	optionID, value, ok := parseSetConfigParams(req.Params)
	if !ok {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("invalid %s params: need a config option id and value", req.Method))
		return
	}
	if !strings.EqualFold(strings.TrimSpace(optionID), acpConfigOptionID) {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("unknown config option: %q", optionID))
		return
	}
	provider, modelID, err := s.app.ResolveModelOption(value)
	if err != nil {
		s.sendError(req.ID, acpErrInvalidParams, err.Error())
		return
	}
	if err := s.app.SetModel(provider, modelID); err != nil {
		s.sendError(req.ID, acpErrInvalidParams, fmt.Sprintf("set model %s/%s: %v", provider, modelID, err))
		return
	}
	slog.Info("[ACP] model switched by host", "provider", provider, "modelId", modelID)
	s.sendResult(req.ID, acpResultWithCatalog(map[string]any{}, acpModelCatalog(s.app)))
}

func parseSetConfigParams(params json.RawMessage) (optionID, value string, ok bool) {
	var m map[string]any
	if err := json.Unmarshal(params, &m); err != nil || m == nil {
		return "", "", false
	}
	idKeys := []string{"option_id", "id", "config_option_id", "configId", "optionId"}
	valKeys := []string{"value", "model_id", "new_value", "newValue", "modelId", "model"}
	optionID, foundID := findStringKey(m, idKeys)
	value, foundVal := findStringKey(m, valKeys)
	if foundID && foundVal {
		return optionID, value, true
	}
	for _, wrapper := range []string{"configuration", "config", "params", "setting", "option"} {
		sub, isMap := m[wrapper].(map[string]any)
		if !isMap {
			continue
		}
		optionID, foundID = findStringKey(sub, idKeys)
		value, foundVal = findStringKey(sub, valKeys)
		if foundID && foundVal {
			return optionID, value, true
		}
	}
	return "", "", false
}

func findStringKey(m map[string]any, keys []string) (string, bool) {
	for _, key := range keys {
		if value, ok := m[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

var _ config.ModelSpec
