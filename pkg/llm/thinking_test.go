package llm

import (
	"reflect"
	"testing"
)

func TestBuildThinkingParams(t *testing.T) {
	zaiModel := Model{Provider: "zai", Reasoning: true}
	dsModel := Model{Provider: "deepseek", Reasoning: true}
	genericModel := Model{Provider: "openai", Reasoning: true}
	clampedModel := Model{Provider: "opencode", Reasoning: true, ReasoningEfforts: []string{"low", "high", "max"}}
	nonReasoning := Model{Provider: "zai", Reasoning: false}

	tests := []struct {
		name   string
		model  Model
		level  string
		expect map[string]any
	}{
		// Non-reasoning model — no params regardless of level.
		{"non-reasoning off", nonReasoning, "off", nil},
		{"non-reasoning high", nonReasoning, "high", nil},

		// Empty level — no params (let model use default).
		{"zai empty", zaiModel, "", nil},

		// ZAI: pass through all levels (server-side degradation handles them).
		{"zai off", zaiModel, "off", map[string]any{"thinking": map[string]string{"type": "disabled"}}},
		{"zai minimal", zaiModel, "minimal", map[string]any{
			"thinking":         map[string]string{"type": "enabled"},
			"reasoning_effort": "minimal",
		}},
		{"zai low", zaiModel, "low", map[string]any{
			"thinking":         map[string]string{"type": "enabled"},
			"reasoning_effort": "low",
		}},
		{"zai medium", zaiModel, "medium", map[string]any{
			"thinking":         map[string]string{"type": "enabled"},
			"reasoning_effort": "medium",
		}},
		{"zai high", zaiModel, "high", map[string]any{
			"thinking":         map[string]string{"type": "enabled"},
			"reasoning_effort": "high",
		}},
		{"zai xhigh", zaiModel, "xhigh", map[string]any{
			"thinking":         map[string]string{"type": "enabled"},
			"reasoning_effort": "xhigh",
		}},

		// Provider/model metadata controls supported values; supplied values are not translated.
		{"ds minimal", dsModel, "minimal", map[string]any{"thinking": map[string]string{"type": "enabled"}, "reasoning_effort": "minimal"}},
		{"ds low", dsModel, "low", map[string]any{"thinking": map[string]string{"type": "enabled"}, "reasoning_effort": "low"}},
		{"ds medium", dsModel, "medium", map[string]any{"thinking": map[string]string{"type": "enabled"}, "reasoning_effort": "medium"}},
		{"ds high", dsModel, "high", map[string]any{"thinking": map[string]string{"type": "enabled"}, "reasoning_effort": "high"}},
		{"ds xhigh", dsModel, "xhigh", map[string]any{"thinking": map[string]string{"type": "enabled"}, "reasoning_effort": "xhigh"}},
		{"native effort passthrough", Model{Provider: "openai", Reasoning: true, ReasoningEfforts: []string{"low", "high", "max"}}, "max", map[string]any{"reasoning_effort": "max"}},
		{"model default effort", Model{Provider: "openai", Reasoning: true, DefaultReasoningEffort: "balanced"}, "", map[string]any{"reasoning_effort": "balanced"}},

		// Generic OpenAI-compat: reasoning_effort only, no thinking object.
		{"generic off", genericModel, "off", nil},
		{"generic minimal", genericModel, "minimal", map[string]any{"reasoning_effort": "minimal"}},
		{"generic high", genericModel, "high", map[string]any{"reasoning_effort": "high"}},
		{"generic xhigh", genericModel, "xhigh", map[string]any{"reasoning_effort": "xhigh"}},

		// Native values are passed without strength-based substitution.
		{"native medium", clampedModel, "medium", map[string]any{"reasoning_effort": "medium"}},
		{"native minimal", clampedModel, "minimal", map[string]any{"reasoning_effort": "minimal"}},
		{"native low", clampedModel, "low", map[string]any{"reasoning_effort": "low"}},
		{"native high", clampedModel, "high", map[string]any{"reasoning_effort": "high"}},
		{"native xhigh", clampedModel, "xhigh", map[string]any{"reasoning_effort": "xhigh"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildThinkingParams(tt.model, tt.level)
			if !reflect.DeepEqual(got, tt.expect) {
				t.Errorf("buildThinkingParams(%+v, %q)\n  got:  %v\n  want: %v", tt.model, tt.level, got, tt.expect)
			}
		})
	}
}
