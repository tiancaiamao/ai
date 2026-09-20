package app

import (
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

// These tests cover the regression where /fork and /rewind stopped working
// after a compacted session was resumed: lazy loading represented
// pre-compaction messages with synthetic entries whose IDs the client could
// pick (via get_tree / get_fork_messages) but /fork and /rewind rejected,
// because those handlers call EnsureFullyLoaded and the synthetic IDs are
// discarded by the full load.

// seedCompactedSessionOnDisk writes a session that already contains a
// compaction, reproducing the on-disk state of a long conversation that was
// compacted and later resumed. It returns the session directory and the real
// (persisted) entry IDs.
func seedCompactedSessionOnDisk(t *testing.T, sessionsDir, sessionID string) (string, map[string]string) {
	t.Helper()
	sessDir := filepath.Join(sessionsDir, sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	s := session.NewSession(sessDir)

	u1 := agentctx.NewUserMessage("before-1")
	u1.Timestamp = 1000
	idU1, err := s.AppendMessage(u1)
	if err != nil {
		t.Fatalf("append u1: %v", err)
	}
	a1 := assistantMessage("before-a", 2000)
	idA1, err := s.AppendMessage(a1)
	if err != nil {
		t.Fatalf("append a1: %v", err)
	}

	// Compaction snapshot = the post-compaction in-memory context: a summary
	// message plus the retained tail message.
	retained := a1
	retained.EntryID = idA1
	summary := agentctx.AgentMessage{
		Role:    "user",
		Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "earlier conversation summarized"}},
	}
	if _, err := s.AppendCompaction("earlier conversation summarized", []agentctx.AgentMessage{summary, retained}); err != nil {
		t.Fatalf("append compaction: %v", err)
	}

	u2 := agentctx.NewUserMessage("after-1")
	u2.Timestamp = 3000
	idU2, err := s.AppendMessage(u2)
	if err != nil {
		t.Fatalf("append u2: %v", err)
	}
	a2 := assistantMessage("after-a", 4000)
	idA2, err := s.AppendMessage(a2)
	if err != nil {
		t.Fatalf("append a2: %v", err)
	}

	return sessDir, map[string]string{"u1": idU1, "a1": idA1, "u2": idU2, "a2": idA2}
}

func assistantMessage(text string, ts int64) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role:      "assistant",
		Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: text}},
		Timestamp: ts,
	}
}

// lazyAppForDir resumes the session at sessionsDir/sessionID with lazy loading
// (exactly what "/resume" does) and wires an App around it.
func lazyAppForDir(t *testing.T, sessionsDir, sessionID string) *App {
	t.Helper()
	sess, err := session.LoadSession(filepath.Join(sessionsDir, sessionID))
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	return newForkRewindTestAppForSession(t, sess, sessionsDir, sessionID)
}

// TestGetTreeExposesStableEntryIDsAfterLazyResume is the core regression test:
// every entryId the server hands to the client (get_tree / get_fork_messages)
// must be usable by /fork and /rewind.
func TestGetTreeExposesStableEntryIDsAfterLazyResume(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	_, real := seedCompactedSessionOnDisk(t, sessionsDir, "lazy")

	app := lazyAppForDir(t, sessionsDir, "lazy")

	tree, err := app.handleGetTree("")
	if err != nil {
		t.Fatalf("get_tree: %v", err)
	}
	entries := tree.(map[string]any)["entries"].([]TreeEntry)
	if len(entries) == 0 {
		t.Fatal("get_tree returned no entries")
	}

	// Every exposed entryId must exist in the fully-loaded session; otherwise
	// /rewind and /fork (which call EnsureFullyLoaded) will reject it.
	var userIDs []string
	sawPreCompactionUser := false
	for _, e := range entries {
		if _, ok := app.sess.GetEntry(e.EntryID); !ok {
			t.Fatalf("get_tree exposed unstable entryId %q (%s %q) absent after full load", e.EntryID, e.Type, e.Text)
		}
		if e.EntryID == real["u1"] {
			sawPreCompactionUser = true
		}
		if e.Role == "user" {
			userIDs = append(userIDs, e.EntryID)
		}
	}
	if !sawPreCompactionUser {
		t.Fatalf("get_tree should include the real pre-compaction user entry %q: %+v", real["u1"], entries)
	}
	if len(userIDs) != 2 {
		t.Fatalf("expected 2 user entries, got %v", userIDs)
	}

	fm, err := app.handleGetForkMessages("")
	if err != nil {
		t.Fatalf("get_fork_messages: %v", err)
	}
	forkMsgs := fm.(map[string]any)["messages"].([]ForkMessage)
	if len(forkMsgs) == 0 {
		t.Fatal("get_fork_messages returned no messages")
	}
	for _, m := range forkMsgs {
		if _, ok := app.sess.GetEntry(m.EntryID); !ok {
			t.Fatalf("get_fork_messages exposed unstable entryId %q (%q) absent after full load", m.EntryID, m.Text)
		}
	}

	// Every user entryId from get_tree must actually work with /rewind.
	// Rewinding only moves the leaf, so the same App can serve all of them.
	for _, id := range userIDs {
		if _, err := app.handleRewind(id); err != nil {
			t.Fatalf("rewind %q: %v", id, err)
		}
	}

	// ... and with /fork. Forking switches to a new session, so use a fresh
	// App (reloading the same on-disk session keeps the entry IDs stable).
	for _, id := range userIDs {
		forkApp := lazyAppForDir(t, sessionsDir, "lazy")
		if _, err := forkApp.handleFork(id); err != nil {
			t.Fatalf("fork %q: %v", id, err)
		}
	}
}

// TestGetForkMessagesReturnsPreCompactionUserAfterLazyResume verifies the user
// facing list is non-empty and references the real pre-compaction entry.
func TestGetForkMessagesReturnsPreCompactionUserAfterLazyResume(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	_, real := seedCompactedSessionOnDisk(t, sessionsDir, "lazy")
	app := lazyAppForDir(t, sessionsDir, "lazy")

	fm, err := app.handleGetForkMessages("")
	if err != nil {
		t.Fatalf("get_fork_messages: %v", err)
	}
	forkMsgs := fm.(map[string]any)["messages"].([]ForkMessage)

	found := false
	for _, m := range forkMsgs {
		if m.EntryID == real["u1"] && m.Text == "before-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected pre-compaction user %q in fork messages, got %+v", real["u1"], forkMsgs)
	}
}

// TestRewindRootClearsContextAfterLazyResume covers "/rewind root" clearing the
// conversation even when the session was lazily resumed.
func TestRewindRootClearsContextAfterLazyResume(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	seedCompactedSessionOnDisk(t, sessionsDir, "lazy")
	app := lazyAppForDir(t, sessionsDir, "lazy")

	if len(app.sess.GetMessages()) == 0 {
		t.Fatal("precondition: session should have messages before rewind root")
	}

	if _, err := app.handleRewind("root"); err != nil {
		t.Fatalf("rewind root: %v", err)
	}
	if msgs := app.sess.GetMessages(); len(msgs) != 0 {
		t.Fatalf("after '/rewind root' the session context should be empty, got %v", texts(msgs))
	}
	if msgs := app.ag.GetMessages(); len(msgs) != 0 {
		t.Fatalf("after '/rewind root' the agent context should be empty, got %d messages", len(msgs))
	}
}
