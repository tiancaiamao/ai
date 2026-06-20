package run

import (
	"testing"
	"time"
)

func TestBuildRPCFlags_ModelIncluded(t *testing.T) {
	flags := BuildRPCFlags("/tmp/session.json", "", 0, 0, "", "claude-sonnet-4-20250514", "")

	found := false
	for i, f := range flags {
		if f == "--model" && i+1 < len(flags) && flags[i+1] == "claude-sonnet-4-20250514" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --model claude-sonnet-4-20250514 in flags, got %v", flags)
	}
}

func TestBuildRPCFlags_ModelEmpty(t *testing.T) {
	flags := BuildRPCFlags("/tmp/session.json", "", 0, 0, "", "", "")

	for _, f := range flags {
		if f == "--model" {
			t.Errorf("expected --model to be absent, but found in flags: %v", flags)
		}
	}
}

func TestBuildRPCFlags_AllFlags(t *testing.T) {
	flags := BuildRPCFlags(
		"/tmp/session.json",
		"system prompt",
		10,
		5*time.Minute,
		":6060",
		"test-model",
		"abc123",
	)

	expected := map[string]string{
		"--session":       "/tmp/session.json",
		"--system-prompt": "system prompt",
		"--max-turns":     "10",
		"--timeout":       "5m0s",
		"--http":          ":6060",
		"--model":         "test-model",
		"--runid":         "abc123",
	}

	for key, want := range expected {
		found := false
		for i, f := range flags {
			if f == key && i+1 < len(flags) {
				if flags[i+1] != want {
					t.Errorf("flag %s: expected value %q, got %q", key, want, flags[i+1])
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected flag %s in result, got %v", key, flags)
		}
	}
}
