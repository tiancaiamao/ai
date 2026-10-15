package run

import (
	"path/filepath"
	"testing"

	"github.com/tiancaiamao/ai/subcommand/run/tui"
)

// A run records its session to run.json via SetRunSession after serveApp was
// constructed, so sp.meta is a stale copy without it. The final status write
// must reload the file instead of clobbering the session.
func TestWriteFinalMetaPreservesSession(t *testing.T) {
	metaPath := filepath.Join(t.TempDir(), "run.json")

	if err := tui.SaveRunMeta(&tui.RunMeta{
		ID:      "abc123",
		Status:  tui.StatusRunning,
		Session: "sess-uuid",
	}, metaPath); err != nil {
		t.Fatal(err)
	}

	sp := &serveApp{
		meta:     &tui.RunMeta{ID: "abc123", Status: tui.StatusRunning}, // stale: no Session
		metaPath: metaPath,
		status:   tui.StatusKilled,
	}
	if err := sp.writeFinalMeta(); err != nil {
		t.Fatal(err)
	}

	got, err := tui.LoadRunMeta(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.Session != "sess-uuid" {
		t.Fatalf("session lost on final meta write: got %q, want %q", got.Session, "sess-uuid")
	}
	if got.Status != tui.StatusKilled {
		t.Fatalf("status = %q, want %q", got.Status, tui.StatusKilled)
	}
	if got.FinishedAt == 0 {
		t.Fatal("finished_at not set")
	}
}
