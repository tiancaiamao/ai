package llm

// supportsThinkingObject returns true for providers that accept the
// "thinking":{"type":"enabled/disabled"} object in their OpenAI-compatible
// request body (ZAI and DeepSeek).
func supportsThinkingObject(provider string) bool {
	return provider == "zai" || provider == "deepseek"
}

// buildThinkingParams forwards a model-native reasoning effort unchanged.
// Empty means use the model's configured default, or the provider default if
// the model does not declare one.
func buildThinkingParams(model Model, effort string) map[string]any {
	if !model.Reasoning {
		return nil
	}
	if effort == "" {
		effort = model.DefaultReasoningEffort
	}
	if effort == "" {
		return nil
	}

	if effort == "off" && !containsEffort(model.ReasoningEfforts, effort) {
		if supportsThinkingObject(model.Provider) {
			return map[string]any{"thinking": map[string]string{"type": "disabled"}}
		}
		return nil
	}

	params := map[string]any{"reasoning_effort": effort}
	if supportsThinkingObject(model.Provider) {
		params["thinking"] = map[string]string{"type": "enabled"}
	}
	return params
}

func containsEffort(efforts []string, effort string) bool {
	for _, candidate := range efforts {
		if candidate == effort {
			return true
		}
	}
	return false
}
