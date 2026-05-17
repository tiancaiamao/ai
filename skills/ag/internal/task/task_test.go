package task

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTest(t *testing.T) {
	t.Helper()
	origDir, _ := os.Getwd()
	os.Chdir(t.TempDir())
	t.Cleanup(func() { os.Chdir(origDir) })
}

// claimAndRun moves a task from pending → claimed → running (ready for done/fail).
func claimAndRun(t *testing.T, id, claimant string) {
	t.Helper()
	_, err := Claim(id, claimant)
	if err != nil {
		t.Fatalf("Claim %s: %v", id, err)
	}
	_, err = Transition(id, StatusRunning)
	if err != nil {
		t.Fatalf("Transition %s to running: %v", id, err)
	}
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
	claimAndRun(t, "t001", "worker-1")

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
	claimAndRun(t, "t001", "worker-1")

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

	claimAndRun(t, "t001", "worker-1")
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
	claimAndRun(t, "t001", "worker-1")

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
	claimAndRun(t, "t001", "worker-1")

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

		// Complete t001: must go through running → done
	Transition("t001", StatusRunning)
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

		// Verify T001 — description is the YAML description field (not title prefix)
	t1, err := Load("T001")
	if err != nil {
		t.Fatalf("Load T001: %v", err)
	}
	if t1.Description != "Create schema and seed data" {
		t.Fatalf("wrong T001 description: %s", t1.Description)
	}
	if t1.Title != "Setup database" {
		t.Fatalf("wrong T001 title: %s", t1.Title)
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

// TestImportPlanForwardDependency verifies that tasks with forward references
// (dependency on a task defined LATER in the YAML) still get their deps linked.
func TestImportPlanForwardDependency(t *testing.T) {
	setupTest(t)

	planContent := `version: "1.0"
tasks:
  - id: "T002"
    title: "Task Two"
    dependencies:
      - "T001"
  - id: "T001"
    title: "Task One"
`
	planFile := filepath.Join(t.TempDir(), "PLAN.yml")
	os.WriteFile(planFile, []byte(planContent), 0644)

	count, err := ImportPlan(planFile)
	if err != nil {
		t.Fatalf("ImportPlan: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 tasks, got %d", count)
	}

		t2, err := Load("T002")
	if err != nil {
		t.Fatalf("Load T002: %v", err)
	}
	if len(t2.Dependencies) != 1 || t2.Dependencies[0] != "T001" {
		t.Fatalf("forward dependency missing: T002.Dependencies = %v", t2.Dependencies)
	}
}

func TestStateMachine_InvalidTransitions(t *testing.T) {
	setupTest(t)

	Create("Task", "")

	// pending → done is invalid (must go through claimed → running)
	_, err := Transition("t001", StatusDone)
	if err == nil {
		t.Fatal("expected error: pending → done is invalid")
	}

	// pending → running is invalid (must go through claimed first)
	_, err = Transition("t001", StatusRunning)
	if err == nil {
		t.Fatal("expected error: pending → running is invalid")
	}

	// pending → claimed is valid
	_, err = Transition("t001", StatusClaimed)
	if err != nil {
		t.Fatalf("pending → claimed should succeed: %v", err)
	}

			// claimed → done is now valid (manual override when work is complete)
	_, err = Transition("t001", StatusDone)
	if err != nil {
		t.Fatalf("claimed → done should succeed: %v", err)
	}

	// done → anything is invalid (terminal state)
	_, err = Transition("t001", StatusRunning)
	if err == nil {
		t.Fatal("expected error: done is terminal")
	}
}

func TestStateMachine_FailedToDone(t *testing.T) {
	setupTest(t)

	Create("Task", "")

	// pending → claimed → running → failed
	Transition("t001", StatusClaimed)
	Transition("t001", StatusRunning)
	Fail("t001", "some error", true)

	// failed → done is now valid (manual override after human verification)
	_, err := Transition("t001", StatusDone)
	if err != nil {
		t.Fatalf("failed → done should succeed: %v", err)
	}
}

func TestStateMachine_ReviewCycle(t *testing.T) {
	setupTest(t)

	Create("Task", "")
	claimAndRun(t, "t001", "worker-1")

	// running → review
	_, err := Transition("t001", StatusReview)
	if err != nil {
		t.Fatalf("running → review: %v", err)
	}

	// review → revision
	_, err = Transition("t001", StatusRevision)
	if err != nil {
		t.Fatalf("review → revision: %v", err)
	}

	// revision → review (second round)
	_, err = Transition("t001", StatusReview)
	if err != nil {
		t.Fatalf("revision → review: %v", err)
	}

	// review → done
	_, err = Transition("t001", StatusDone)
	if err != nil {
		t.Fatalf("review → done: %v", err)
	}
}

func TestRetry(t *testing.T) {
	setupTest(t)

	Create("Task", "")
	claimAndRun(t, "t001", "worker-1")

	// Fail the task
	_, err := Fail("t001", "error", true)
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// Retry back to pending
	task, err := Retry("t001", 3)
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if task.Status != StatusPending {
		t.Fatalf("expected pending after retry, got %s", task.Status)
	}
	if task.RetryCount != 1 {
		t.Fatalf("expected retry count 1, got %d", task.RetryCount)
	}
	if task.Claimant != "" {
		t.Fatalf("claimant should be cleared on retry, got %s", task.Claimant)
	}

	// Can retry again
	claimAndRun(t, "t001", "worker-2")
	Fail("t001", "error again", true)
	task, err = Retry("t001", 3)
	if err != nil {
		t.Fatalf("Retry 2: %v", err)
	}
	if task.RetryCount != 2 {
		t.Fatalf("expected retry count 2, got %d", task.RetryCount)
	}

	// Retry on non-failed task should fail
	Create("Another", "")
	_, err = Retry("t002", 3)
	if err == nil {
		t.Fatal("expected error: can't retry non-failed task")
	}
}

func TestRetry_MaxExceeded(t *testing.T) {
	setupTest(t)

	Create("Task", "")
	claimAndRun(t, "t001", "worker-1")
	Fail("t001", "error", true)
	Retry("t001", 2)

	claimAndRun(t, "t001", "worker-2")
	Fail("t001", "error", true)
	Retry("t001", 2)

	claimAndRun(t, "t001", "worker-3")
	Fail("t001", "error", true)

	// Third retry should fail (max 2)
	_, err := Retry("t001", 2)
	if err == nil {
		t.Fatal("expected error: max retries exceeded")
	}
}

func TestGroups(t *testing.T) {
	setupTest(t)

	planContent := `version: "1"
tasks:
  - id: "T001"
    title: "Task 1"
    group: "backend"
  - id: "T002"
    title: "Task 2"
    group: "backend"
  - id: "T003"
    title: "Task 3"
    group: "frontend"
  - id: "T004"
    title: "No group"
`
	planFile := filepath.Join(t.TempDir(), "plan.yml")
	os.WriteFile(planFile, []byte(planContent), 0644)

	ImportPlan(planFile)

	groups, err := Groups()
	if err != nil {
		t.Fatalf("Groups: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d: %v", len(groups), groups)
	}

	// GroupTasks for backend
	bt, err := GroupTasks("backend")
	if err != nil {
		t.Fatalf("GroupTasks: %v", err)
	}
	if len(bt) != 2 {
		t.Fatalf("expected 2 backend tasks, got %d", len(bt))
	}

	// GroupTasks for default (no group specified)
	dt, err := GroupTasks("default")
	if err != nil {
		t.Fatalf("GroupTasks default: %v", err)
	}
	if len(dt) != 1 {
		t.Fatalf("expected 1 default task, got %d", len(dt))
	}
}

func TestAllDone(t *testing.T) {
	setupTest(t)

	Create("Task 1", "")
	Create("Task 2", "")

	done, err := AllDone()
	if err != nil {
		t.Fatalf("AllDone: %v", err)
	}
	if done {
		t.Fatal("should not be all done")
	}

	claimAndRun(t, "t001", "w1")
	Done("t001", "ok")

	done, _ = AllDone()
	if done {
		t.Fatal("t002 still pending, should not be all done")
	}

	claimAndRun(t, "t002", "w2")
	Done("t002", "ok")

	done, _ = AllDone()
	if !done {
		t.Fatal("all tasks done, should be true")
	}
}

func TestIsTerminal(t *testing.T) {
	if !IsTerminal(StatusDone) {
		t.Fatal("done should be terminal")
	}
	if IsTerminal(StatusFailed) {
		t.Fatal("failed should not be terminal (can retry to pending)")
	}
	if IsTerminal(StatusPending) {
		t.Fatal("pending should not be terminal")
	}
	if IsTerminal(StatusRunning) {
		t.Fatal("running should not be terminal")
	}
	if IsTerminal(StatusReview) {
		t.Fatal("review should not be terminal")
	}
}

func TestImportPlanWithGroups(t *testing.T) {
	setupTest(t)

	planContent := `version: "1"
tasks:
  - id: "T001"
    title: "Backend task"
    description: |
      ## Goal
      Build the API
      ## Files
      - api/handler.go
      ## Done when
      - Tests pass
    group: "backend"
  - id: "T002"
    title: "Frontend task"
    description: "Build the UI"
    group: "frontend"
    dependencies:
      - "T001"
`
	planFile := filepath.Join(t.TempDir(), "plan.yml")
	os.WriteFile(planFile, []byte(planContent), 0644)

	count, err := ImportPlan(planFile)
	if err != nil {
		t.Fatalf("ImportPlan: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}

	t1, _ := Load("T001")
	if t1.Group != "backend" {
		t.Fatalf("expected backend group, got %s", t1.Group)
	}
	if t1.Title != "Backend task" {
		t.Fatalf("wrong title: %s", t1.Title)
	}

	t2, _ := Load("T002")
	if t2.Group != "frontend" {
		t.Fatalf("expected frontend group, got %s", t2.Group)
	}
}

func TestRetryForce(t *testing.T) {
	setupTest(t)

	Create("Task", "")
	claimAndRun(t, "t001", "worker-1")

	// Fail and retry 3 times to exceed max
	for i := 0; i < 3; i++ {
		Fail("t001", "error", true)
		Retry("t001", 3)
		claimAndRun(t, "t001", fmt.Sprintf("worker-%d", i))
	}
	Fail("t001", "final error", true)

	// Normal retry should fail (max exceeded)
	_, err := Retry("t001", 3)
	if err == nil {
		t.Fatal("expected error: max retries exceeded")
	}

	// Force retry should succeed
	task, err := RetryForce("t001")
	if err != nil {
		t.Fatalf("RetryForce: %v", err)
	}
	if task.Status != StatusPending {
		t.Fatalf("expected pending, got %s", task.Status)
	}
	if task.RetryCount != 4 {
		t.Fatalf("expected retry count 4, got %d", task.RetryCount)
	}
}

func TestReset(t *testing.T) {
	setupTest(t)

	Create("Task", "")
	claimAndRun(t, "t001", "worker-1")
	Fail("t001", "some error", true)
	Retry("t001", 3)
	claimAndRun(t, "t001", "worker-2")
	Fail("t001", "another error", true)

	// Reset should clear everything
	task, err := Reset("t001")
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if task.Status != StatusPending {
		t.Fatalf("expected pending, got %s", task.Status)
	}
	if task.Claimant != "" {
		t.Fatalf("claimant should be empty, got %s", task.Claimant)
	}
	if task.Error != "" {
		t.Fatalf("error should be empty, got %s", task.Error)
	}
	if task.RetryCount != 0 {
		t.Fatalf("retry count should be 0, got %d", task.RetryCount)
	}
	if task.ClaimedAt != 0 {
		t.Fatalf("claimedAt should be 0, got %d", task.ClaimedAt)
	}

	// Should be claimable again
	_, err = Claim("t001", "fresh-worker")
	if err != nil {
		t.Fatalf("should be claimable after reset: %v", err)
	}
}

func TestMarkNonRetryable(t *testing.T) {
	setupTest(t)

	Create("Task", "")
	claimAndRun(t, "t001", "worker-1")
	Fail("t001", "error", true)

	task, _ := Load("t001")
	if !task.Retryable {
		t.Fatal("should be retryable initially")
	}

	MarkNonRetryable("t001")

	task, _ = Load("t001")
	if task.Retryable {
		t.Fatal("should be non-retryable after MarkNonRetryable")
	}
}

func TestRecordFailedRun(t *testing.T) {
	setupTest(t)

	Create("Task", "")
	claimAndRun(t, "t001", "worker-1")
	Fail("t001", "timeout", true)

	// Record a failed run
	err := RecordFailedRun("t001", "run-abc123", 1000000, "task timed out")
	if err != nil {
		t.Fatalf("RecordFailedRun: %v", err)
	}

	task, _ := Load("t001")
	if len(task.PreviousRuns) != 1 {
		t.Fatalf("expected 1 previous run, got %d", len(task.PreviousRuns))
	}
	if task.PreviousRuns[0].RunID != "run-abc123" {
		t.Fatalf("wrong run ID: %s", task.PreviousRuns[0].RunID)
	}
	if task.PreviousRuns[0].Error != "task timed out" {
		t.Fatalf("wrong error: %s", task.PreviousRuns[0].Error)
	}

	// Record another failed run
	RecordFailedRun("t001", "run-def456", 2000000, "LLM silence")
	task, _ = Load("t001")
	if len(task.PreviousRuns) != 2 {
		t.Fatalf("expected 2 previous runs, got %d", len(task.PreviousRuns))
	}
}

func TestGetWorkerStartedAt(t *testing.T) {
	setupTest(t)

	Create("Task", "")

	// No claimant — should return 0
	ts := GetWorkerStartedAt("t001")
	if ts != 0 {
		t.Fatalf("expected 0 with no claimant, got %d", ts)
	}

	// With claimant but no worker-meta.json — should return 0
	Claim("t001", "worker-t001")
	ts = GetWorkerStartedAt("t001")
	if ts != 0 {
		t.Fatalf("expected 0 with no meta file, got %d", ts)
	}
}

func TestRetryBackoff(t *testing.T) {
	tests := []struct {
		retryCount int
		expected   time.Duration
	}{
		{0, 30 * time.Second},
		{1, 30 * time.Second},
		{2, 2 * time.Minute},
		{3, 5 * time.Minute},
		{5, 5 * time.Minute},
	}
	for _, tt := range tests {
		got := retryBackoff(tt.retryCount)
		if got != tt.expected {
			t.Errorf("retryBackoff(%d) = %v, want %v", tt.retryCount, got, tt.expected)
		}
	}
}

func TestSchedulerStateProcessSlots(t *testing.T) {
	state := &schedulerState{maxProcesses: 3}

	if state.activeSlots() != 0 {
		t.Fatalf("expected 0 active slots, got %d", state.activeSlots())
	}

	// Acquire slots up to max
	if !state.tryAcquireSlot() {
		t.Fatal("should acquire slot 1")
	}
	if !state.tryAcquireSlot() {
		t.Fatal("should acquire slot 2")
	}
	if !state.tryAcquireSlot() {
		t.Fatal("should acquire slot 3")
	}
	if state.tryAcquireSlot() {
		t.Fatal("should NOT acquire slot 4 (at cap)")
	}

	// Release one and acquire again
	state.releaseSlot()
	if state.activeSlots() != 2 {
		t.Fatalf("expected 2 active slots, got %d", state.activeSlots())
	}
	if !state.tryAcquireSlot() {
		t.Fatal("should acquire slot after release")
	}
}

func TestSchedulerStatePerTaskFailures(t *testing.T) {
	state := &schedulerState{}

	if state.getTaskFailures("t001") != 0 {
		t.Fatal("initial failures should be 0")
	}

	count := state.recordTaskFailure("t001")
	if count != 1 {
		t.Fatalf("expected 1, got %d", count)
	}
	count = state.recordTaskFailure("t001")
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}

	state.resetTaskFailures("t001")
	if state.getTaskFailures("t001") != 0 {
		t.Fatal("failures should be 0 after reset")
	}

	// Different task should be independent
	state.recordTaskFailure("t002")
	if state.getTaskFailures("t001") != 0 {
		t.Fatal("t001 failures should be independent of t002")
	}
	if state.getTaskFailures("t002") != 1 {
		t.Fatal("t002 should have 1 failure")
	}
}

func TestHasAssistantMessages(t *testing.T) {
	// Create a fake run directory with events
	tmpDir := t.TempDir()

	// Test with no run directory
	if hasAssistantMessages("nonexistent-run") {
		t.Fatal("should return false for nonexistent run")
	}

	// Create a run directory with events containing assistant messages
	runDir := filepath.Join(tmpDir, ".ai", "runs", "test-run")
	os.MkdirAll(runDir, 0755)
	eventsContent := `{"role":"user","content":"hello"}
{"role":"assistant","content":"hi there"}
`
	os.WriteFile(filepath.Join(runDir, "events.jsonl"), []byte(eventsContent), 0644)

	// Override home dir for the test
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	if !hasAssistantMessages("test-run") {
		t.Fatal("should find assistant messages")
	}

	// Test with no assistant messages
	runDir2 := filepath.Join(tmpDir, ".ai", "runs", "test-run2")
	os.MkdirAll(runDir2, 0755)
	os.WriteFile(filepath.Join(runDir2, "events.jsonl"), []byte(`{"role":"user","content":"hello"}
`), 0644)

	if hasAssistantMessages("test-run2") {
		t.Fatal("should NOT find assistant messages in run2")
	}
}
