package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAutoCacheModeDetection verifies AS-3: IsCacheMode auto-detects the appropriate
// CacheMode from a model name.
func TestAutoCacheModeDetection(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		expected CacheMode
	}{
		{
			name:     "deepseek-chat returns CacheModeCache",
			model:    "deepseek-chat",
			expected: CacheModeCache,
		},
		{
			name:     "DeepSeek-Reasoner case-insensitive returns CacheModeCache",
			model:    "DeepSeek-Reasoner",
			expected: CacheModeCache,
		},
		{
			name:     "glm-4 returns CacheModeContext",
			model:    "glm-4",
			expected: CacheModeContext,
		},
		{
			name:     "empty string returns CacheModeContext",
			model:    "",
			expected: CacheModeContext,
		},
		{
			name:     "claude-3-opus returns CacheModeContext",
			model:    "claude-3-opus",
			expected: CacheModeContext,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := IsCacheMode(tc.model)
			assert.Equal(t, tc.expected, result, "IsCacheMode(%q)", tc.model)
		})
	}
}

// TestResolveCacheMode verifies that ResolveCacheMode correctly resolves Auto
// to a concrete mode while preserving explicit overrides.
func TestResolveCacheMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     CacheMode
		model    string
		expected CacheMode
	}{
		{
			name:     "Auto with deepseek resolves to Cache",
			mode:     CacheModeAuto,
			model:    "deepseek-chat",
			expected: CacheModeCache,
		},
		{
			name:     "Auto with glm-4 resolves to Context",
			mode:     CacheModeAuto,
			model:    "glm-4",
			expected: CacheModeContext,
		},
		{
			name:     "Explicit Cache overrides model detection",
			mode:     CacheModeCache,
			model:    "glm-4",
			expected: CacheModeCache,
		},
		{
			name:     "Explicit Context overrides model detection",
			mode:     CacheModeContext,
			model:    "deepseek-chat",
			expected: CacheModeContext,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ResolveCacheMode(tc.mode, tc.model)
			assert.Equal(t, tc.expected, result, "ResolveCacheMode(%v, %q)", tc.mode, tc.model)
		})
	}
}

// TestDefaultMutationPolicy verifies that DefaultMutationPolicy returns the correct
// RuntimeStateStrategy for each CacheMode.
func TestDefaultMutationPolicy(t *testing.T) {
	tests := []struct {
		name             string
		mode             CacheMode
		expectedStrategy RuntimeStateStrategy
	}{
		{
			name:             "CacheModeCache returns RuntimeStatePersist",
			mode:             CacheModeCache,
			expectedStrategy: RuntimeStatePersist,
		},
		{
			name:             "CacheModeContext returns RuntimeStateEphemeral",
			mode:             CacheModeContext,
			expectedStrategy: RuntimeStateEphemeral,
		},
		{
			name:             "CacheModeAuto fallback returns RuntimeStateEphemeral",
			mode:             CacheModeAuto,
			expectedStrategy: RuntimeStateEphemeral,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := DefaultMutationPolicy(tc.mode)
			assert.NotNil(t, policy, "DefaultMutationPolicy(%v) returned nil", tc.mode)
			assert.Equal(t, tc.expectedStrategy, policy.RuntimeStateStrategy(),
				"DefaultMutationPolicy(%v).RuntimeStateStrategy()", tc.mode)
		})
	}
}
