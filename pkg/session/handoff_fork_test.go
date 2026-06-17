package session

import (
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// makeTestHandoffSessionDir creates a temporary sessions directory, initializes
// a handoff-mode session inside it via the SessionManager, populates it with
// checkpoints and messages, and returns the manager, session, and session dir.
func makeTestHandoffSessionDir(t *testing.T) (*SessionManager, *Session, string) {
	t.Helper()

	tmp := t.TempDir()
	sm := NewSessionManager(tmp)

	// Create a session.
	sess, err := sm.CreateSession("handoff-src", "Handoff Source")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	sessDir := sess.GetDir()

	// Initialize the handoff checkpoint structure.
	if err := InitHandoffSession(sessDir); err != nil {
		t.Fatalf("InitHandoffSession failed: %v", err)
	}

	// Write some messages into cp_001.
	messages := []SessionEntry{
		{
			Type:      EntryTypeMessage,
			ID:        "msg-001",
			Timestamp: "2024-01-01T00:00:00Z",
			Message: &agentctx.AgentMessage{
				Role: "user",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "hello from checkpoint 1"},
				},
			},
		},
		{
			Type:      EntryTypeMessage,
			ID:        "msg-002",
			Timestamp: "2024-01-01T00:00:01Z",
			Message: &agentctx.AgentMessage{
				Role: "assistant",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "hi there"},
				},
			},
		},
	}
	if err := WriteHandoffMessages(sessDir, "cp_001", messages); err != nil {
		t.Fatalf("WriteHandoffMessages cp_001 failed: %v", err)
	}

	// Create a second checkpoint (simulating a handoff).
	cp2, err := CreateHandoffCheckpoint(sessDir, 2, "cp_001")
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint cp_002 failed: %v", err)
	}
	if err := SwitchCheckpoint(sessDir, cp2); err != nil {
		t.Fatalf("SwitchCheckpoint failed: %v", err)
	}

	// Write a handoff.md for cp_002.
	if err := WriteHandoffDocument(sessDir, cp2, "# Handoff\n\nSummary of checkpoint 1"); err != nil {
		t.Fatalf("WriteHandoffDocument failed: %v", err)
	}

	// Write messages into cp_002.
	messages2 := []SessionEntry{
		{
			Type:      EntryTypeMessage,
			ID:        "msg-003",
			Timestamp: "2024-01-01T00:00:02Z",
			Message: &agentctx.AgentMessage{
				Role: "user",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "hello from checkpoint 2"},
				},
			},
		},
	}
	if err := WriteHandoffMessages(sessDir, cp2, messages2); err != nil {
		t.Fatalf("WriteHandoffMessages cp_002 failed: %v", err)
	}

	// Set the source session's meta to handoff mode.
	sourceID := filepath.Base(sessDir)
	meta, err := sm.GetMeta(sourceID)
	if err != nil {
		t.Fatalf("GetMeta failed: %v", err)
	}
	meta.ContextManagementMode = "handoff"
	if err := sm.saveMeta(sourceID, meta); err != nil {
		t.Fatalf("saveMeta failed: %v", err)
	}

	return sm, sess, sessDir
}

func TestForkHandoffSession_CopiesAllFiles(t *testing.T) {
	sm, sess, sessDir := makeTestHandoffSessionDir(t)

	newSess, err := sm.ForkHandoffSession(sess, "handoff-fork", "Forked Handoff Session")
	if err != nil {
		t.Fatalf("ForkHandoffSession failed: %v", err)
	}

	newDir := newSess.GetDir()

	// Verify the new session is a handoff session.
	if !IsHandoffSession(newDir) {
		t.Fatal("forked session should be a handoff session")
	}

	// Verify checkpoints/ directory exists.
	cpDir := filepath.Join(newDir, "checkpoints")
	if info, err := os.Stat(cpDir); err != nil || !info.IsDir() {
		t.Fatalf("checkpoints/ directory not copied: %v", err)
	}

	// Verify both checkpoint directories were copied.
	for _, cpName := range []string{"cp_001", "cp_002"} {
		dir := filepath.Join(cpDir, cpName)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("checkpoint %s not copied: %v", cpName, err)
		}

		// Verify messages.jsonl exists.
		msgsPath := filepath.Join(dir, "messages.jsonl")
		if _, err := os.Stat(msgsPath); err != nil {
			t.Fatalf("messages.jsonl in %s not copied: %v", cpName, err)
		}
	}

	// Verify current.txt matches the source.
	srcCurrent, err := GetCurrentCheckpoint(sessDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint source failed: %v", err)
	}
	newCurrent, err := GetCurrentCheckpoint(newDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint forked failed: %v", err)
	}
	if srcCurrent != newCurrent {
		t.Errorf("current.txt mismatch: source=%q, forked=%q", srcCurrent, newCurrent)
	}
	if newCurrent != "cp_002" {
		t.Errorf("expected current.txt to point to cp_002, got %q", newCurrent)
	}

	// Verify handoff.md was copied for cp_002.
	docPath := filepath.Join(cpDir, "cp_002", "handoff.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("handoff.md not copied: %v", err)
	}
	if string(data) == "" {
		t.Error("handoff.md content is empty")
	}

	// Verify messages.jsonl in the root was copied.
	rootMsgs := filepath.Join(newDir, "messages.jsonl")
	if _, err := os.Stat(rootMsgs); err != nil {
		t.Fatalf("root messages.jsonl not copied: %v", err)
	}
}

