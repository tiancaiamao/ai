package backend

import (
	"os"
	"path/filepath"
	"testing"
)

const testBackendsYAML = `
backends:
  ai:
    command: ai
    args: ["--mode", "rpc"]
    protocol: json-rpc
    supports:
      steer: true
      abort: true
      prompt: true
  codex:
    command: codex
    args: ["--quiet", "--full-auto"]
    protocol: raw
    supports:
      steer: false
      abort: false
      prompt: false
`

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backends.yaml")
	if err := os.WriteFile(path, []byte(testBackendsYAML), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(cfg.Backends))
	}

	ai, err := cfg.Find("ai")
	if err != nil {
		t.Fatalf("Find ai: %v", err)
	}
	if ai.Command != "ai" {
		t.Errorf("ai.Command = %q, want %q", ai.Command, "ai")
	}
	if ai.Protocol != ProtocolJSONRPC {
		t.Errorf("ai.Protocol = %q, want %q", ai.Protocol, ProtocolJSONRPC)
	}
	if !ai.Supports.Steer {
		t.Error("ai.Supports.Steer = false, want true")
	}

	codex, err := cfg.Find("codex")
	if err != nil {
		t.Fatalf("Find codex: %v", err)
	}
	if codex.Protocol != ProtocolRaw {
		t.Errorf("codex.Protocol = %q, want %q", codex.Protocol, ProtocolRaw)
	}
	if codex.Supports.Steer {
		t.Error("codex.Supports.Steer = true, want false")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/backends.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadOrDefaultMissingFile(t *testing.T) {
	cfg, err := LoadOrDefault("/nonexistent/backends.yaml")
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if len(cfg.Backends) != 1 {
		t.Fatalf("expected 1 default backend, got %d", len(cfg.Backends))
	}
	ai, err := cfg.Find("ai")
	if err != nil {
		t.Fatalf("Find ai in default: %v", err)
	}
	if ai.Protocol != ProtocolJSONRPC {
		t.Errorf("default ai.Protocol = %q, want %q", ai.Protocol, ProtocolJSONRPC)
	}
}

func TestDefaultBackends(t *testing.T) {
	cfg := DefaultBackends()
	if len(cfg.Backends) != 1 {
		t.Fatalf("expected 1 default backend, got %d", len(cfg.Backends))
	}
	ai, err := cfg.Find("ai")
	if err != nil {
		t.Fatalf("Find ai: %v", err)
	}
	if ai.Command != "ai" {
		t.Errorf("ai.Command = %q", ai.Command)
	}
	if !ai.Supports.Steer || !ai.Supports.Abort || !ai.Supports.Prompt {
		t.Error("default ai should support steer, abort, prompt")
	}
}

func TestFindUnknown(t *testing.T) {
	cfg := DefaultBackends()
	_, err := cfg.Find("nonexistent")
	if err == nil {
		t.Error("expected error for unknown backend")
	}
}

func TestLoadInvalidProtocol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backends.yaml")
	content := `
backends:
  bad:
    command: bad
    protocol: invalid
    supports:
      steer: false
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid protocol")
	}
}

func TestLoadMissingCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backends.yaml")
	content := `
backends:
  bad:
    protocol: raw
    supports:
      steer: false
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for missing command")
	}
}

func TestLoadAiOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backends.yaml")
	content := `
backends:
  ai:
    command: ai
    args: ["--mode", "rpc"]
    protocol: json-rpc
    supports:
      steer: true
      abort: true
      prompt: true
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(cfg.Backends))
	}
	ai, err := cfg.Find("ai")
	if err != nil {
		t.Fatalf("Find ai: %v", err)
	}
	if ai.Name != "ai" {
		t.Errorf("ai.Name = %q, want %q", ai.Name, "ai")
	}
}