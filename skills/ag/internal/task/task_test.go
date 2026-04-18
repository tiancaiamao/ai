package task

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTest(t *testing.T) {
	t.Helper()
	origDir, _ := os.Getwd()
	os.Chdir(t.TempDir())
	t.Cleanup(func() { os.Chdir(origDir) })
}

func TestCreate(t *testing.T) {
	setupTest(t)

	task, err := Create("Fix the bug", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if task.ID != "t001" {
		t.Fatalf("expected t001, got %s", task.ID)
	}
	if task.Status != StatusPending {
		t.Fatalf("expected pending, got %s", task.Status)
	}
	if task.Description != "Fix the bug" {
		t.Fatalf("wrong description: %s", task.Description)
	}
}

func TestClaim(t *testing.T) {
	setupTest(t)

	Create("Task 1", "")
	Create("Task 2", "")

	// Claim first task
	task, err := Claim("t001", "worker-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if task.Status != StatusClaimed {
		t.Fatalf("expected claimed, got %s", task.Status)
	}
	if task.Claimant != "worker-1" {
		t.Fatalf("expected worker-1, got %s", task.Claimant)
	}

	// Can't claim again
	_, err = Claim("t001", "worker-2")
	if err == nil {
		t.Fatal("expected error on double claim")
	}

	// Can claim second task
	task, err = Claim("t002", "worker-2")
	if err != nil {
		t.Fatalf("Claim t002: %v", err)
	}
	if task.Claimant != "worker-2" {
		t.Fatalf("expected worker-2, got %s", task.Claimant)
	}
}

func TestDone(t *testing.T) {
	setupTest(t)

	Create("Task", "")
	Claim("t001", "worker-1")

	task, err := Done("t001", "fixed the bug in auth.go")
	if err != nil {
		t.Fatalf("Done: %v", err)
	}
	if task.Status != StatusDone {
		t.Fatalf("expected done, got %s", task.Status)
	}
	if task.Summary != "fixed the bug in auth.go" {
		t.Fatalf("wrong summary: %s", task.Summary)
	}

	// Can't claim a done task
	_, err = Claim("t001", "worker-3")
	if err == nil {
		t.Fatal("expected error on claiming done task")
	}
}

func TestFail(t *testing.T) {
	setupTest(t)

	Create("Task", "")
	Claim("t001", "worker-1")

	task, err := Fail("t001", "out of memory", true)
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if task.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", task.Status)
	}
	if task.Error != "out of memory" {
		t.Fatalf("wrong error: %s", task.Error)
	}
}

func TestList(t *testing.T) {
	setupTest(t)

	Create("Task A", "")
	Create("Task B", "")
	Claim("t001", "worker-1")

	tasks, err := List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2, got %d", len(tasks))
	}

	// Filter by pending
	pending, _ := List(StatusPending)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}

	// Filter by claimed
	claimed, _ := List(StatusClaimed)
	if len(claimed) != 1 {
		t.Fatalf("expected 1 claimed, got %d", len(claimed))
	}

	// Filter by done
	done, _ := List(StatusDone)
	if len(done) != 0 {
		t.Fatalf("expected 0 done, got %d", len(done))
	}
}

