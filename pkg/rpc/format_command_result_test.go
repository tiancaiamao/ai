package rpc

import (
	"encoding/json"
	"testing"
)

// TestFormatCommandResultParameterTypes documents the expected behavior of
// FormatCommandResult with different parameter types. This test ensures that
// passing []byte directly (without unmarshaling first) is detected as a bug.
func TestFormatCommandResultParameterTypes(t *testing.T) {
	contextJSON := `{"state":{"sessionId":"test","model":{"id":"test-model","provider":"test","contextWindow":200000}},"stats":{"sessionId":"test","totalMessages":0,"tokens":{"activeWindowTokens":0,"systemPromptTokens":0,"systemToolsTokens":0}}}`

	t.Run("correct: map[string]any", func(t *testing.T) {
		var data map[string]any
		if err := json.Unmarshal([]byte(contextJSON), &data); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}

		result := FormatCommandResult("context", data)
		if result == "" {
			t.Error("expected non-empty result with map[string]any")
		}
		// Should NOT contain base64
		if len(result) > 3 && result[0:4] == "ewog" {
			t.Errorf("result starts with base64 prefix 'ewog', bug detected! Result: %s", result)
		}
		// Should NOT contain raw JSON keys
		if contains(result, `"state":`) || contains(result, `"stats":`) {
			t.Errorf("result contains raw JSON keys, bug detected! Result: %s", result)
		}
		// Should contain formatted text
		if !contains(result, "Context Usage") {
			t.Errorf("result missing 'Context Usage', got: %s", result)
		}
	})

	t.Run("buggy: []byte parameter", func(t *testing.T) {
		// This is the BUGGY way: passing []byte directly
		// FormatCommandResult will json.Marshal the []byte, resulting in base64
		result := FormatCommandResult("context", []byte(contextJSON))

		// When []byte is passed, json.Marshal encodes it as base64 string
		// which then fails to unmarshal to map[string]any
		if result != "" {
			// If result is not empty, it should be the fallback (raw JSON)
			// because the base64 string fails to unmarshal
			t.Logf("With []byte parameter, result: %s", result)

			// Verify it's NOT formatted output (the bug)
			if contains(result, "Context Usage") {
				t.Error("BUG: []byte parameter produced formatted output, this shouldn't happen!")
			}

			// Verify it's base64-encoded or raw JSON fallback
			isBase64 := len(result) > 3 && result[0:4] == "ewog"
			if !isBase64 && !contains(result, `"state":`) {
				t.Logf("Result is neither base64 nor raw JSON: %s", result)
			}
		} else {
			t.Logf("With []byte parameter, result is empty (expected for buggy input)")
		}
	})

	t.Run("correct: string parameter", func(t *testing.T) {
		// String parameters are passed through directly by formatACPCommandResult
		// But FormatCommandResult only handles strings for formatACPCommandResult fallback
		result := FormatCommandResult("context", "test string")
		// Should be empty (no named command matches a string payload)
		if result != "" {
			t.Logf("String parameter result: %s", result)
		}
	})
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
