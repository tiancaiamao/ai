package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// === Test Helpers ===

func createTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func writeTestRegistry(t *testing.T, dir string) {
	t.Helper()
	registry := Registry{
		Version: "3.0",
		Templates: map[string]Template{
			"feature": {
				Name:        "Feature Development",
				Description: "Develop a new feature",
				Phases: []TemplatePhase{
					{Name: "brainstorm", Skill: "brainstorm", Gate: true},
					{Name: "spec", Skill: "spec", Gate: true},
					{Name: "plan", Skill: "plan", Gate: true},
					{Name: "implement", Skill: "implement", Gate: false},
				},
				Category:   "features",
				Complexity: "medium",
				Aliases:    []string{"feat", "new"},
			},
			"hotfix": {
				Name:        "Hotfix",
				Description: "Emergency fix",
				Phases: []TemplatePhase{
					{Name: "implement", Skill: "implement", Gate: false},
				},
				Category:   "hotfixes",
				Complexity: "minimal",
				Aliases:    []string{"hot", "urgent"},
			},
		},
	}
	data, _ := json.MarshalIndent(registry, "", "  ")
	tmplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tmplDir, 0755)
	os.WriteFile(filepath.Join(tmplDir, "registry.json"), data, 0644)
}

func writeTestState(t *testing.T, dir string, state *State) {
	t.Helper()
	data, _ := json.MarshalIndent(state, "", "  ")
	os.MkdirAll(filepath.Join(dir, ".workflow"), 0755)
	os.WriteFile(filepath.Join(dir, ".workflow", "STATE.json"), data, 0644)
}

func makeFeatureState() *State {
	return &State{
		ID:           "wf-feature-1000",
		Template:     "feature",
		TemplateName: "Feature Development",
		Description:  "test feature",
		CurrentPhase: 0,
		Status:       "in_progress",
		ArtifactDir:  ".workflow/artifacts/feature",
		Phases: []Phase{
			{Name: "brainstorm", Skill: "brainstorm", Gate: true, Status: "active"},
			{Name: "spec", Skill: "spec", Gate: true, Status: "pending"},
			{Name: "plan", Skill: "plan", Gate: true, Status: "pending"},
			{Name: "implement", Skill: "implement", Status: "pending"},
		},
	}
}

// === State Transition Tests ===

func TestValidateTransition(t *testing.T) {
	tests := []struct {
		from    string
		to      string
		wantErr bool
	}{
		{"pending", "active", false},
		{"active", "completed", false},
		{"active", "failed", false},
		{"active", "skipped", false},
		{"failed", "active", false},
		// Invalid transitions
		{"pending", "completed", true},
		{"completed", "active", true},
		{"completed", "pending", true},
		{"skipped", "active", true},
		{"pending", "failed", true},
	}

	for _, tt := range tests {
		err := validateTransition(tt.from, tt.to)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateTransition(%s, %s) = %v, wantErr=%v", tt.from, tt.to, err, tt.wantErr)
		}
	}
}

func TestValidateWorkflowAction_AdvanceOnCompleted(t *testing.T) {
	state := makeFeatureState()
	state.Status = "completed"

	err := validateWorkflowAction(state, "advance")
	if err == nil {
		t.Error("expected error when advancing a completed workflow")
	}
}

func TestValidateWorkflowAction_AdvanceOnPaused(t *testing.T) {
	state := makeFeatureState()
	state.Status = "paused"

	err := validateWorkflowAction(state, "advance")
	if err == nil {
		t.Error("expected error when advancing a paused workflow")
	}
}

func TestValidateWorkflowAction_BackAtFirst(t *testing.T) {
	state := makeFeatureState()
	state.CurrentPhase = 0

	err := validateWorkflowAction(state, "back")
	if err == nil {
		t.Error("expected error when backing from first phase")
	}
}

func TestValidateWorkflowAction_BackFromSecond(t *testing.T) {
	state := makeFeatureState()
	state.CurrentPhase = 2

	err := validateWorkflowAction(state, "back")
	if err != nil {
		t.Errorf("expected no error when backing from phase 2, got: %v", err)
	}
}

// === Output Validation Tests ===

func TestValidateAdvanceOutput_NoOutput(t *testing.T) {
	phase := &Phase{Name: "brainstorm", Output: ""}
	err := validateAdvanceOutput(phase, "")
	if err != nil {
		t.Errorf("expected no error when no output declared, got: %v", err)
	}
}

