package team

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTest(t *testing.T) {
	t.Helper()
	origDir, _ := os.Getwd()
	os.Chdir(t.TempDir())
	t.Cleanup(func() {
		_ = os.Chdir(origDir)
	})
}

func TestInitUseCurrentAndResolveBaseDir(t *testing.T) {
	setupTest(t)

	meta, err := Init("alpha", "first team")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if meta.ID != "alpha" {
		t.Fatalf("expected alpha, got %s", meta.ID)
	}

	current, err := Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current != "alpha" {
		t.Fatalf("expected current team alpha, got %s", current)
	}

	base, teamID, err := ResolveBaseDir()
	if err != nil {
		t.Fatalf("ResolveBaseDir: %v", err)
	}
	if teamID != "alpha" {
		t.Fatalf("expected team alpha, got %s", teamID)
	}
	if base != filepath.Join(".ag", "teams", "alpha") {
		t.Fatalf("unexpected base dir: %s", base)
	}
}

func TestListAndDone(t *testing.T) {
	setupTest(t)

	if _, err := Init("alpha", "first"); err != nil {
		t.Fatalf("Init alpha: %v", err)
	}
	if _, err := Init("beta", "second"); err != nil {
		t.Fatalf("Init beta: %v", err)
	}

	if _, err := Done("alpha"); err != nil {
		t.Fatalf("Done alpha: %v", err)
	}

	teams, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(teams))
	}

	statuses := map[string]string{}
	for _, item := range teams {
		statuses[item.ID] = item.Status
	}
	if statuses["alpha"] != "done" {
		t.Fatalf("alpha should be done, got %s", statuses["alpha"])
	}
	if statuses["beta"] != "active" {
		t.Fatalf("beta should be active, got %s", statuses["beta"])
	}
}

func TestCleanupGuardsRunningAgents(t *testing.T) {
	setupTest(t)

	if _, err := Init("alpha", "first"); err != nil {
		t.Fatalf("Init alpha: %v", err)
	}
	runningStatus := filepath.Join(".ag", "teams", "alpha", "agents", "w1", "status")
	if err := os.MkdirAll(filepath.Dir(runningStatus), 0755); err != nil {
		t.Fatalf("mkdir running status dir: %v", err)
	}
	if err := os.WriteFile(runningStatus, []byte("running\n"), 0644); err != nil {
		t.Fatalf("write status: %v", err)
	}

	if err := Cleanup("alpha", false); err == nil {
		t.Fatal("expected cleanup to fail when running agents exist")
	}

	if err := Cleanup("alpha", true); err != nil {
		t.Fatalf("forced cleanup failed: %v", err)
	}
}