func TestForkHandoffSession_InheritsMode(t *testing.T) {
	sm, sess, _ := makeTestHandoffSessionDir(t)

	newSess, err := sm.ForkHandoffSession(sess, "handoff-fork-2", "Forked Handoff")
	if err != nil {
		t.Fatalf("ForkHandoffSession failed: %v", err)
	}

	newID := filepath.Base(newSess.GetDir())
	meta, err := sm.GetMeta(newID)
	if err != nil {
		t.Fatalf("GetMeta failed: %v", err)
	}

	if meta.ContextManagementMode != "handoff" {
		t.Errorf("expected ContextManagementMode %q, got %q", "handoff", meta.ContextManagementMode)
	}
	if meta.Name != "handoff-fork-2" {
		t.Errorf("expected name %q, got %q", "handoff-fork-2", meta.Name)
	}
}

func TestForkHandoffSession_CurrentTxtPointsToSameCheckpoint(t *testing.T) {
	sm, sess, sessDir := makeTestHandoffSessionDir(t)

	newSess, err := sm.ForkHandoffSession(sess, "handoff-fork-3", "Forked")
	if err != nil {
		t.Fatalf("ForkHandoffSession failed: %v", err)
	}

	srcCurrent, _ := GetCurrentCheckpoint(sessDir)
	newCurrent, _ := GetCurrentCheckpoint(newSess.GetDir())

	if srcCurrent != newCurrent {
		t.Errorf("checkpoint mismatch: source=%q forked=%q", srcCurrent, newCurrent)
	}

	// Verify the messages in the forked checkpoint match the source.
	srcMsgs, err := LoadHandoffCheckpointMessages(sessDir, srcCurrent)
	if err != nil {
		t.Fatalf("LoadHandoffCheckpointMessages source failed: %v", err)
	}
	newMsgs, err := LoadHandoffCheckpointMessages(newSess.GetDir(), newCurrent)
	if err != nil {
		t.Fatalf("LoadHandoffCheckpointMessages forked failed: %v", err)
	}

	if len(srcMsgs) != len(newMsgs) {
		t.Fatalf("message count mismatch: source=%d forked=%d", len(srcMsgs), len(newMsgs))
	}
}

func TestForkHandoffSession_SetsParentSession(t *testing.T) {
	sm, sess, _ := makeTestHandoffSessionDir(t)

	newSess, err := sm.ForkHandoffSession(sess, "handoff-fork-parent", "Forked")
	if err != nil {
		t.Fatalf("ForkHandoffSession failed: %v", err)
	}

	header := newSess.GetHeader()
	if header.ParentSession == "" {
		t.Error("expected ParentSession to be set on forked session")
	}

	sourcePath := sess.GetPath()
	if header.ParentSession != sourcePath {
		t.Errorf("expected ParentSession %q, got %q", sourcePath, header.ParentSession)
	}
}

func TestForkHandoffSession_NilSource(t *testing.T) {
	sm := NewSessionManager(t.TempDir())

	_, err := sm.ForkHandoffSession(nil, "fork", "Forked")
	if err == nil {
		t.Fatal("expected error for nil source")
	}
}

func TestForkHandoffSession_DifferentDirectory(t *testing.T) {
	sm, sess, sessDir := makeTestHandoffSessionDir(t)

	newSess, err := sm.ForkHandoffSession(sess, "unique-fork", "Unique")
	if err != nil {
		t.Fatalf("ForkHandoffSession failed: %v", err)
	}

	// The new session must be in a different directory.
	if newSess.GetDir() == sessDir {
		t.Fatal("forked session should be in a different directory from source")
	}

	// The new session must be loadable via the manager.
	newID := filepath.Base(newSess.GetDir())
	loaded, err := sm.GetSession(newID)
	if err != nil {
		t.Fatalf("GetSession on forked session failed: %v", err)
	}
	if loaded.GetDir() != newSess.GetDir() {
		t.Error("loaded session dir mismatch")
	}
}
