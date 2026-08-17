package session

import (
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestSetSessionRole(t *testing.T) {
	sm := NewSessionManager(t.TempDir())
	sess, err := sm.CreateSession("work", "Work Session")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	id := sess.GetID()

	if err := sm.SetSessionRole("", "reviewer"); err == nil {
		t.Error("empty id should fail")
	}
	if err := sm.SetSessionRole("no-such-session", "reviewer"); err == nil {
		t.Error("unknown session should fail")
	}

	if err := sm.SetSessionRole(id, "reviewer"); err != nil {
		t.Fatalf("SetSessionRole: %v", err)
	}
	meta, err := sm.GetMeta(id)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if meta.Role != "reviewer" {
		t.Errorf("meta.Role = %q; want reviewer", meta.Role)
	}
}

func TestBuildMetaFromSession(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sess-1")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}

	sess := NewSession(sessDir)

	// Session info entry provides name/title.
	if _, err := sess.AppendSessionInfo("my-name", "My Title"); err != nil {
		t.Fatalf("AppendSessionInfo: %v", err)
	}
	if _, err := sess.AppendMessage(agentctx.NewUserMessage("hello world")); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	meta, err := buildMetaFromSession(sess, "sess-1", sessDir)
	if err != nil {
		t.Fatalf("buildMetaFromSession: %v", err)
	}
	if meta.Name != "my-name" {
		t.Errorf("meta.Name = %q; want my-name", meta.Name)
	}
	if meta.Title != "My Title" {
		t.Errorf("meta.Title = %q; want My Title", meta.Title)
	}
	if meta.MessageCount != 1 {
		t.Errorf("meta.MessageCount = %d; want 1", meta.MessageCount)
	}
}

func TestBuildMetaFromSessionFallbacks(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sess-2")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}

	// No session info entries: fall back to id / "Session".
	sess := NewSession(sessDir)
	meta, err := buildMetaFromSession(sess, "sess-2", sessDir)
	if err != nil {
		t.Fatalf("buildMetaFromSession: %v", err)
	}
	if meta.Name != "sess-2" {
		t.Errorf("meta.Name = %q; want sess-2", meta.Name)
	}
	if meta.Title != "Session" {
		t.Errorf("meta.Title = %q; want Session", meta.Title)
	}

	// Non-existent directory errors out.
	if _, err := buildMetaFromSession(sess, "x", filepath.Join(dir, "missing")); err == nil {
		t.Error("missing session dir should error")
	}
}
