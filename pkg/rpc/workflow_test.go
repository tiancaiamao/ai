package rpc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetWorkflowStatus_NoWorkflowDir(t *testing.T) {
	tmp := t.TempDir()
	state, err := GetWorkflowStatus(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != "not_started" {
		t.Errorf("Phase = %q, want not_started", state.Phase)
	}
	if state.TotalTasks != 0 {
		t.Errorf("TotalTasks = %d, want 0", state.TotalTasks)
	}
}

func TestGetWorkflowStatus_WithTasks(t *testing.T) {
	tmp := t.TempDir()
	workflowDir := filepath.Join(tmp, ".workflow")
	os.MkdirAll(workflowDir, 0o755)

	// Write state.json
	stateJSON := `{"phase":"executing","started_at":"2025-01-01T00:00:00Z","tasks_file":"tasks.md"}`
	os.WriteFile(filepath.Join(workflowDir, "state.json"), []byte(stateJSON), 0o644)

	// Write tasks.md
	tasksMD := "- [x] TASK001: Setup project\n- [-] TASK002: Implement feature\n- [ ] TASK003: Write tests\n"
	os.WriteFile(filepath.Join(tmp, "tasks.md"), []byte(tasksMD), 0o644)

	state, err := GetWorkflowStatus(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Phase != "executing" {
		t.Errorf("Phase = %q, want executing", state.Phase)
	}
	if state.TotalTasks != 3 {
		t.Errorf("TotalTasks = %d, want 3", state.TotalTasks)
	}
	if state.DoneTasks != 1 {
		t.Errorf("DoneTasks = %d, want 1", state.DoneTasks)
	}
	if state.PendingTasks != 2 {
		t.Errorf("PendingTasks = %d, want 2", state.PendingTasks)
	}
	if state.InProgressTask == nil {
		t.Fatal("expected non-nil InProgressTask")
	}
	if state.InProgressTask.ID != "TASK002" {
		t.Errorf("InProgressTask.ID = %q, want TASK002", state.InProgressTask.ID)
	}
}

func TestGetWorkflowStatus_AbsoluteTasksFile(t *testing.T) {
	tmp := t.TempDir()
	workflowDir := filepath.Join(tmp, ".workflow")
	os.MkdirAll(workflowDir, 0o755)

	tasksFile := filepath.Join(tmp, "custom_tasks.md")
	os.WriteFile(tasksFile, []byte("- [!] TASK001: Failed task\n"), 0o644)

	stateJSON := `{"phase":"executing","tasks_file":"` + tasksFile + `"}`
	os.WriteFile(filepath.Join(workflowDir, "state.json"), []byte(stateJSON), 0o644)

	state, err := GetWorkflowStatus(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.FailedTasks != 1 {
		t.Errorf("FailedTasks = %d, want 1", state.FailedTasks)
	}
}
