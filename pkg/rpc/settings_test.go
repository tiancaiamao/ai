package rpc

import (
	"testing"
)

func TestBuildSettingsResponse(t *testing.T) {
	t.Run("nil compaction", func(t *testing.T) {
		resp := BuildSettingsResponse(SettingsSnapshot{
			ModelID:        "gpt-4",
			ModelProvider:  "openai",
			ShowThinking:   true,
			ShowTools:      false,
			ShowPrefix:     true,
			ThinkingLevel:  "high",
			BusyMode:       "steer",
			AutoCompaction: true,
		})
		data := resp["data"].(map[string]any)
		if data["model"] != "openai/gpt-4" {
			t.Errorf("model = %v, want openai/gpt-4", data["model"])
		}
		if data["show-thinking"] != "on" {
			t.Errorf("show-thinking = %v, want on", data["show-thinking"])
		}
		if data["tools"] != "off" {
			t.Errorf("tools = %v, want off", data["tools"])
		}
		if data["compaction-context-window"] != "unknown" {
			t.Errorf("compaction-context-window = %v, want unknown", data["compaction-context-window"])
		}
	})

	t.Run("with compaction", func(t *testing.T) {
		resp := BuildSettingsResponse(SettingsSnapshot{
			ModelID: "claude",
			Compaction: &CompactionState{
				ContextWindow:    128000,
				ReserveTokens:    4096,
				MaxMessages:      50,
				MaxTokens:        100000,
				KeepRecent:       10,
				KeepRecentTokens: 5000,
				TokenLimit:       8000,
				TokenLimitSource: "context_window",
			},
		})
		data := resp["data"].(map[string]any)
		if data["compaction-context-window"] != "128000" {
			t.Errorf("compaction-context-window = %v, want 128000", data["compaction-context-window"])
		}
		if data["compaction-max-messages"] != "50" {
			t.Errorf("compaction-max-messages = %v, want 50", data["compaction-max-messages"])
		}
		if data["compaction-token-limit"] != "8000 (context-window)" {
			t.Errorf("compaction-token-limit = %v, want 8000 (context-window)", data["compaction-token-limit"])
		}
	})

	t.Run("no provider", func(t *testing.T) {
		resp := BuildSettingsResponse(SettingsSnapshot{ModelID: "gpt-4"})
		data := resp["data"].(map[string]any)
		if data["model"] != "gpt-4" {
			t.Errorf("model = %v, want gpt-4", data["model"])
		}
	})
}

func TestSetUsage(t *testing.T) {
	usage := SetUsage()
	if usage["usage"] != "/set <key> [value]" {
		t.Errorf("usage = %v, want /set <key> [value]", usage["usage"])
	}
	settings := usage["settings"].([]string)
	if len(settings) < 10 {
		t.Errorf("expected at least 10 settings, got %d", len(settings))
	}
}

func TestParseToggleValue(t *testing.T) {
	tests := []struct {
		value   string
		current bool
		wantVal bool
		wantChg bool
	}{
		{"on", false, true, true},
		{"off", true, false, true},
		{"toggle", false, true, true},
		{"toggle", true, false, true},
		{"", false, true, true},
		{"", true, false, true},
		{"garbage", false, false, false},
	}
	for _, tt := range tests {
		got := ParseToggleValue(tt.value, tt.current)
		if got.Value != tt.wantVal {
			t.Errorf("ParseToggleValue(%q, %v).Value = %v, want %v", tt.value, tt.current, got.Value, tt.wantVal)
		}
		if got.Changed != tt.wantChg {
			t.Errorf("ParseToggleValue(%q, %v).Changed = %v, want %v", tt.value, tt.current, got.Changed, tt.wantChg)
		}
	}
}

func TestParseBoolFromInput(t *testing.T) {
	tests := []struct {
		value string
		key   string
		want  bool
	}{
		{"true", "", true},
		{"1", "", true},
		{"false", "", false},
		{"0", "", false},
		{`{"enabled": true}`, "enabled", true},
		{`{"enabled": false}`, "enabled", false},
		{`{"enabled": true}`, "other", false},
		{"anything", "", false},
	}
	for _, tt := range tests {
		got := ParseBoolFromInput(tt.value, tt.key)
		if got != tt.want {
			t.Errorf("ParseBoolFromInput(%q, %q) = %v, want %v", tt.value, tt.key, got, tt.want)
		}
	}
}

func TestParseModeFromInput(t *testing.T) {
	valid := map[string]bool{"all": true, "immediate": true, "one-at-a-time": true}
	tests := []struct {
		value string
		key   string
		want  string
		err   bool
	}{
		{"all", "mode", "all", false},
		{"IMMEDIATE", "mode", "immediate", false},
		{`{"mode": "one-at-a-time"}`, "mode", "one-at-a-time", false},
		{"invalid", "mode", "", true},
		{`{"mode": "bad"}`, "mode", "", true},
	}
	for _, tt := range tests {
		got, err := ParseModeFromInput(tt.value, tt.key, valid)
		if tt.err && err == nil {
			t.Errorf("ParseModeFromInput(%q) expected error, got nil", tt.value)
		}
		if !tt.err && err != nil {
			t.Errorf("ParseModeFromInput(%q) unexpected error: %v", tt.value, err)
		}
		if !tt.err && got != tt.want {
			t.Errorf("ParseModeFromInput(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
}
