package rpc

import "testing"

func TestTruncateText(t *testing.T) {
	tests := []struct {
		text  string
		limit int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"hi", 2, "hi"},
		{"hello", 0, ""},
		{"hello", -1, ""},
		{"hello", 3, "hel"},
		{"hello", 4, "h..."},
	}
	for _, tt := range tests {
		got := TruncateText(tt.text, tt.limit)
		if got != tt.want {
			t.Errorf("TruncateText(%q, %d) = %q, want %q", tt.text, tt.limit, got, tt.want)
		}
	}
}

func TestFormatIntOrUnknown(t *testing.T) {
	tests := []struct {
		value int
		want  string
	}{
		{0, "unknown"},
		{-1, "unknown"},
		{42, "42"},
		{1, "1"},
	}
	for _, tt := range tests {
		got := FormatIntOrUnknown(tt.value)
		if got != tt.want {
			t.Errorf("FormatIntOrUnknown(%d) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestFormatLimit(t *testing.T) {
	tests := []struct {
		value int
		want  string
	}{
		{0, "disabled"},
		{-1, "disabled"},
		{100, "100"},
	}
	for _, tt := range tests {
		got := FormatLimit(tt.value)
		if got != tt.want {
			t.Errorf("FormatLimit(%d) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestFormatTokenLimit(t *testing.T) {
	tests := []struct {
		name  string
		state *CompactionState
		want  string
	}{
		{"nil", nil, "unknown"},
		{"zero limit", &CompactionState{TokenLimit: 0}, "unknown"},
		{"no source", &CompactionState{TokenLimit: 5000}, "5000"},
		{"with source", &CompactionState{TokenLimit: 8000, TokenLimitSource: "context_window"}, "8000 (context-window)"},
		{"none source", &CompactionState{TokenLimit: 8000, TokenLimitSource: "none"}, "8000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatTokenLimit(tt.state)
			if got != tt.want {
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
		{"  CONTEXT_WINDOW  ", "context-window"},
		{"", ""},
	}
	for _, tt := range tests {
		got := FormatTokenLimitSource(tt.value)
		if got != tt.want {
			t.Errorf("FormatTokenLimitSource(%q) = %q, want %q", tt.value, got, tt.want)
		}
	}
}
