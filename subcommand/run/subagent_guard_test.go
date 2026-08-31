package run

import (
	"strings"
	"testing"
)

func TestCheckSubagentSpawnAllowed(t *testing.T) {
	tests := []struct {
		name    string
		env     string
		allowed bool
	}{
		{name: "root", env: "", allowed: true},
		{name: "top-level agent", env: "0", allowed: true},
		{name: "child", env: "1", allowed: false},
		{name: "negative depth is invalid", env: "-1", allowed: false},
		{name: "invalid marker is still restricted", env: "unexpected", allowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSubagentSpawnAllowed(func(string) string { return tt.env })
			if (err == nil) != tt.allowed {
				t.Fatalf("allowed = %v, error = %v", tt.allowed, err)
			}
			if !tt.allowed && !strings.Contains(err.Error(), "nested subagents") {
				t.Fatalf("error = %q, want nested subagents message", err)
			}
		})
	}
}

func TestSubagentProcessEnvMarksChildAndReplacesMarker(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{name: "root becomes depth zero", in: []string{"PATH=/bin"}, want: subagentDepthEnv + "=0"},
		{name: "nested child increments depth", in: []string{"PATH=/bin", subagentDepthEnv + "=1"}, want: subagentDepthEnv + "=2"},
		{name: "duplicate markers collapse", in: []string{subagentDepthEnv + "=0", "PATH=/bin", subagentDepthEnv + "=1"}, want: subagentDepthEnv + "=1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := subagentProcessEnv(tt.in)
			var markers []string
			for _, entry := range env {
				if strings.HasPrefix(entry, subagentDepthEnv+"=") {
					markers = append(markers, entry)
				}
			}
			if len(markers) != 1 || markers[0] != tt.want {
				t.Fatalf("markers = %v, want [%s]", markers, tt.want)
			}
		})
	}
}
