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

	// Map level to reasoning_effort for the target provider. A model-declared
	// supported-efforts list takes precedence over the legacy per-provider
	// mapping: the declaration is authoritative wire-level truth.
	var effort string
	if len(model.ReasoningEfforts) > 0 {
		effort = clampEffort(model, level)
	} else {
		switch {
		case provider == "deepseek" && level == "xhigh":
			// DeepSeek only supports high/max.
			effort = "max"
		case provider == "deepseek":
			effort = "high"
		case provider != "zai" && level == "xhigh":
			// OpenAI-standard providers don't have xhigh.
			effort = "high"
		default:
			effort = level
		}
	}

	params := map[string]any{"reasoning_effort": effort}
	if useThinkingObj {
		params["thinking"] = map[string]string{"type": "enabled"}
	}
	return params
}

// effortRank ranks wire-level reasoning efforts by strength for nearest-match
// clamping. "xhigh" and "max" are synonyms at the top of the scale.
var effortRank = map[string]int{
	"minimal": 1,
	"low":     2,
	"medium":  3,
	"high":    4,
	"xhigh":   5,
	"max":     5,
}

// clampEffort returns the supported reasoning effort closest in strength to
// effort, using model.ReasoningEfforts as the allowed set. Ties resolve
// upward (more reasoning). When the model declares no efforts, or either the
// request or every declared value is unknown to the rank table, effort is
// returned unchanged so provider-specific values pass through untouched.
func clampEffort(model Model, effort string) string {
	requested, ok := effortRank[effort]
	if len(model.ReasoningEfforts) == 0 || !ok {
		return effort
	}
	best, bestRank, bestDist := "", -1, -1
	for _, candidate := range model.ReasoningEfforts {
		rank, ok := effortRank[candidate]
		if !ok {
			continue
		}
		dist := rank - requested
		if dist < 0 {
			dist = -dist
		}
		if bestDist == -1 || dist < bestDist || (dist == bestDist && rank > bestRank) {
			best, bestRank, bestDist = candidate, rank, dist
		}
	}
	if best == "" {
		return effort
	}
	return best
}
