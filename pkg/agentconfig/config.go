// Package agentconfig loads agent YAML configuration (system prompt, memory,
// middleware list) and resolves it into runtime structures.
package agentconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ContextManagementConfig holds configuration for context management features.
// All fields have zero-value defaults that disable the features.
type ContextManagementConfig struct {
	StaleAnnotation       bool   `yaml:"stale_annotation"`
	StaleAgeInvestigative int    `yaml:"stale_age_investigative"`
	StaleAgeModification  int    `yaml:"stale_age_modification"`
	PromptFile            string `yaml:"prompt_file,omitempty"`
}

// AgentConfig represents the parsed agent.yaml configuration.
type AgentConfig struct {
	Version           int                      `yaml:"version"`
	SystemPrompt      string                   `yaml:"system_prompt"`
	Memory            string                   `yaml:"memory"`
	Middlewares       []MiddlewareEntry        `yaml:"middlewares"`
	ContextManagement *ContextManagementConfig `yaml:"context_management,omitempty"`

	// dir is the directory of the YAML file, used for resolving relative paths.
	dir string
}

// MiddlewareEntry represents a single middleware reference in the config.
type MiddlewareEntry struct {
	Name    string         `yaml:"name"`
	Enabled bool           `yaml:"enabled"`
	Params  map[string]any `yaml:"params,omitempty"`
}

// Load reads and parses an agent config YAML file.
// Returns an error if the file cannot be read, parsed, or if version != 1.
func Load(path string) (*AgentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent config: %w", err)
	}

	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse agent config: %w", err)
	}

	if cfg.Version != 1 {
		return nil, fmt.Errorf("unsupported agent config version: %d", cfg.Version)
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve agent config path: %w", err)
	}
	cfg.dir = filepath.Dir(absPath)

	return &cfg, nil
}

// ResolveSystemPrompt reads the system_prompt file and optionally appends
// the memory file content. Relative paths are resolved relative to the YAML
// file directory. The system_prompt file must exist; the memory file is optional.
func (c *AgentConfig) ResolveSystemPrompt() (string, error) {
	spPath := c.resolvePath(c.SystemPrompt)
	data, err := os.ReadFile(spPath)
	if err != nil {
		return "", fmt.Errorf("read system_prompt file %q: %w", spPath, err)
	}
	result := string(data)

	if c.Memory != "" {
		memPath := c.resolvePath(c.Memory)
		memData, err := os.ReadFile(memPath)
		if err != nil {
			// Memory file is optional — silently skip if not found.
			return result, nil
		}
		result = result + "\n" + string(memData)
	}

	return result, nil
}

// resolvePath resolves a path relative to the YAML file directory.
// If the path is already absolute, it is returned as-is.
func (c *AgentConfig) resolvePath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

// ResolveContextManagementConfig resolves the optional context_management
// section into a form suitable for consumption by the compact package.
// Returns nil if context_management is not configured.
func (c *AgentConfig) ResolveContextManagementConfig() *ContextManagementConfig {
	if c.ContextManagement == nil {
		return nil
	}
	return c.ContextManagement
}

// LoadContextManagementPrompt reads the external prompt file if configured.
// Returns empty string if no prompt_file is set or if the file cannot be read.
func (c *AgentConfig) LoadContextManagementPrompt() string {
	if c.ContextManagement == nil || c.ContextManagement.PromptFile == "" {
		return ""
	}
	path := c.resolvePath(c.ContextManagement.PromptFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