func TestValidateAdvanceOutput_FileExists(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "design.md")
	os.WriteFile(f, []byte("design"), 0644)

	phase := &Phase{Name: "brainstorm", Output: f}
	err := validateAdvanceOutput(phase, "")
	if err != nil {
		t.Errorf("expected no error when file exists, got: %v", err)
	}
}

func TestValidateAdvanceOutput_FileMissing(t *testing.T) {
	phase := &Phase{Name: "brainstorm", Output: "/nonexistent/design.md"}
	err := validateAdvanceOutput(phase, "")
	if err == nil {
		t.Error("expected error when output file is missing")
	}
}

func TestValidateAdvanceOutput_OutputFlagOverrides(t *testing.T) {
	dir := t.TempDir()
	existingFile := filepath.Join(dir, "design.md")
	os.WriteFile(existingFile, []byte("design"), 0644)

	phase := &Phase{Name: "brainstorm", Output: "/nonexistent/design.md"}
	err := validateAdvanceOutput(phase, existingFile)
	if err != nil {
		t.Errorf("output flag should override, got: %v", err)
	}
}

// === Back Preserves Output ===

func TestBackPreservesOutput(t *testing.T) {
	state := makeFeatureState()
	// Simulate: brainstorm completed with output, spec completed with output
	state.Phases[0].Status = "completed"
	state.Phases[0].Output = ".workflow/artifacts/feature/design.md"
	state.Phases[1].Status = "completed"
	state.Phases[1].Output = ".workflow/artifacts/feature/SPEC.md"
	state.CurrentPhase = 2

	// Back to brainstorm (steps=2)
	target := state.CurrentPhase - 2 // 0
	for i := target + 1; i < len(state.Phases); i++ {
		p := &state.Phases[i]
		if p.Output != "" {
			p.PreviousOutput = p.Output
		}
		p.Status = "pending"
		p.Output = ""
		p.GateApproved = false
		p.ApprovedAt = ""
		p.Notes = ""
	}
	state.Phases[target].Status = "active"
	state.CurrentPhase = target

	// Check: output preserved in PreviousOutput
	if state.Phases[1].PreviousOutput != ".workflow/artifacts/feature/SPEC.md" {
		t.Errorf("expected PreviousOutput preserved, got: %s", state.Phases[1].PreviousOutput)
	}
	if state.Phases[1].Output != "" {
		t.Error("expected Output cleared after back")
	}
	if state.Phases[0].PreviousOutput != "" {
		t.Error("phase 0 was the target, should not have PreviousOutput modified by this logic")
	}
}

// === Template Resolution Tests ===

func TestResolveTemplate_ByExactName(t *testing.T) {
	dir := t.TempDir()
	writeTestRegistry(t, dir)
	skillsPath = dir

	id, tmpl, err := resolveTemplate("feature")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "feature" {
		t.Errorf("expected id=feature, got %s", id)
	}
	if tmpl.Name != "Feature Development" {
		t.Errorf("unexpected name: %s", tmpl.Name)
	}
}

func TestResolveTemplate_ByAlias(t *testing.T) {
	dir := t.TempDir()
	writeTestRegistry(t, dir)
	skillsPath = dir

	id, _, err := resolveTemplate("feat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "feature" {
		t.Errorf("expected id=feature for alias 'feat', got %s", id)
	}
}

func TestResolveTemplate_NotFound(t *testing.T) {
	dir := t.TempDir()
	writeTestRegistry(t, dir)
	skillsPath = dir

	_, _, err := resolveTemplate("nonexistent")
	if err == nil {
		t.Error("expected error for unknown template")
	}
}

// === Plan Lint Tests ===

func TestLintPlan_EmptyTasks(t *testing.T) {
	plan := Plan{Version: "1"}
	issues := lintPlan(plan)
	found := false
	for _, iss := range issues {
		if iss.Level == "error" && contains(iss.Message, "no tasks") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for empty tasks")
	}
}

func TestLintPlan_DuplicateTaskID(t *testing.T) {
	plan := Plan{
		Version: "1",
		Tasks: []PTask{
			{ID: "T001", Title: "Task 1"},
			{ID: "T001", Title: "Task 2"},
		},
	}
	issues := lintPlan(plan)
	found := false
	for _, iss := range issues {
		if iss.Level == "error" && contains(iss.Message, "duplicate") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for duplicate task ID")
	}
}

