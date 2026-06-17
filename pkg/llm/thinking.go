package llm

// supportsThinkingObject returns true for providers that accept the
// "thinking":{"type":"enabled/disabled"} object in their OpenAI-compatible
// request body (ZAI and DeepSeek).
func supportsThinkingObject(provider string) bool {
	return provider == "zai" || provider == "deepseek"
}

// buildThinkingParams returns thinking/reasoning parameters to inject into an
// OpenAI-compatible request body, or nil if none should be sent.
//
// level is expected to be pre-normalized to one of:
// off/minimal/low/medium/high/xhigh. An empty level means "no preference" — the
// model uses its default and no params are injected.
func buildThinkingParams(model Model, level string) map[string]any {
	if !model.Reasoning || level == "" {
		return nil
	}

	provider := model.Provider
	useThinkingObj := supportsThinkingObject(provider)

	// "off" — disable thinking entirely.
	if level == "off" {
		if useThinkingObj {
			return map[string]any{"thinking": map[string]string{"type": "disabled"}}
		}
		// No way to disable via API for this provider.
		return nil
	}

	// "minimal" — DeepSeek has no lightweight effort, disabling is closest.
	if level == "minimal" && provider == "deepseek" {
		return map[string]any{"thinking": map[string]string{"type": "disabled"}}
	}

	// Map level to reasoning_effort for the target provider.
	effort := level
	switch {
	case provider == "deepseek" && level == "xhigh":
		effort = "max"
	case provider == "deepseek":
		// DeepSeek only supports high/max.
		effort = "high"
	case provider != "zai" && level == "xhigh":
		// OpenAI-standard providers don't have xhigh.
		effort = "high"
	}

	params := map[string]any{"reasoning_effort": effort}
	if useThinkingObj {
		params["thinking"] = map[string]string{"type": "enabled"}
	}
	return params
}
