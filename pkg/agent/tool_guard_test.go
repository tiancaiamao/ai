package agent

import (
	"testing"
)

func TestIsSuccessfulStopReason(t *testing.T) {
	tests := []struct {
		reason   string
		expected bool
	}{
		{"stop", true},
		{"tool_calls", true},
		{"toolUse", true},
		{"length", true},
		{"", false}, // empty stopReason means incomplete response
		{"error", false},
		{"network_error", false},
		{"timeout", false},
		{"rate_limit", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := isSuccessfulStopReason(tt.reason)
			if got != tt.expected {
				t.Errorf("isSuccessfulStopReason(%q) = %v, want %v", tt.reason, got, tt.expected)
			}
		})
	}
}