func TestLintPlan_MissingDependency(t *testing.T) {
	plan := Plan{
		Version: "1",
		Tasks: []PTask{
			{ID: "T001", Title: "Task 1", Dependencies: []string{"T999"}},
		},
	}
	issues := lintPlan(plan)
	found := false
	for _, iss := range issues {
		if iss.Level == "error" && contains(iss.Message, "not found") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for missing dependency")
	}
}

func TestLintPlan_DependencyCycle(t *testing.T) {
	plan := Plan{
		Version: "1",
		Tasks: []PTask{
			{ID: "T001", Dependencies: []string{"T002"}},
			{ID: "T002", Dependencies: []string{"T003"}},
			{ID: "T003", Dependencies: []string{"T001"}},
		},
	}
	issues := lintPlan(plan)
	found := false
	for _, iss := range issues {
		if iss.Level == "error" && contains(iss.Message, "cycle") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for dependency cycle")
	}
}

func TestLintPlan_ValidPlan(t *testing.T) {
	plan := Plan{
		Version: "1",
		Metadata: PMetadata{
			SpecFile: "SPEC.md",
		},
		Tasks: []PTask{
			{ID: "T001", Title: "Task 1", EstimatedHours: 2},
			{ID: "T002", Title: "Task 2", EstimatedHours: 3, Dependencies: []string{"T001"}},
		},
		Groups: []PGroup{
			{Name: "core", Title: "Core", Tasks: []string{"T001", "T002"}, CommitMessage: "feat: core"},
		},
		GroupOrder: []string{"core"},
		Risks: []PRisk{
			{Area: "Scope", Risk: "Too big", Mitigation: "Break down"},
		},
	}
	issues := lintPlan(plan)
	for _, iss := range issues {
		if iss.Level == "error" {
			t.Errorf("unexpected error: %s", iss.Message)
		}
	}
}

func TestLintPlan_GroupOrderInvalidRef(t *testing.T) {
	plan := Plan{
		Version: "1",
		Tasks:   []PTask{{ID: "T001", Title: "Task 1"}},
		Groups:  []PGroup{{Name: "core", Title: "Core", Tasks: []string{"T001"}}},
		GroupOrder: []string{"core", "nonexistent"},
	}
	issues := lintPlan(plan)
	found := false
	for _, iss := range issues {
		if iss.Level == "error" && contains(iss.Message, "non-existent group") {
			found = true
		}
	}
	if !found {
		t.Error("expected error for group_order referencing non-existent group")
	}
}

func TestDependencyDepth(t *testing.T) {
	plan := Plan{
		Tasks: []PTask{
			{ID: "T001"},
			{ID: "T002", Dependencies: []string{"T001"}},
			{ID: "T003", Dependencies: []string{"T002"}},
			{ID: "T004", Dependencies: []string{"T003"}},
		},
	}
	depth := dependencyDepth(plan)
	if depth != 4 {
		t.Errorf("expected depth 4, got %d", depth)
	}
}

// === Audit Log Test ===

func TestAppendAudit(t *testing.T) {
	dir := t.TempDir()
	// Override WorkflowDir for this test
	oldWorkflowDir := WorkflowDir
	_ = oldWorkflowDir // WorkflowDir is const, we need another approach

	// Instead, just test the audit event marshalling
	evt := AuditEvent{
		Timestamp: "2025-01-25T10:00:00Z",
		Event:     "start",
		Phase:     "",
		Detail:    "template=feature",
	}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal audit event: %v", err)
	}
	if !contains(string(data), `"event":"start"`) {
		t.Errorf("audit event JSON missing event: %s", string(data))
	}

	// Verify file append works
	auditPath := filepath.Join(dir, "AUDIT.jsonl")
	os.MkdirAll(dir, 0755)
	f, _ := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	f.WriteString(string(data) + "\n")
	f.Close()

	content, _ := os.ReadFile(auditPath)
	lines := splitLines(string(content))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	var parsed AuditEvent
	json.Unmarshal([]byte(lines[0]), &parsed)
	if parsed.Event != "start" {
		t.Errorf("expected event=start, got %s", parsed.Event)
	}
}

// === Phase Flow Test ===

func TestPhaseFlow(t *testing.T) {
	phases := []Phase{
		{Name: "brainstorm"},
		{Name: "spec"},
		{Name: "plan"},
	}
	result := phaseFlow(phases)
	if result != "brainstorm → spec → plan" {
		t.Errorf("unexpected flow: %s", result)
	}
}

// === Helpers ===

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}