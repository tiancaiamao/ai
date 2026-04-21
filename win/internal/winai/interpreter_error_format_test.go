package winai

import "testing"

func TestFormatAgentErrorStatus(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		mustHas string
	}{
		{
			name:    "rate_limit",
			input:   "API error (429): Rate limit reached for requests",
			mustHas: "request failed (rate limit)",
		},
		{
			name:    "authentication",
			input:   "API error (401): unauthorized",
			mustHas: "request failed (authentication)",
		},
		{
			name:    "permission",
			input:   "API error (403): forbidden",
			mustHas: "request failed (permission)",
		},
		{
			name:    "context_limit",
			input:   "context length exceeded: maximum context length exceeded",
			mustHas: "request failed (context limit)",
		},
		{
			name:    "timeout",
			input:   "tool execution timeout after 30s",
			mustHas: "request failed (timeout)",
		},
		{
			name:    "network",
			input:   "DNS error: cannot resolve API host",
			mustHas: "request failed (network)",
		},
		{
			name:    "server_5xx",
			input:   "API error (503): service unavailable",
			mustHas: "request failed (server)",
		},
		{
			name:    "default",
			input:   "some random failure",
			mustHas: "request failed: some random failure",
		},
		{
			name:    "empty",
			input:   "",
			mustHas: "request failed: unknown error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAgentErrorStatus(tt.input)
			if !containsAny(got, tt.mustHas) {
				t.Fatalf("expected %q to contain %q", got, tt.mustHas)
			}
		})
	}
}
