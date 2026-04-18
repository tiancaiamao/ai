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

// WorkflowDir is the directory for workflow state. Mutable for testing.
var WorkflowDir = ".workflow"

const (
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

// legacyStateV1 represents the old format used before schema versioning.
type legacyStateV1 struct {
	ID              string   `json:"id"`
	Template        string   `json:"template"`
	Description     string   `json:"description"`
	Status          string   `json:"status"`
	CurrentPhase    string   `json:"current_phase"`  // was a string, not an index
	StartedAt       string   `json:"started_at"`
	Phases          []string `json:"phases"`          // was a string array
	CompletedPhases []string `json:"completed_phases"`
	ArtifactDir     string   `json:"artifact_dir"`
}

func loadState() (*State, error) {
	statePath := filepath.Join(WorkflowDir, StateFile)
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, fmt.Errorf("read state: %w (run 'workflow-ctl start' first)", err)
	}

	// Try v2 format first (has schemaVersion)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}

	if _, hasVersion := raw["schemaVersion"]; hasVersion {
		var state State
		if err := json.Unmarshal(data, &state); err != nil {
			return nil, fmt.Errorf("parse state: %w", err)
		}
		return &state, nil
	}

	// Legacy v1 format — attempt migration
	return migrateV1State(data)
}

func migrateV1State(data []byte) (*State, error) {
	var legacy legacyStateV1
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("parse state (unsupported format): %w", err)
	}

	// Build phases from the old string array
	phases := make([]Phase, len(legacy.Phases))
	completedSet := make(map[string]bool, len(legacy.CompletedPhases))
	for _, p := range legacy.CompletedPhases {
		completedSet[p] = true
	}

	for i, name := range legacy.Phases {
		p := Phase{Name: name, Skill: name}
		if completedSet[name] {
			p.Status = "completed"
		} else {
			p.Status = "pending"
		}
		phases[i] = p
	}

	// Determine current phase index and activate it
	currentIdx := -1
	for i, name := range legacy.Phases {
		if !completedSet[name] {
			currentIdx = i
			break
		}
	}

	if currentIdx >= 0 {
		phases[currentIdx].Status = "active"
	} else {
		// All phases completed — set currentIdx to last phase
		currentIdx = len(phases) - 1
	}

	status := legacy.Status
	if status == "" {
		if len(legacy.CompletedPhases) == len(legacy.Phases) {
			status = "completed"
		} else {
			status = "in_progress"
		}
	}

	state := &State{
		SchemaVersion: CurrentSchemaVersion,
		ID:            legacy.ID,
		Template:      legacy.Template,
		TemplateName:  legacy.Template,
		Description:   legacy.Description,
		Phases:        phases,
		CurrentPhase:  currentIdx,
		Status:        status,
		StartedAt:     legacy.StartedAt,
		ArtifactDir:   legacy.ArtifactDir,
	}

	// Save migrated state immediately
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: migrated state but failed to save: %v\n", err)
	}

	return state, nil
}

func saveState(state *State) error {
	if err := os.MkdirAll(WorkflowDir, 0755); err != nil {
		return fmt.Errorf("create workflow dir: %w", err)
	}
	state.UpdatedAt = time.Now().Format(time.RFC3339)
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
		return fmt.Errorf("create workflow dir for audit: %w", err)
	}
	auditPath := filepath.Join(WorkflowDir, AuditFile)
	f, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
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
		return fmt.Errorf("marshal audit event: %w", err)
	}
	_, err = f.WriteString(string(data) + "\n")
	if err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return nil
}

// audit is appendAudit with error logging to stderr on failure.
func audit(event, phase, detail string) {
	if err := appendAudit(event, phase, detail); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: audit log write failed: %v\n", err)
	}
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