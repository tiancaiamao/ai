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

// setupTestEnv sets up a temporary directory with registry and returns cleanup.
// It saves and restores the global WorkflowDir and skillsPath.
func setupTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Save originals
	origWorkflowDir := WorkflowDir
	origSkillsPath := skillsPath

	// Override globals
	WorkflowDir = filepath.Join(dir, ".workflow")
	skillsPath = dir
	writeTestRegistry(t, dir)

	t.Cleanup(func() {
		WorkflowDir = origWorkflowDir
		skillsPath = origSkillsPath
	})

	return dir
}

func writeTestState(t *testing.T, state *State) {
	t.Helper()
	data, _ := json.MarshalIndent(state, "", "  ")
	os.MkdirAll(WorkflowDir, 0755)
	os.WriteFile(filepath.Join(WorkflowDir, StateFile), data, 0644)
}

func makeFeatureState() *State {
	return &State{
		SchemaVersion: CurrentSchemaVersion,
		ID:            "wf-feature-1000",
		Template:      "feature",
		TemplateName:  "Feature Development",
		Description:   "test feature",
		CurrentPhase:  0,
		Status:        "in_progress",
		ArtifactDir:   ".workflow/artifacts/feature",
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

// === Fix #2: back should check workflow status ===

func TestValidateWorkflowAction_BackOnCompleted(t *testing.T) {
	state := makeFeatureState()
	state.Status = "completed"
	state.CurrentPhase = 2

	err := validateWorkflowAction(state, "back")
	if err == nil {
		t.Error("expected error when backing on a completed workflow")
	}
}

func TestValidateWorkflowAction_BackOnPaused(t *testing.T) {
	state := makeFeatureState()
	state.Status = "paused"
	state.CurrentPhase = 2

	err := validateWorkflowAction(state, "back")
	if err == nil {
		t.Error("expected error when backing on a paused workflow")
	}
}

func TestValidateWorkflowAction_BackOnFailed(t *testing.T) {
	state := makeFeatureState()
	state.Status = "failed"
	state.CurrentPhase = 2

	err := validateWorkflowAction(state, "back")
	if err == nil {
		t.Error("expected error when backing on a failed workflow")
	}
}

// === Fix #3: note should check failed status ===

func TestValidateWorkflowAction_NoteOnFailed(t *testing.T) {
	state := makeFeatureState()
	state.Status = "failed"

	err := validateWorkflowAction(state, "note")
	if err == nil {
		t.Error("expected error when noting on a failed workflow")
	}
}

// === Fix #4: Schema version ===

func TestSchemaVersionSet(t *testing.T) {
	state := makeFeatureState()
	if state.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("expected schemaVersion=%d, got %d", CurrentSchemaVersion, state.SchemaVersion)
	}
}

func TestSaveAndLoadState(t *testing.T) {
	_ = setupTestEnv(t)

	state := makeFeatureState()
	if err := saveState(state); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	loaded, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}

	if loaded.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("schema version mismatch: got %d, want %d", loaded.SchemaVersion, CurrentSchemaVersion)
	}
	if loaded.ID != state.ID {
		t.Errorf("ID mismatch: got %s, want %s", loaded.ID, state.ID)
	}
	if loaded.CurrentPhase != state.CurrentPhase {
		t.Errorf("CurrentPhase mismatch: got %d, want %d", loaded.CurrentPhase, state.CurrentPhase)
	}
}

// === Fix #1: Legacy v1 migration ===

func TestMigrateV1State(t *testing.T) {
	dir := setupTestEnv(t)

	// Write a legacy v1 STATE.json (snake_case fields, no schemaVersion)
	legacy := legacyStateV1{
		ID:              "wf-refactor-old",
		Template:        "refactor",
		Description:     "Old format migration test",
		Status:          "in_progress",
		StartedAt:       "2025-01-25T00:00:00Z",
		Phases:          []string{"assess", "plan", "execute", "verify"},
		CompletedPhases: []string{"assess"},
		ArtifactDir:     ".workflow/artifacts",
	}
	data, _ := json.MarshalIndent(legacy, "", "  ")
	workflowDir := filepath.Join(dir, ".workflow")
	os.MkdirAll(workflowDir, 0755)
	os.WriteFile(filepath.Join(workflowDir, StateFile), data, 0644)

	loaded, err := loadState()
	if err != nil {
		t.Fatalf("loadState with legacy format: %v", err)
	}

	if loaded.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("expected migrated schemaVersion=%d, got %d", CurrentSchemaVersion, loaded.SchemaVersion)
	}
	if loaded.ID != "wf-refactor-old" {
		t.Errorf("ID not preserved: got %s", loaded.ID)
	}
	if len(loaded.Phases) != 4 {
		t.Fatalf("expected 4 phases, got %d", len(loaded.Phases))
	}
	// First phase (assess) was completed
	if loaded.Phases[0].Status != "completed" {
		t.Errorf("phase 0 (assess) should be completed, got %s", loaded.Phases[0].Status)
	}
	// Second phase (plan) should be active (first non-completed)
	if loaded.Phases[1].Status != "active" {
		t.Errorf("phase 1 (plan) should be active, got %s", loaded.Phases[1].Status)
	}
	if loaded.CurrentPhase != 1 {
		t.Errorf("CurrentPhase should be 1 (plan), got %d", loaded.CurrentPhase)
	}

	// Verify the file was re-saved in v2 format
	var raw map[string]json.RawMessage
	savedData, _ := os.ReadFile(filepath.Join(workflowDir, StateFile))
	json.Unmarshal(savedData, &raw)
	if _, ok := raw["schemaVersion"]; !ok {
		t.Error("migrated state should have schemaVersion field")
	}

	_ = dir // suppress unused warning
}

