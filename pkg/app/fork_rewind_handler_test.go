package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/command"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
)

// newForkRewindTestApp builds a minimally-wired App around a real, persisted
// session so the /fork and /rewind handlers can be exercised end to end:
// index resolution -> session branch/fork -> agent context rebuild.
func newForkRewindTestApp(t *testing.T, sessionsDir, sessionID string) *App {
	t.Helper()
	sessDir := filepath.Join(sessionsDir, sessionID)
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	return newForkRewindTestAppForSession(t, session.NewSession(sessDir), sessionsDir, sessionID)
}

// newForkRewindTestAppForSession wires an App around an already-created session
// (for example one returned by session.LoadSession for a lazy resume).
func newForkRewindTestAppForSession(t *testing.T, sess *session.Session, sessionsDir, sessionID string) *App {
	t.Helper()
	sessMgr := session.NewSessionManager(sessionsDir)
	if err := sessMgr.SetCurrent(sessionID); err != nil {
		t.Fatalf("set current: %v", err)
	}

	ws, err := tools.NewWorkspace("")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}

	app := &App{
		sess:               sess,
		sessionMgr:         sessMgr,
		sessionID:          sessionID,
		sessionName:        sessionID,
		ws:                 ws,
		registry:           tools.NewRegistry(),
		skillResult:        &skill.LoadResult{},
		skillStats:         &skill.SkillStatsFile{},
		customSystemPrompt: "test-system-prompt",
		ag:                 agent.NewAgent(llm.Model{}, "", "test-system-prompt"),
		sessionComp:        &sessionCompactor{},
		commands:           command.New(),
	}
	// Simulate app startup: populate the agent context from the session.
	app.setAgentContext(app.createBaseContext())
	return app
}

// seedConversation appends a fixed 3-message conversation (user/assistant/user)
// with distinct timestamps and returns the resulting session entry IDs.
func seedConversation(t *testing.T, sess *session.Session) []string {
	t.Helper()
	u1 := agentctx.NewUserMessage("hello")
	u1.Timestamp = 1000
	id1, err := sess.AppendMessage(u1)
	if err != nil {
		t.Fatalf("append u1: %v", err)
	}
	a1 := agentctx.AgentMessage{
		Role:      "assistant",
		Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hi"}},
		Timestamp: 2000,
	}
	id2, err := sess.AppendMessage(a1)
	if err != nil {
		t.Fatalf("append a1: %v", err)
	}
	u2 := agentctx.NewUserMessage("second")
	u2.Timestamp = 3000
	id3, err := sess.AppendMessage(u2)
	if err != nil {
		t.Fatalf("append u2: %v", err)
	}
	return []string{id1, id2, id3}
}

func TestHandleRewind_ByIndex(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	app := newForkRewindTestApp(t, sessionsDir, "sess-rewind")
	ids := seedConversation(t, app.sess)
	app.setAgentContext(app.createBaseContext())

	if _, err := app.handleRewind("0"); err != nil {
		t.Fatalf("handleRewind(0): %v", err)
	}
	msgs := app.sess.GetMessages()
	if len(msgs) != 1 || msgs[0].ExtractText() != "hello" {
		t.Fatalf("after rewind to index 0, messages = %v", texts(msgs))
	}
	if leaf := app.sess.GetLeafID(); leaf == nil || *leaf != ids[0] {
		t.Fatalf("leaf = %v, want %s", leaf, ids[0])
	}
}

func TestHandleRewind_ByEntryID(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	app := newForkRewindTestApp(t, sessionsDir, "sess-rewind-id")
	ids := seedConversation(t, app.sess)
	app.setAgentContext(app.createBaseContext())

	if _, err := app.handleRewind(ids[1]); err != nil {
		t.Fatalf("handleRewind(entryID): %v", err)
	}
	msgs := app.sess.GetMessages()
	if len(msgs) != 2 || msgs[1].ExtractText() != "hi" {
		t.Fatalf("after rewind to entryID, messages = %v", texts(msgs))
	}
}

func TestHandleRewind_Root(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	app := newForkRewindTestApp(t, sessionsDir, "sess-rewind-root")
	seedConversation(t, app.sess)
	app.setAgentContext(app.createBaseContext())

	if _, err := app.handleRewind("root"); err != nil {
		t.Fatalf("handleRewind(root): %v", err)
	}
	if msgs := app.sess.GetMessages(); len(msgs) != 0 {
		t.Fatalf("after rewind root, messages = %v", texts(msgs))
	}
}

func TestHandleFork_ByIndex(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	app := newForkRewindTestApp(t, sessionsDir, "sess-fork")
	seedConversation(t, app.sess)
	app.setAgentContext(app.createBaseContext())
	origID := app.sessionID

	res, err := app.handleFork("2")
	if err != nil {
		t.Fatalf("handleFork(2): %v", err)
	}
	fr, ok := res.(*ForkResult)
	if !ok || fr.Cancelled || fr.Text != "second" {
		t.Fatalf("fork result = %#v", res)
	}
	if app.sessionID == origID {
		t.Fatalf("fork did not switch session (still %s)", origID)
	}
	// Fork point is "second"'s parent (the assistant reply "hi"): the new
	// session copies root..parent = [hello, hi].
	msgs := app.sess.GetMessages()
	if len(msgs) != 2 || msgs[0].ExtractText() != "hello" || msgs[1].ExtractText() != "hi" {
		t.Fatalf("forked session messages = %v", texts(msgs))
	}
	if _, err := app.sessionMgr.GetSession(app.sessionID); err != nil {
		t.Fatalf("forked session not persisted: %v", err)
	}
}

func TestHandleFork_RejectsNonUser(t *testing.T) {
	sessionsDir := filepath.Join(t.TempDir(), "sessions")
	app := newForkRewindTestApp(t, sessionsDir, "sess-fork-bad")
	seedConversation(t, app.sess)
	app.setAgentContext(app.createBaseContext())

	// index 1 is the assistant message — not a valid fork point.
	if _, err := app.handleFork("1"); err == nil {
		t.Fatal("expected error forking at a non-user message")
	}
}

func texts(msgs []agentctx.AgentMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ExtractText()
	}
	return out
}
