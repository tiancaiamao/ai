package app

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/compact"
)

func TestTruncateText(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{"empty limit", "hello", 0, ""},
		{"negative limit", "hello", -1, ""},
		{"no truncation", "hi", 10, "hi"},
		{"exact limit", "hello", 5, "hello"},
		{"limit 1", "hello", 1, "h"},
		{"limit 2", "hello", 2, "he"},
		{"limit 3", "hello", 3, "hel"},
		{"limit 4", "hello", 4, "h..."},
		{"limit 5 with ellipsis", "hello world", 8, "hello..."},
		{"empty string", "", 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateText(tt.text, tt.limit)
			if got != tt.want {
				t.Errorf("TruncateText(%q, %d) = %q, want %q", tt.text, tt.limit, got, tt.want)
			}
		})
	}
}

func TestFormatIntOrUnknown(t *testing.T) {
	tests := []struct {
		value int
		want  string
	}{
		{0, "unknown"},
		{-1, "unknown"},
		{1, "1"},
		{42, "42"},
		{1000, "1000"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := FormatIntOrUnknown(tt.value); got != tt.want {
				t.Errorf("FormatIntOrUnknown(%d) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestFormatLimit(t *testing.T) {
	tests := []struct {
		value int
		want  string
	}{
		{0, "disabled"},
		{-5, "disabled"},
		{10, "10"},
		{100, "100"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := FormatLimit(tt.value); got != tt.want {
				t.Errorf("FormatLimit(%d) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestFormatTokenLimit(t *testing.T) {
	tests := []struct {
		name  string
		state *compact.CompactionState
		want  string
	}{
		{"nil", nil, "unknown"},
		{"zero", &compact.CompactionState{}, "unknown"},
		{"no source", &compact.CompactionState{TokenLimit: 80000}, "80000"},
		{"with source", &compact.CompactionState{TokenLimit: 80000, TokenLimitSource: "context_window"}, "80000 (context-window)"},
		{"unknown source", &compact.CompactionState{TokenLimit: 100, TokenLimitSource: "something"}, "100 (something)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatTokenLimit(tt.state); got != tt.want {
				t.Errorf("FormatTokenLimit() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatTokenLimitSource(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{"context_window", "context-window"},
		{"max_tokens", "max-tokens"},
		{"none", ""},
		{"custom", "custom"},
		{"  context_window  ", "context-window"},
		{"CONTEXT_WINDOW", "context-window"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := FormatTokenLimitSource(tt.value); got != tt.want {
				t.Errorf("FormatTokenLimitSource(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}