func TestMigrateV1StateFullyCompleted(t *testing.T) {
	dir := setupTestEnv(t)

	legacy := legacyStateV1{
		ID:              "wf-hotfix-done",
		Template:        "hotfix",
		Description:     "Already done",
		Status:          "completed",
		StartedAt:       "2025-01-25T00:00:00Z",
		Phases:          []string{"implement"},
		CompletedPhases: []string{"implement"},
		ArtifactDir:     ".workflow/artifacts",
	}
	data, _ := json.MarshalIndent(legacy, "", "  ")
	workflowDir := filepath.Join(dir, ".workflow")
	os.MkdirAll(workflowDir, 0755)
	os.WriteFile(filepath.Join(workflowDir, StateFile), data, 0644)

	loaded, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}

	if loaded.Status != "completed" {
		t.Errorf("expected completed, got %s", loaded.Status)
	}
	if loaded.CurrentPhase != 0 {
		t.Errorf("expected CurrentPhase=0, got %d", loaded.CurrentPhase)
	}

	_ = dir
}

// === Fix #7: audit error logging ===

func TestAuditWritesToLog(t *testing.T) {
	_ = setupTestEnv(t)

	err := appendAudit("test-event", "test-phase", "test-detail")
	if err != nil {
		t.Fatalf("appendAudit: %v", err)
	}

	auditPath := filepath.Join(WorkflowDir, AuditFile)
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}

	var evt AuditEvent
	lines := splitLines(string(data))
	if len(lines) == 0 {
		t.Fatal("audit log is empty")
	}
	if err := json.Unmarshal([]byte(lines[0]), &evt); err != nil {
		t.Fatalf("parse audit event: %v", err)
	}
	if evt.Event != "test-event" {
		t.Errorf("expected event=test-event, got %s", evt.Event)
	}
	if evt.Phase != "test-phase" {
		t.Errorf("expected phase=test-phase, got %s", evt.Phase)
	}
}

// === Fix #8: skip preserves existing notes ===

func TestSkipPreservesExistingNotes(t *testing.T) {
	_ = setupTestEnv(t)

	state := makeFeatureState()
	state.Phases[0].Notes = "existing note"
	writeTestState(t, state)

	// Manually simulate skip logic (same as runSkip but without os.Exit)
	cp := &state.Phases[0]
	cp.Status = "skipped"
	reason := "not needed"
	if cp.Notes != "" {
		cp.Notes += "\n"
	}
	cp.Notes += "[skip] " + reason
	if err := saveState(state); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	loaded, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}

	expected := "existing note\n[skip] not needed"
	if loaded.Phases[0].Notes != expected {
		t.Errorf("notes mismatch:\ngot:      %q\nexpected: %q", loaded.Phases[0].Notes, expected)
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
		t.Errorf("output flag should override phase output: %v", err)
	}
}

func TestBackPreservesOutput(t *testing.T) {
	state := makeFeatureState()
	state.CurrentPhase = 2
	state.Phases[0].Status = "completed"
	state.Phases[0].Output = "design.md"
	state.Phases[1].Status = "completed"
	state.Phases[1].Output = "SPEC.md"

	// Simulate back(1) — go from phase 2 back to phase 1
	target := state.CurrentPhase - 1
	for i := target + 1; i < len(state.Phases); i++ {
		p := &state.Phases[i]
		if p.Output != "" {
			p.PreviousOutput = p.Output
		}
		p.Status = "pending"
		p.Output = ""
	}
	state.Phases[target].Status = "active"
	state.CurrentPhase = target

	if state.Phases[1].Status != "active" {
		t.Error("phase 1 should be active after back")
	}
	if state.Phases[2].PreviousOutput != "" {
		// phase 2 had no output, so PreviousOutput should be empty
		t.Error("phase 2 had no output, PreviousOutput should be empty")
	}
	// Now set output on phase 2 and back again
	state.Phases[2].Output = "PLAN.yml"
	state.CurrentPhase = 2
	state.Phases[1].Status = "completed"
	state.Phases[2].Status = "active"

	target = 1
	for i := target + 1; i < len(state.Phases); i++ {
		p := &state.Phases[i]
		if p.Output != "" {
			p.PreviousOutput = p.Output
		}
		p.Output = ""
		p.Status = "pending"
	}

	if state.Phases[2].PreviousOutput != "PLAN.yml" {
		t.Errorf("expected PreviousOutput=PLAN.yml, got %s", state.Phases[2].PreviousOutput)
	}
}

