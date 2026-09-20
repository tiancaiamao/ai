package app

import (
	"path/filepath"
	"strings"
	"testing"
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