func TestShow(t *testing.T) {
	setupTest(t)

	Create("My task", "spec.md")
	task, err := Show("t001")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if task.Description != "My task" {
		t.Fatalf("wrong description: %s", task.Description)
	}
	if task.SpecFile != "spec.md" {
		t.Fatalf("wrong spec: %s", task.SpecFile)
	}

	// Non-existent
	_, err = Show("t999")
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestSequentialIDs(t *testing.T) {
	setupTest(t)

	t1, _ := Create("First", "")
	t2, _ := Create("Second", "")
	t3, _ := Create("Third", "")

	if t1.ID != "t001" || t2.ID != "t002" || t3.ID != "t003" {
		t.Fatalf("expected sequential IDs, got %s %s %s", t1.ID, t2.ID, t3.ID)
	}
}

func TestDependencyBlocksClaim(t *testing.T) {
	setupTest(t)

	Create("Base task", "")
	Create("Dependent task", "")

	if _, err := AddDependency("t002", "t001"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	if _, err := Claim("t002", "worker-1"); err == nil {
		t.Fatal("expected claim to fail when dependency is not done")
	}

	if _, err := Claim("t001", "worker-1"); err != nil {
		t.Fatalf("claim t001: %v", err)
	}
	if _, err := Done("t001", "done"); err != nil {
		t.Fatalf("done t001: %v", err)
	}

	if _, err := Claim("t002", "worker-2"); err != nil {
		t.Fatalf("expected claim to succeed after dependency is done: %v", err)
	}
}

func TestNextClaimsUnblockedTask(t *testing.T) {
	setupTest(t)

	Create("Task 1", "")
	Create("Task 2", "")

	if _, err := AddDependency("t002", "t001"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	next, err := Next("worker-1")
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if next.ID != "t001" {
		t.Fatalf("expected t001 first, got %s", next.ID)
	}
}

func TestDependencyCycleRejected(t *testing.T) {
	setupTest(t)

	Create("Task 1", "")
	Create("Task 2", "")
	Create("Task 3", "")

	if _, err := AddDependency("t002", "t001"); err != nil {
		t.Fatalf("AddDependency t002->t001: %v", err)
	}
	if _, err := AddDependency("t003", "t002"); err != nil {
		t.Fatalf("AddDependency t003->t002: %v", err)
	}

	if _, err := AddDependency("t001", "t003"); err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestDone_WithSummary(t *testing.T) {
	setupTest(t)

	Create("Task with summary", "")
	Claim("t001", "worker-1")

	task, err := Done("t001", "completed all changes to auth module")
	if err != nil {
		t.Fatalf("Done: %v", err)
	}
	if task.Status != StatusDone {
		t.Fatalf("expected done, got %s", task.Status)
	}
	if task.Summary != "completed all changes to auth module" {
		t.Fatalf("wrong summary: %s", task.Summary)
	}

	// Verify Summary persists via Show
	loaded, err := Show("t001")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if loaded.Summary != "completed all changes to auth module" {
		t.Fatalf("summary not persisted: %s", loaded.Summary)
	}
}

func TestFail_WithRetryable(t *testing.T) {
	setupTest(t)

	Create("Flaky task", "")
	Claim("t001", "worker-1")

	task, err := Fail("t001", "connection timeout", true)
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if task.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", task.Status)
	}
	if task.Error != "connection timeout" {
		t.Fatalf("wrong error: %s", task.Error)
	}
	if task.Retryable != true {
		t.Fatal("expected Retryable to be true")
	}

	// Verify Error and Retryable persist
	loaded, err := Show("t001")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if loaded.Error != "connection timeout" {
		t.Fatalf("error not persisted: %s", loaded.Error)
	}
	if loaded.Retryable != true {
		t.Fatal("retryable flag not persisted")
	}
}

func TestLoad(t *testing.T) {
	setupTest(t)

	Create("Load test task", "spec.md")

	task, err := Load("t001")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if task.ID != "t001" {
		t.Fatalf("expected t001, got %s", task.ID)
	}
	if task.Description != "Load test task" {
		t.Fatalf("wrong description: %s", task.Description)
	}
	if task.SpecFile != "spec.md" {
		t.Fatalf("wrong specFile: %s", task.SpecFile)
	}

	// Non-existent task
	_, err = Load("t999")
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestClaimNext(t *testing.T) {
	setupTest(t)

	Create("First task", "")
	Create("Second task", "")

	// Add dependency: t002 depends on t001
	AddDependency("t002", "t001")

	// ClaimNext should return t001 (first pending, unblocked)
	id, err := ClaimNext("worker-1")
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if id != "t001" {
		t.Fatalf("expected t001, got %s", id)
	}

	// t002 is still blocked by t001 (which is claimed but not done)
	_, err = ClaimNext("worker-2")
	if err == nil {
		t.Fatal("expected error, t002 should be blocked")
	}

	// Complete t001
	Done("t001", "finished")

	// Now t002 should be claimable
	id, err = ClaimNext("worker-2")
	if err != nil {
		t.Fatalf("ClaimNext after unblock: %v", err)
	}
	if id != "t002" {
		t.Fatalf("expected t002, got %s", id)
	}
}

func TestImportPlan(t *testing.T) {
	setupTest(t)

	// Create a temporary PLAN.yml file
	planContent := `version: "1.0"
tasks:
  - id: "T001"
    title: "Setup database"
    description: "Create schema and seed data"
  - id: "T002"
    title: "Build API"
    description: "REST endpoints"
    dependencies:
      - "T001"
  - id: "T003"
    title: "Write tests"
    dependencies:
      - "T002"
`
	planFile := filepath.Join(t.TempDir(), "PLAN.yml")
	os.WriteFile(planFile, []byte(planContent), 0644)

	count, err := ImportPlan(planFile)
	if err != nil {
		t.Fatalf("ImportPlan: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 tasks, got %d", count)
	}

	// Verify T001
	t1, err := Load("T001")
	if err != nil {
		t.Fatalf("Load T001: %v", err)
	}
	if t1.Description != "Setup database: Create schema and seed data" {
		t.Fatalf("wrong T001 description: %s", t1.Description)
	}

	// Verify T002 has dependency on T001
	t2, err := Load("T002")
	if err != nil {
		t.Fatalf("Load T002: %v", err)
	}
	if len(t2.Dependencies) != 1 || t2.Dependencies[0] != "T001" {
		t.Fatalf("wrong T002 dependencies: %v", t2.Dependencies)
	}

	// Verify T003 has dependency on T002
	t3, err := Load("T003")
	if err != nil {
		t.Fatalf("Load T003: %v", err)
	}
	if len(t3.Dependencies) != 1 || t3.Dependencies[0] != "T002" {
		t.Fatalf("wrong T003 dependencies: %v", t3.Dependencies)
	}

	// Verify blocking: T002 is blocked since T001 is not done
	unmet, err := UnmetDependencies("T002")
	if err != nil {
		t.Fatalf("UnmetDependencies T002: %v", err)
	}
	if len(unmet) != 1 || unmet[0] != "T001" {
		t.Fatalf("expected T001 unmet, got %v", unmet)
	}
}
