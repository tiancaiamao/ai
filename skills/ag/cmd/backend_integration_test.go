package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/storage"
)

func TestLsIncludesBackend(t *testing.T) {
	dir := t.TempDir()
	origBase := storage.BaseDir
	storage.BaseDir = filepath.Join(dir, ".ag")
	defer func() { storage.BaseDir = origBase }()

	// Create two agents with different backends
	for _, tc := range []struct {
		id      string
		backend string
	}{
		{"agent-ai", "ai"},
		{"agent-codex", "codex"},
	} {
		agentDir := agent.AgentDir(tc.id)
		if err := os.MkdirAll(agentDir, 0755); err != nil {
			t.Fatal(err)
		}
		activity := map[string]any{
			"status":    "done",
			"backend":   tc.backend,
			"startedAt": time.Now().Unix(),
		}
		data, _ := json.Marshal(activity)
		if err := os.WriteFile(filepath.Join(agentDir, "activity.json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	agents, err := agent.List()
	if err != nil {
		t.Fatal(err)
	}

	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}

	backends := map[string]string{}
	for _, a := range agents {
		backends[a.ID] = a.Backend
	}
	if backends["agent-ai"] != "ai" {
		t.Errorf("agent-ai backend = %q, want %q", backends["agent-ai"], "ai")
	}
	if backends["agent-codex"] != "codex" {
		t.Errorf("agent-codex backend = %q, want %q", backends["agent-codex"], "codex")
	}
}

func TestStatusShowsBackend(t *testing.T) {
	dir := t.TempDir()
	origBase := storage.BaseDir
	storage.BaseDir = filepath.Join(dir, ".ag")
	defer func() { storage.BaseDir = origBase }()

	agentDir := agent.AgentDir("test-status")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	activity := map[string]any{
		"status":      "done",
		"backend":     "codex",
		"startedAt":   time.Now().Unix() - 10,
		"finishedAt":  time.Now().Unix(),
		"turns":       5,
		"tokensTotal": 1000,
	}
	data, _ := json.Marshal(activity)
	if err := os.WriteFile(filepath.Join(agentDir, "activity.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	meta := map[string]any{"id": "test-status", "backend": "codex"}
	metaData, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(agentDir, "meta.json"), metaData, 0644)

	act, err := agent.ReadActivity("test-status")
	if err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if act.Backend != "codex" {
		t.Errorf("Backend = %q, want %q", act.Backend, "codex")
	}
}

func TestOutputIncludesMetadata(t *testing.T) {
	dir := t.TempDir()
	origBase := storage.BaseDir
	storage.BaseDir = filepath.Join(dir, ".ag")
	defer func() { storage.BaseDir = origBase }()

	agentDir := agent.AgentDir("test-output")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatal(err)
	}

	activity := map[string]any{
		"status":      "done",
		"backend":     "codex",
		"startedAt":   time.Now().Unix() - 5,
		"finishedAt":  time.Now().Unix(),
		"turns":       3,
		"tokensTotal": 500,
	}
	data, _ := json.Marshal(activity)
	os.WriteFile(filepath.Join(agentDir, "activity.json"), data, 0644)
	os.WriteFile(filepath.Join(agentDir, "output"), []byte("test output content"), 0644)

	output, err := Output("test-output", 0)
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if !strings.Contains(output, "test output content") {
		t.Errorf("output = %q, want to contain test output", output)
	}
}