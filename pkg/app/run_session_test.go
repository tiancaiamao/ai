package app

import "testing"

// updateRunSession must forward the run ID and session ID to the injected
// updater, and be a no-op without a run ID or updater.
func TestUpdateRunSession(t *testing.T) {
	restore := func() {}
	if orig := runSessionUpdater; orig != nil {
		restore = func() { runSessionUpdater = orig }
	}
	defer restore()

	var gotRun, gotSession string
	calls := 0
	runSessionUpdater = func(baseDir, runID, sessionID string) error {
		calls++
		gotRun, gotSession = runID, sessionID
		return nil
	}

	app := &App{runID: "abc123"}
	app.updateRunSession("session-1")
	if calls != 1 || gotRun != "abc123" || gotSession != "session-1" {
		t.Fatalf("updater not called correctly: calls=%d run=%q session=%q", calls, gotRun, gotSession)
	}

	// Switching sessions (e.g. /resume) updates to the new session.
	app.updateRunSession("session-2")
	if calls != 2 || gotSession != "session-2" {
		t.Fatalf("session switch not forwarded: calls=%d session=%q", calls, gotSession)
	}

	// Empty run ID (standalone app) → no-op.
	runSessionUpdater = nil
	app.runID = "abc123"
	app.updateRunSession("session-3") // must not panic
	if runSessionUpdater != nil {
		t.Error("runSessionUpdater unexpectedly set")
	}
}
