package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// === Configuration ===

const (
	WorkflowDir = ".workflow"
	StateFile   = "STATE.json"
	AuditFile   = "AUDIT.jsonl"
	TemplateDir = "templates"
	ArtifactDir = "artifacts"
)

var skillsPath string

func init() {
	skillsPath = resolveSkillsPath()
}

func resolveSkillsPath() string {
	// 1. Explicit env override
	if p := os.Getenv("WORKFLOW_SKILLS_PATH"); p != "" {
		return p
	}

	// 2. Binary-relative: if executable lives in .../workflow/bin/workflow-ctl,
	//    then skillsPath = .../workflow/
	if exe, err := os.Executable(); err == nil {
		resolved, _ := filepath.EvalSymlinks(exe)
		parent := filepath.Dir(filepath.Dir(resolved))
		if fi, err := os.Stat(filepath.Join(parent, TemplateDir, "registry.json")); err == nil && !fi.IsDir() {
			return parent
		}
	}

	// 3. Fallback to home directory
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ai", "skills", "workflow")
}

// === State File I/O ===

func loadState() (*State, error) {
	statePath := filepath.Join(WorkflowDir, StateFile)
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("read state: %w (run 'workflow-ctl start' first)", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	return &state, nil
}

func saveState(state *State) error {
	if err := os.MkdirAll(WorkflowDir, 0755); err != nil {
		return fmt.Errorf("create workflow dir: %w", err)
	}
	statePath := filepath.Join(WorkflowDir, StateFile)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return os.WriteFile(statePath, data, 0644)
}

// === Registry File I/O ===

func loadRegistry() (*Registry, error) {
	registryPath := filepath.Join(skillsPath, TemplateDir, "registry.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("read registry: %w", err)
	}
	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	return &registry, nil
}

// === Audit Log ===

func appendAudit(event, phase, detail string) error {
	if err := os.MkdirAll(WorkflowDir, 0755); err != nil {
		return err
	}
	auditPath := filepath.Join(WorkflowDir, AuditFile)
	f, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	evt := AuditEvent{
		Timestamp: time.Now().Format(time.RFC3339),
		Event:     event,
		Phase:     phase,
		Detail:    detail,
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	_, err = f.WriteString(string(data) + "\n")
	return err
}

// === Template Resolution ===

func resolveTemplate(name string) (string, *Template, error) {
	registry, err := loadRegistry()
	if err != nil {
		return "", nil, err
	}

	if t, ok := registry.Templates[name]; ok {
		return name, &t, nil
	}

	for id, t := range registry.Templates {
		for _, alias := range t.Aliases {
			if alias == name {
				return id, &t, nil
			}
		}
	}

	return "", nil, fmt.Errorf("template '%s' not found (available: %s)", name, availableTemplates(registry))
}

func availableTemplates(r *Registry) string {
	names := make([]string, 0, len(r.Templates))
	for id := range r.Templates {
		names = append(names, id)
	}
	return strings.Join(names, ", ")
}

// === Helpers ===

func phaseFlow(phases []Phase) string {
	names := make([]string, len(phases))
	for i, p := range phases {
		names[i] = p.Name
	}
	return strings.Join(names, " → ")
}

func phaseNames(phases []Phase) string {
	names := make([]string, len(phases))
	for i, p := range phases {
		names[i] = p.Name
	}
	return strings.Join(names, ", ")
}