func TestResolveTemplate_ByExactName(t *testing.T) {
	_ = setupTestEnv(t)

	id, _, err := resolveTemplate("feature")
	if err != nil {
		t.Fatalf("resolveTemplate(feature): %v", err)
	}
	if id != "feature" {
		t.Errorf("expected id=feature, got %s", id)
	}
}

func TestResolveTemplate_ByAlias(t *testing.T) {
	_ = setupTestEnv(t)

	id, _, err := resolveTemplate("feat")
	if err != nil {
		t.Fatalf("resolveTemplate(feat): %v", err)
	}
	if id != "feature" {
		t.Errorf("expected id=feature for alias feat, got %s", id)
	}
}

func TestResolveTemplate_NotFound(t *testing.T) {
	_ = setupTestEnv(t)

	_, _, err := resolveTemplate("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent template")
	}
}

// === Plan Lint Tests ===

func TestLintPlan_EmptyTasks(t *testing.T) {
	plan := Plan{Version: "1", Tasks: nil}
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
	_ = setupTestEnv(t)

	// Write audit event
	err := appendAudit("start", "", "template=feature")
	if err != nil {
		t.Fatalf("appendAudit: %v", err)
	}

	auditPath := filepath.Join(WorkflowDir, AuditFile)
	content, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit file: %v", err)
	}
	lines := splitLines(string(content))
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	var parsed AuditEvent
	if err := json.Unmarshal([]byte(lines[0]), &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}
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

// === UpdatedAt auto-set ===

func TestSaveStateSetsUpdatedAt(t *testing.T) {
	_ = setupTestEnv(t)

	state := makeFeatureState()
	if err := saveState(state); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	loaded, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}
	if loaded.UpdatedAt == "" {
		t.Error("UpdatedAt should be set by saveState")
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

// === Back preserves notes on target phase ===

func TestBackPreservesTargetNotes(t *testing.T) {
	_ = setupTestEnv(t)

	state := makeFeatureState()
	state.CurrentPhase = 1 // spec phase
	state.Phases[0].Status = "completed"
	state.Phases[0].Notes = "brainstorm completed with design A"
	state.Phases[1].Status = "active"
	state.Phases[1].Notes = "spec in progress, wrote 3 stories"
	if err := saveState(state); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	// Simulate back: manually apply the same logic runBack uses
	target := state.CurrentPhase - 1
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
	state.Phases[target].GateApproved = false
	state.Phases[target].ApprovedAt = ""
	// NOTE: intentionally NOT clearing target phase notes
	state.CurrentPhase = target
	state.Status = "in_progress"
	if err := saveState(state); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	loaded, err := loadState()
	if err != nil {
		t.Fatalf("loadState: %v", err)
	}

	// Phase 0 (brainstorm) should keep its notes
	if loaded.Phases[0].Notes != "brainstorm completed with design A" {
		t.Errorf("expected target phase notes preserved, got: %q", loaded.Phases[0].Notes)
	}

	// Phase 0 should be active
	if loaded.Phases[0].Status != "active" {
		t.Errorf("expected target phase active, got: %s", loaded.Phases[0].Status)
	}

	// Phase 1 notes should be cleared (it was reset)
	if loaded.Phases[1].Notes != "" {
		t.Errorf("expected subsequent phase notes cleared, got: %q", loaded.Phases[1].Notes)
	}
}

// === LintGranularity level is warning ===

func TestLintGranularity_LargeTaskNoSubtasks(t *testing.T) {
	plan := Plan{
		Version: "1",
		Tasks: []PTask{
			{ID: "T001", Title: "Big task", EstimatedHours: 6},
		},
	}
	issues := lintGranularity(plan)
	if len(issues) == 0 {
		t.Fatal("expected issue for large task without subtasks")
	}
	if issues[0].Level != "warning" {
		t.Errorf("expected warning level for large task, got: %s", issues[0].Level)
	}
}

func TestLintGranularity_LargeTaskWithSubtasks(t *testing.T) {
	plan := Plan{
		Version: "1",
		Tasks: []PTask{
			{ID: "T001", Title: "Big task", EstimatedHours: 6, Subtasks: []PSubtask{{ID: "S1", Description: "sub"}}},
		},
	}
	issues := lintGranularity(plan)
	if len(issues) != 0 {
		t.Errorf("expected no issues for large task with subtasks, got: %v", issues)
	}
}