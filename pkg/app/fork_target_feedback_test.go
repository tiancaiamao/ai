package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/session"
)

// These tests pin the user-visible contract of /fork targets: a successful fork
// must report which session the client switched to (so the switch is
// observable), and a rejected target must say what was wrong with the value the
// user typed instead of a generic "invalid entryId".

// TestHandleFork_ByIndex_ReportsNewSession covers the success path: the index of
// a user message forks the session and the result names the new session.
func TestHandleFork_ByIndex_ReportsNewSession(t *testing.T) {
	app := newForkRewindTestApp(t, t.TempDir(), "sess-fork-report")
	seedConversation(t, app.sess)
	app.setAgentContext(app.createBaseContext())

	// Index 0 is the "hello" user message, index 1 its assistant reply.
	result, err := app.handleFork("0")
	if err != nil {
		t.Fatalf("handleFork(0): %v", err)
	}
	fork, ok := result.(*ForkResult)
	if !ok {
		t.Fatalf("handleFork returned %T, want *ForkResult", result)
	}
	if fork.Cancelled {
		t.Errorf("fork of a user message must not be cancelled: %+v", fork)
	}
	if fork.Text != "hello" {
		t.Errorf("ForkResult.Text = %q, want %q", fork.Text, "hello")
	}
	if fork.SessionID == "" || fork.SessionName == "" {
		t.Fatalf("fork must report the new session identity, got %+v", fork)
	}
	if got := app.sess.GetID(); got != fork.SessionID {
		t.Errorf("app session = %q, want the forked session %q", got, fork.SessionID)
	}

	// The renderer must turn that identity into readable text (previously the
	// raw JSON was shown, or nothing at all).
	rendered := FormatCommandResult("fork", fork)
	if rendered == "" {
		t.Fatal("FormatCommandResult(fork) must not be empty")
	}
	if strings.HasPrefix(rendered, "{") {
		t.Errorf("fork output must be human readable, got raw JSON:\n%s", rendered)
	}
	for _, want := range []string{"Forked to", fork.SessionName, "hello"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("fork output missing %q, got:\n%s", want, rendered)
		}
	}
}

// TestHandleFork_RejectsNonUserTargets checks that each rejection names the
// offending value and never claims a resolved entry id was an "invalid entryId".
func TestHandleFork_RejectsNonUserTargets(t *testing.T) {
	cases := []struct {
		name  string
		args  string
		wants []string
	}{
		{
			// Index 1 is the assistant reply: forking from it has no user
			// message to branch at.
			name:  "assistant index",
			args:  "1",
			wants: []string{"index 1", "not a user message", "role: assistant"},
		},
		{
			name:  "unresolvable index",
			args:  "99",
			wants: []string{"index 99 out of range", "session has 3 messages"},
		},
		{
			name:  "negative index",
			args:  "-1",
			wants: []string{"index -1 out of range"},
		},
		{
			name:  "unknown entryId",
			args:  "zz-not-an-entry",
			wants: []string{"entryId zz-not-an-entry not found"},
		},
		{
			name:  "empty",
			args:  "",
			wants: []string{"usage: /fork"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newForkRewindTestApp(t, t.TempDir(), "sess-fork-errors")
			seedConversation(t, app.sess)
			app.setAgentContext(app.createBaseContext())
			before := app.sess.GetID()

			_, err := app.handleFork(tc.args)
			if err == nil {
				t.Fatalf("handleFork(%q) should fail", tc.args)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("handleFork(%q) error %q missing %q", tc.args, err, want)
				}
			}
			if got := app.sess.GetID(); got != before {
				t.Errorf("a failed fork must not switch session: %q -> %q", before, got)
			}
		})
	}
}

// TestHandleRewind_ReportsTarget covers the /rewind confirmation text, including
// the root rewind which has no entry id.
func TestHandleRewind_ReportsTarget(t *testing.T) {
	app := newForkRewindTestApp(t, t.TempDir(), "sess-rewind-report")
	ids := seedConversation(t, app.sess)
	app.setAgentContext(app.createBaseContext())

	result, err := app.handleRewind(ids[0])
	if err != nil {
		t.Fatalf("handleRewind: %v", err)
	}
	rendered := FormatCommandResult("rewind", result)
	if rendered == "" || strings.HasPrefix(rendered, "{") {
		t.Fatalf("rewind output must be human readable, got %q", rendered)
	}
	if !strings.Contains(rendered, ids[0]) {
		t.Errorf("rewind output should name the target entry %q, got %q", ids[0], rendered)
	}

	rootResult, err := app.handleRewind("root")
	if err != nil {
		t.Fatalf("handleRewind(root): %v", err)
	}
	rootRendered := FormatCommandResult("rewind", rootResult)
	if rootRendered == "" || strings.HasPrefix(rootRendered, "{") {
		t.Fatalf("rewind root output must be human readable, got %q", rootRendered)
	}
	if !strings.Contains(rootRendered, "start of the session") {
		t.Errorf("rewind root output = %q, want a start-of-session message", rootRendered)
	}
}

