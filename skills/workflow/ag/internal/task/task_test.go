package task

import (
	"os"
	"testing"
)

func setupTest(t *testing.T) {
	t.Helper()
	origDir, _ := os.Getwd()
	os.Chdir(t.TempDir())
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset task counter
	nextTaskID = 1
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

	task, err := Done("t001", "output.md")
	if err != nil {
		t.Fatalf("Done: %v", err)
	}
	if task.Status != StatusDone {
		t.Fatalf("expected done, got %s", task.Status)
	}
	if task.OutputFile != "output.md" {
		t.Fatalf("wrong output: %s", task.OutputFile)
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

	task, err := Fail("t001", "out of memory")
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