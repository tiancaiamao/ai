package model

import (
	"testing"
)

func TestCapability_Has(t *testing.T) {
	tests := []struct {
		name     string
		cap      Capability
		check    Capability
		expected bool
	}{
		{
			name:     "text capability has text",
			cap:      CapabilityText,
			check:    CapabilityText,
			expected: true,
		},
		{
			name:     "vision capability has vision",
			cap:      CapabilityVision,
			check:    CapabilityVision,
			expected: true,
		},
		{
			name:     "vision capability does not have function calling",
			cap:      CapabilityVision,
			check:    CapabilityFunctionCalling,
			expected: false,
		},
		{
			name:     "combined capability has all",
			cap:      CapabilityText | CapabilityVision | CapabilityFunctionCalling,
			check:    CapabilityVision,
			expected: true,
		},
		{
			name:     "combined capability has all - function calling",
			cap:      CapabilityText | CapabilityVision | CapabilityFunctionCalling,
			check:    CapabilityFunctionCalling,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cap.Has(tt.check); got != tt.expected {
				t.Errorf("Capability.Has() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCapability_SupportsVision(t *testing.T) {
	tests := []struct {
		name     string
		cap      Capability
		expected bool
	}{
		{
			name:     "text only does not support vision",
			cap:      CapabilityText,
			expected: false,
		},
		{
			name:     "vision supports vision",
			cap:      CapabilityVision,
			expected: true,
		},
		{
			name:     "combined supports vision",
			cap:      CapabilityText | CapabilityVision,
			expected: true,
		},
		{
			name:     "function calling only does not support vision",
			cap:      CapabilityFunctionCalling,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cap.SupportsVision(); got != tt.expected {
				t.Errorf("Capability.SupportsVision() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCapability_SupportsFunctionCalling(t *testing.T) {
	tests := []struct {
		name     string
		cap      Capability
		expected bool
	}{
		{
			name:     "text only does not support function calling",
			cap:      CapabilityText,
			expected: false,
		},
		{
			name:     "function calling supports function calling",
			cap:      CapabilityFunctionCalling,
			expected: true,
		},
		{
			name:     "combined supports function calling",
			cap:      CapabilityText | CapabilityFunctionCalling,
			expected: true,
		},
		{
			name:     "vision only does not support function calling",
			cap:      CapabilityVision,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cap.SupportsFunctionCalling(); got != tt.expected {
				t.Errorf("Capability.SupportsFunctionCalling() = %v, want %v", got, tt.expected)
			}
		})
	}
}