// TestHandleFork_CompactedSessionDiagnostics covers index targets on a lazily
// resumed, compacted session: the summary message appears in /messages (so its
// index is in range) but has no session entry, and the retained assistant
// message is not a valid fork point. Both must be reported precisely, since
// "/messages" alone cannot tell the user which indices /fork accepts.
func TestHandleFork_CompactedSessionDiagnostics(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	seedCompactedSessionOnDisk(t, sessionsDir, "lazy")
	app := lazyAppForDir(t, sessionsDir, "lazy")

	// Index 0 is the compaction summary: in range for /messages, but not backed
	// by an entry that /fork can branch from.
	_, err := app.handleFork("0")
	if err == nil {
		t.Fatal("forking from the compaction summary should fail")
	}
	if !strings.Contains(err.Error(), "index 0 has no session entry") {
		t.Errorf("summary index error = %q, want a no-session-entry message", err)
	}

	// Index 1 is the retained assistant message: resolvable, but not a user message.
	_, err = app.handleFork("1")
	if err == nil {
		t.Fatal("forking from an assistant message should fail")
	}
	if !strings.Contains(err.Error(), "index 1") || !strings.Contains(err.Error(), "not a user message") {
		t.Errorf("assistant index error = %q, want a not-a-user-message message", err)
	}

	// Index 2 is the first post-compaction user message and must work.
	result, err := app.handleFork("2")
	if err != nil {
		t.Fatalf("fork 2: %v", err)
	}
	fork := result.(*ForkResult)
	if fork.Text != "after-1" {
		t.Errorf("ForkResult.Text = %q, want %q", fork.Text, "after-1")
	}
	if fork.SessionID == "" {
		t.Errorf("fork must report the new session id: %+v", fork)
	}
}

// seedPersistedConversation writes the standard 3-message conversation to disk
// (rather than keeping it in memory) so a test can rewrite entry ids on disk
// and reload the session the way "/resume" does.
func seedPersistedConversation(t *testing.T, sessionsDir, sessionID string) []string {
	t.Helper()
	sessDir := filepath.Join(sessionsDir, sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	return seedConversation(t, session.NewSession(sessDir))
}

// renameEntryIDs rewrites entry ids in messages.jsonl, both the "id" field and
// every "parentId" referencing it. Entry ids are 8 hex characters, so roughly
// 2% of generated ids are all decimal digits: renameEntryIDs lets a test pin
// that case deterministically.
func renameEntryIDs(t *testing.T, sessDir string, renames map[string]string) {
	t.Helper()
	path := filepath.Join(sessDir, "messages.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	content := string(data)
	for oldID, newID := range renames {
		if !strings.Contains(content, oldID) {
			t.Fatalf("entry id %q not found in %s", oldID, path)
		}
		content = strings.ReplaceAll(content, oldID, newID)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestHandleFork_NumericEntryID covers entry ids that look like /messages
// indexes. A numeric target must only be read as an index when the session has
// no entry with that exact id, otherwise the ~2% of ids made up of decimal
// digits alone are unusable:
//   - "12345678" is out of range as an index (3 messages) and used to be
//     rejected with "index 12345678 out of range";
//   - "00000001" is a valid index (the assistant reply) and used to be read as
//     one, rejecting a fork of the user message it actually names.
func TestHandleFork_NumericEntryID(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	ids := seedPersistedConversation(t, sessionsDir, "numeric-fork")
	renameEntryIDs(t, filepath.Join(sessionsDir, "numeric-fork"), map[string]string{
		ids[0]: "12345678", // "hello", first user message
		ids[2]: "00000001", // "second", second user message
	})

	app := lazyAppForDir(t, sessionsDir, "numeric-fork")
	for _, id := range []string{"12345678", "00000001"} {
		if _, ok := app.sess.GetEntry(id); !ok {
			t.Fatalf("precondition: entry %q missing after reload", id)
		}
	}

	// Out-of-range numeric id: must fork at the user message, not report an index.
	forkApp := lazyAppForDir(t, sessionsDir, "numeric-fork")
	result, err := forkApp.handleFork("12345678")
	if err != nil {
		t.Fatalf("handleFork(12345678) should resolve the entry id: %v", err)
	}
	if fork := result.(*ForkResult); fork.Text != "hello" {
		t.Errorf("ForkResult.Text = %q, want %q", fork.Text, "hello")
	}

	// In-range numeric id: the exact entry id wins over the index reading.
	forkApp = lazyAppForDir(t, sessionsDir, "numeric-fork")
	result, err = forkApp.handleFork("00000001")
	if err != nil {
		t.Fatalf("handleFork(00000001) should resolve the entry id: %v", err)
	}
	if fork := result.(*ForkResult); fork.Text != "second" {
		t.Errorf("ForkResult.Text = %q, want %q (index 1 is the assistant reply)", fork.Text, "second")
	}
}

// TestHandleRewind_NumericEntryID mirrors TestHandleFork_NumericEntryID for
// /rewind, which shares the target resolution.
func TestHandleRewind_NumericEntryID(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	ids := seedPersistedConversation(t, sessionsDir, "numeric-rewind")
	renameEntryIDs(t, filepath.Join(sessionsDir, "numeric-rewind"), map[string]string{
		ids[0]: "00000001", // "hello": index 1 is the assistant reply
	})

	app := lazyAppForDir(t, sessionsDir, "numeric-rewind")
	if _, err := app.handleRewind("00000001"); err != nil {
		t.Fatalf("handleRewind(00000001) should resolve the entry id: %v", err)
	}
	msgs := app.sess.GetMessages()
	if len(msgs) != 1 || msgs[0].ExtractText() != "hello" {
		t.Fatalf("after rewind to the numeric id, messages = %v", texts(msgs))
	}
	leaf := app.sess.GetLeafID()
	if leaf == nil || *leaf != "00000001" {
		t.Fatalf("leaf = %v, want the numeric entry id 00000001", leaf)
	}
}
