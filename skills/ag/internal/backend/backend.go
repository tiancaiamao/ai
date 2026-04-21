package backend

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Protocol defines how the bridge communicates with the agent process.
type Protocol string

const (
	ProtocolJSONRPC Protocol = "json-rpc"
	ProtocolRaw     Protocol = "raw"
)

// Supports declares which control-plane operations a backend can handle.
type Supports struct {
	Steer  bool `yaml:"steer"`
	Abort  bool `yaml:"abort"`
	Prompt bool `yaml:"prompt"`
}

// BackendConfig defines a single agent backend.
type BackendConfig struct {
	Name     string   `yaml:"-"`        // populated from map key
	Command  string   `yaml:"command"`
	Args     []string `yaml:"args"`
	Protocol Protocol `yaml:"protocol"`
	Supports Supports `yaml:"supports"`
}

// BackendsConfig is the top-level structure for backends.yaml.
type BackendsConfig struct {
	Backends map[string]*BackendConfig `yaml:"backends"`
}

// Find looks up a backend by name. Returns error if not found.
func (bc *BackendsConfig) Find(name string) (*BackendConfig, error) {
	b, ok := bc.Backends[name]
	if !ok {
		return nil, fmt.Errorf("backend %q not found in config", name)
	}
	b.Name = name
	return b, nil
}

// Names returns the list of available backend names.
func (bc *BackendsConfig) Names() []string {
	names := make([]string, 0, len(bc.Backends))
	for name := range bc.Backends {
		names = append(names, name)
	}
	return names
}

// DefaultBackends returns an ai-only config as fallback when backends.yaml is missing.
func DefaultBackends() *BackendsConfig {
	return &BackendsConfig{
		Backends: map[string]*BackendConfig{
			"ai": {
				Name:     "ai",
				Command:  "ai",
				Args:     []string{"--mode", "rpc"},
				Protocol: ProtocolJSONRPC,
				Supports: Supports{Steer: true, Abort: true, Prompt: true},
			},
		},
	}
}

// Load reads a backends.yaml file and returns the parsed config.
func Load(path string) (*BackendsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read backends config: %w", err)
	}

	var cfg BackendsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse backends config: %w", err)
	}

	// Populate Name from map keys
	for name, bc := range cfg.Backends {
		bc.Name = name
	}

	// Validate
	for name, bc := range cfg.Backends {
		if bc.Command == "" {
			return nil, fmt.Errorf("backend %q: command is required", name)
		}
		if bc.Protocol != ProtocolJSONRPC && bc.Protocol != ProtocolRaw {
			return nil, fmt.Errorf("backend %q: invalid protocol %q (must be json-rpc or raw)", name, bc.Protocol)
		}
	}

	return &cfg, nil
}

// LoadOrDefault attempts to load from path; falls back to default on any error.
func LoadOrDefault(path string) (*BackendsConfig, error) {
	cfg, err := Load(path)
	if err != nil {
		return DefaultBackends(), nil
	}
	return cfg, nil
}

// FindBackendsFile searches for backends.yaml relative to the ag binary location.
// It looks in the skill directory (../../skills/ag/backends.yaml from the binary).
func FindBackendsFile() string {
	// First: check relative to current working directory
	candidates := []string{
		"backends.yaml",
	}

	// If ag binary is in GOPATH/bin, the skill dir is at a known relative path
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		// Check if we're in a skill directory (skills/ag/backends.yaml)
		skillPath := filepath.Join(exeDir, "..", "..", "skills", "ag", "backends.yaml")
		candidates = append(candidates, skillPath)
	}

	for _, p := range candidates {
		abs, _ := filepath.Abs(p)
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}

	return ""
}