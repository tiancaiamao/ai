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

		// DeepSeek: only high/max supported; minimal→disabled.
		{"ds off", dsModel, "off", map[string]any{"thinking": map[string]string{"type": "disabled"}}},
		{"ds minimal", dsModel, "minimal", map[string]any{"thinking": map[string]string{"type": "disabled"}}},
		{"ds low", dsModel, "low", map[string]any{
			"thinking":         map[string]string{"type": "enabled"},
			"reasoning_effort": "high",
		}},
		{"ds medium", dsModel, "medium", map[string]any{
			"thinking":         map[string]string{"type": "enabled"},
			"reasoning_effort": "high",
		}},
		{"ds high", dsModel, "high", map[string]any{
			"thinking":         map[string]string{"type": "enabled"},
			"reasoning_effort": "high",
		}},
		{"ds xhigh", dsModel, "xhigh", map[string]any{
			"thinking":         map[string]string{"type": "enabled"},
			"reasoning_effort": "max",
		}},

		// Generic OpenAI-compat: reasoning_effort only, no thinking object.
		{"generic off", genericModel, "off", nil},
		{"generic minimal", genericModel, "minimal", map[string]any{"reasoning_effort": "minimal"}},
		{"generic high", genericModel, "high", map[string]any{"reasoning_effort": "high"}},
		{"generic xhigh", genericModel, "xhigh", map[string]any{"reasoning_effort": "high"}},

		// Model-declared supported efforts (e.g. ox-alpha-free: low/high/max,
		// always thinking) — unsupported levels are clamped to the nearest
		// supported value, ties resolving upward.
		{"clamped medium→high", clampedModel, "medium", map[string]any{"reasoning_effort": "high"}},
		{"clamped minimal→low", clampedModel, "minimal", map[string]any{"reasoning_effort": "low"}},
		{"clamped low passthrough", clampedModel, "low", map[string]any{"reasoning_effort": "low"}},
		{"clamped high passthrough", clampedModel, "high", map[string]any{"reasoning_effort": "high"}},
		{"clamped xhigh→max", clampedModel, "xhigh", map[string]any{"reasoning_effort": "max"}},
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
