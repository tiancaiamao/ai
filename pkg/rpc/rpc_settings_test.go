package rpc

import (
	"testing"
)

func TestParseToggleValue(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		current bool
		want    ToggleResult
	}{
		{"on", "on", false, ToggleResult{Value: true, Changed: true}},
		{"on already", "on", true, ToggleResult{Value: true, Changed: true}},
		{"off", "off", true, ToggleResult{Value: false, Changed: true}},
		{"off already", "off", false, ToggleResult{Value: false, Changed: true}},
		{"toggle from false", "toggle", false, ToggleResult{Value: true, Changed: true}},
		{"toggle from true", "toggle", true, ToggleResult{Value: false, Changed: true}},
		{"empty toggles", "", false, ToggleResult{Value: true, Changed: true}},
		{"invalid", "maybe", false, ToggleResult{Changed: false}},
		{"whitespace on", "  on  ", false, ToggleResult{Value: true, Changed: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseToggleValue(tt.value, tt.current)
			if got != tt.want {
				t.Errorf("ParseToggleValue(%q, %v) = %+v, want %+v", tt.value, tt.current, got, tt.want)
			}
		})
	}
}

func TestParseModeFromInput(t *testing.T) {
	valid := map[string]bool{"auto": true, "manual": true, "off": true}
	tests := []struct {
		name    string
		value   string
		jsonKey string
		valid   map[string]bool
		want    string
		wantErr bool
	}{
		{"plain valid", "auto", "mode", valid, "auto", false},
		{"plain invalid", "bogus", "mode", valid, "", true},
		{"json valid", `{"mode":"manual"}`, "mode", valid, "manual", false},
		{"json invalid", `{"mode":"nope"}`, "mode", valid, "", true},
		{"json wrong key", `{"other":"auto"}`, "mode", valid, "", true},
		{"empty", "", "mode", valid, "", true},
		{"whitespace", "  auto  ", "mode", valid, "auto", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseModeFromInput(tt.value, tt.jsonKey, tt.valid)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseModeFromInput(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseModeFromInput(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseBoolFromInput(t *testing.T) {
	tests := []struct {
		value   string
		jsonKey string
		want    bool
	}{
		{"true", "flag", true},
		{"1", "flag", true},
		{"on", "flag", true},
		{"ON", "flag", true},
		{"false", "flag", false},
		{"0", "flag", false},
		{"off", "flag", false},
		{"yes", "flag", false},
		{`{"flag":true}`, "flag", true},
		{`{"flag":false}`, "flag", false},
		{`{"other":true}`, "flag", false},
		{"TRUE", "flag", true},
		{"", "flag", false},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := ParseBoolFromInput(tt.value, tt.jsonKey); got != tt.want {
				t.Errorf("ParseBoolFromInput(%q, %q) = %v, want %v", tt.value, tt.jsonKey, got, tt.want)
			}
		})
	}
}
