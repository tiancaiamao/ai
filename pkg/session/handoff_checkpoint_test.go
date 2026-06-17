package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func makeTestSessionDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func TestInitHandoffSession(t *testing.T) {
	sessionDir := makeTestSessionDir(t)

	err := InitHandoffSession(sessionDir)
	if err != nil {
		t.Fatalf("InitHandoffSession failed: %v", err)
	}

	// Verify checkpoints/cp_001/ exists
	cp001Dir := filepath.Join(sessionDir, "checkpoints", "cp_001")
	if info, err := os.Stat(cp001Dir); err != nil || !info.IsDir() {
		t.Fatalf("cp_001 directory not created: %v", err)
	}

	// Verify messages.jsonl exists with a SessionHeader
	msgsPath := filepath.Join(cp001Dir, "messages.jsonl")
	data, err := os.ReadFile(msgsPath)
	if err != nil {
		t.Fatalf("messages.jsonl not created: %v", err)
	}

	var header SessionHeader
	if err := json.Unmarshal(data[:len(data)-1], &header); err != nil { // trim newline
		t.Fatalf("failed to parse header: %v", err)
	}
	if header.Type != EntryTypeSession {
		t.Errorf("expected header type %q, got %q", EntryTypeSession, header.Type)
	}
	if header.ParentCheckpoint != "" {
		t.Errorf("expected empty ParentCheckpoint, got %q", header.ParentCheckpoint)
	}

	// Verify current.txt contains "cp_001"
	curData, err := os.ReadFile(filepath.Join(sessionDir, "current.txt"))
	if err != nil {
		t.Fatalf("current.txt not created: %v", err)
	}
	if string(curData) != "cp_001" {
		t.Errorf("expected current.txt content %q, got %q", "cp_001", string(curData))
	}

	// Verify IsHandoffSession returns true
	if !IsHandoffSession(sessionDir) {
		t.Error("IsHandoffSession should return true after InitHandoffSession")
	}
}

func TestInitHandoffSession_Idempotent(t *testing.T) {
	sessionDir := makeTestSessionDir(t)

	// First init
	if err := InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("first InitHandoffSession failed: %v", err)
	}

	// Second init should be a no-op
	if err := InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("second InitHandoffSession failed: %v", err)
	}

	// current.txt should still point to cp_001
	cur, err := GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint failed: %v", err)
	}
	if cur != "cp_001" {
		t.Errorf("expected cp_001, got %s", cur)
	}
}

func TestInitHandoffFromExisting(t *testing.T) {
	sessionDir := makeTestSessionDir(t)

	entries := []SessionEntry{
		{Type: EntryTypeMessage, ID: "m1", Message: &agentctx.AgentMessage{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hello"}}}},
		{Type: EntryTypeMessage, ID: "m2", Message: &agentctx.AgentMessage{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hi there"}}}},
	}

	if err := InitHandoffFromExisting(sessionDir, entries); err != nil {
		t.Fatalf("InitHandoffFromExisting failed: %v", err)
	}

	// Verify current.txt
	cur, err := GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint: %v", err)
	}
	if cur != "cp_001" {
		t.Errorf("expected cp_001, got %s", cur)
	}

	// Verify messages were written
	msgs, err := LoadHandoffCheckpointMessages(sessionDir, "cp_001")
	if err != nil {
		t.Fatalf("LoadHandoffCheckpointMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Errorf("unexpected roles: %s, %s", msgs[0].Role, msgs[1].Role)
	}

	// Verify checkpoint count
	count, err := ReadCheckpointCount(sessionDir)
	if err != nil {
		t.Fatalf("ReadCheckpointCount: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
}

func TestInitHandoffFromExisting_Idempotent(t *testing.T) {
	sessionDir := makeTestSessionDir(t)

	// First call initializes
	entries := []SessionEntry{
		{Type: EntryTypeMessage, ID: "m1", Message: &agentctx.AgentMessage{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "first"}}}},
	}
	if err := InitHandoffFromExisting(sessionDir, entries); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Second call should be no-op (current.txt exists)
	entries2 := []SessionEntry{
		{Type: EntryTypeMessage, ID: "m2", Message: &agentctx.AgentMessage{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "second"}}}},
	}
	if err := InitHandoffFromExisting(sessionDir, entries2); err != nil {
		t.Fatalf("second call: %v", err)
	}

	// Should still have only the first message
	msgs, err := LoadHandoffCheckpointMessages(sessionDir, "cp_001")
	if err != nil {
		t.Fatalf("LoadHandoffCheckpointMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (no-op), got %d", len(msgs))
	}
}

func TestCreateHandoffCheckpoint_Sequential(t *testing.T) {
	sessionDir := makeTestSessionDir(t)

	// First checkpoint
	cp1, err := CreateHandoffCheckpoint(sessionDir, 1, "")
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint #1 failed: %v", err)
	}
	if cp1 != "cp_001" {
		t.Errorf("expected cp_001, got %s", cp1)
	}

	// Second checkpoint with parent
	cp2, err := CreateHandoffCheckpoint(sessionDir, 2, cp1)
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint #2 failed: %v", err)
	}
	if cp2 != "cp_002" {
		t.Errorf("expected cp_002, got %s", cp2)
	}

	// Third checkpoint
	cp3, err := CreateHandoffCheckpoint(sessionDir, 3, cp2)
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint #3 failed: %v", err)
	}
	if cp3 != "cp_003" {
		t.Errorf("expected cp_003, got %s", cp3)
	}

	// Verify parent checkpoint in cp_002 header
	msgsPath := filepath.Join(sessionDir, "checkpoints", "cp_002", "messages.jsonl")
	data, err := os.ReadFile(msgsPath)
	if err != nil {
		t.Fatalf("read cp_002 messages.jsonl: %v", err)
	}
	var header SessionHeader
	if err := json.Unmarshal(data[:len(data)-1], &header); err != nil {
		t.Fatalf("parse cp_002 header: %v", err)
	}
	if header.ParentCheckpoint != cp1 {
		t.Errorf("expected ParentCheckpoint %q, got %q", cp1, header.ParentCheckpoint)
	}
}

func TestWriteHandoffMessages(t *testing.T) {
	sessionDir := makeTestSessionDir(t)
	cpName, err := CreateHandoffCheckpoint(sessionDir, 1, "")
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint failed: %v", err)
	}

	entries := []SessionEntry{
		{
			Type:      EntryTypeMessage,
			ID:        "msg1",
			Timestamp: "2024-01-01T00:00:00Z",
			Message: &agentctx.AgentMessage{
				Role: "user",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "hello"},
				},
			},
		},
		{
			Type:      EntryTypeMessage,
			ID:        "msg2",
			Timestamp: "2024-01-01T00:00:01Z",
			Message: &agentctx.AgentMessage{
				Role: "assistant",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "hi there"},
				},
			},
		},
	}

	if err := WriteHandoffMessages(sessionDir, cpName, entries); err != nil {
		t.Fatalf("WriteHandoffMessages failed: %v", err)
	}

	// Verify messages can be loaded back
	msgs, err := LoadHandoffCheckpointMessages(sessionDir, cpName)
	if err != nil {
		t.Fatalf("LoadHandoffCheckpointMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected first message role 'user', got %q", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("expected second message role 'assistant', got %q", msgs[1].Role)
	}
	if msgs[0].EntryID != "msg1" {
		t.Errorf("expected first message EntryID 'msg1', got %q", msgs[0].EntryID)
	}
}

func TestLoadHandoffCheckpointMessages_SkipsHeader(t *testing.T) {
	sessionDir := makeTestSessionDir(t)
	cpName, err := CreateHandoffCheckpoint(sessionDir, 1, "")
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint failed: %v", err)
	}

	// No messages written — just the header
	msgs, err := LoadHandoffCheckpointMessages(sessionDir, cpName)
	if err != nil {
		t.Fatalf("LoadHandoffCheckpointMessages failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages with only header, got %d", len(msgs))
	}
}

func TestSwitchCheckpoint(t *testing.T) {
	sessionDir := makeTestSessionDir(t)

	if err := SwitchCheckpoint(sessionDir, "cp_005"); err != nil {
		t.Fatalf("SwitchCheckpoint failed: %v", err)
	}

	// Verify current.txt content
	cur, err := GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint failed: %v", err)
	}
	if cur != "cp_005" {
		t.Errorf("expected cp_005, got %s", cur)
	}

	// Switch again
	if err := SwitchCheckpoint(sessionDir, "cp_006"); err != nil {
		t.Fatalf("SwitchCheckpoint #2 failed: %v", err)
	}
	cur, err = GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint #2 failed: %v", err)
	}
	if cur != "cp_006" {
		t.Errorf("expected cp_006, got %s", cur)
	}

	// Verify temp file doesn't linger
	if _, err := os.Stat(filepath.Join(sessionDir, ".current.txt.tmp")); err == nil {
		t.Error("temp file should have been renamed")
	}
}

func TestGetCurrentCheckpoint_NoFile(t *testing.T) {
	sessionDir := makeTestSessionDir(t)

	cur, err := GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint should not error on missing file: %v", err)
	}
	if cur != "" {
		t.Errorf("expected empty string for missing file, got %q", cur)
	}
}

func TestIsHandoffSession(t *testing.T) {
	t.Run("not handoff (empty path)", func(t *testing.T) {
		if IsHandoffSession("") {
			t.Error("empty dir should not be handoff")
		}
	})

	t.Run("not handoff (no meta, treated as legacy)", func(t *testing.T) {
		dir := makeTestSessionDir(t)
		// With no meta.json, the session is legacy and must NOT be treated
		// as handoff.
		if IsHandoffSession(dir) {
			t.Error("dir without meta.json should not be handoff")
		}
	})

	t.Run("not handoff (legacy mode in meta)", func(t *testing.T) {
		dir := makeTestSessionDir(t)
		meta := SessionMeta{ContextManagementMode: "legacy"}
		data, err := json.Marshal(meta)
		if err != nil {
			t.Fatalf("marshal meta: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0644); err != nil {
			t.Fatalf("write meta.json: %v", err)
		}
		if IsHandoffSession(dir) {
			t.Error("legacy session should not be handoff")
		}
	})

	t.Run("is handoff (initialized)", func(t *testing.T) {
		dir := makeTestSessionDir(t)
		if err := InitHandoffSession(dir); err != nil {
			t.Fatalf("InitHandoffSession failed: %v", err)
		}
		if !IsHandoffSession(dir) {
			t.Error("initialized session should be handoff")
		}
	})

	t.Run("IsHandoffSessionWithDefault (legacy default)", func(t *testing.T) {
		dir := makeTestSessionDir(t)
		if IsHandoffSessionWithDefault(dir, "legacy") {
			t.Error("should be false when default is legacy and no meta")
		}
	})

	t.Run("IsHandoffSessionWithDefault (handoff default)", func(t *testing.T) {
		dir := makeTestSessionDir(t)
		if !IsHandoffSessionWithDefault(dir, "handoff") {
			t.Error("should be true when default is handoff and no meta")
		}
	})
}

func TestWriteHandoffDocument(t *testing.T) {
	sessionDir := makeTestSessionDir(t)
	cpName, err := CreateHandoffCheckpoint(sessionDir, 1, "")
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint failed: %v", err)
	}

	content := "# Handoff Document\n\nThis is a test handoff."
	if err := WriteHandoffDocument(sessionDir, cpName, content); err != nil {
		t.Fatalf("WriteHandoffDocument failed: %v", err)
	}

	docPath := filepath.Join(sessionDir, "checkpoints", cpName, "handoff.md")
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read handoff.md: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}
}

func TestLoadHandoffCheckpointMessages_MixedEntries(t *testing.T) {
	sessionDir := makeTestSessionDir(t)
	cpName, err := CreateHandoffCheckpoint(sessionDir, 1, "")
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint failed: %v", err)
	}

	// Write a mix of message and non-message entries
	entries := []SessionEntry{
		{
			Type: EntryTypeMessage,
			ID:   "m1",
			Message: &agentctx.AgentMessage{
				Role: "user",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "q"},
				},
			},
		},
		{
			Type:  EntryTypeSessionInfo,
			ID:    "si1",
			Name:  "test",
			Title: "Test",
		},
		{
			Type: EntryTypeMessage,
			ID:   "m2",
			Message: &agentctx.AgentMessage{
				Role: "assistant",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "a"},
				},
			},
		},
	}

	if err := WriteHandoffMessages(sessionDir, cpName, entries); err != nil {
		t.Fatalf("WriteHandoffMessages failed: %v", err)
	}

	msgs, err := LoadHandoffCheckpointMessages(sessionDir, cpName)
	if err != nil {
		t.Fatalf("LoadHandoffCheckpointMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (non-message entries skipped), got %d", len(msgs))
	}
}

// TestCreateHandoffCheckpoint_WithExistingDirs verifies that creating a
// checkpoint works even when non-matching directories exist under checkpoints/.
// The checkpoint number comes from the caller, not directory scanning.
func TestCreateHandoffCheckpoint_WithExistingDirs(t *testing.T) {
	sessionDir := makeTestSessionDir(t)

	// Create a bogus directory that doesn't match cp_NNN pattern
	_ = os.MkdirAll(filepath.Join(sessionDir, "checkpoints", "random_dir"), 0755)

	cpName, err := CreateHandoffCheckpoint(sessionDir, 1, "")
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint failed: %v", err)
	}
	if cpName != "cp_001" {
		t.Errorf("expected cp_001 (number from caller), got %s", cpName)
	}
}

// TestFullHandoffLifecycle exercises the complete handoff workflow:
// init → write messages → create second checkpoint → switch → resume.
func TestFullHandoffLifecycle(t *testing.T) {
	sessionDir := makeTestSessionDir(t)

	// 1. Initialize
	if err := InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	// 2. Write some messages to cp_001
	msgs1 := []SessionEntry{
		{
			Type: EntryTypeMessage,
			ID:   "m1",
			Message: &agentctx.AgentMessage{
				Role: "user",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "start"},
				},
			},
		},
	}
	if err := WriteHandoffMessages(sessionDir, "cp_001", msgs1); err != nil {
		t.Fatalf("WriteHandoffMessages: %v", err)
	}

	// 3. Create cp_002 and write a handoff doc
	cp2, err := CreateHandoffCheckpoint(sessionDir, 2, "cp_001")
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint: %v", err)
	}
	if cp2 != "cp_002" {
		t.Fatalf("expected cp_002, got %s", cp2)
	}
	handoffContent := "# Handoff\nKey info here."
	if err := WriteHandoffDocument(sessionDir, cp2, handoffContent); err != nil {
		t.Fatalf("WriteHandoffDocument: %v", err)
	}
	msgs2 := []SessionEntry{
		{
			Type: EntryTypeMessage,
			ID:   "m2",
			Message: &agentctx.AgentMessage{
				Role: "user",
				Content: []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "continued"},
				},
			},
		},
	}
	if err := WriteHandoffMessages(sessionDir, cp2, msgs2); err != nil {
		t.Fatalf("WriteHandoffMessages cp2: %v", err)
	}

	// 4. Switch to cp_002
	if err := SwitchCheckpoint(sessionDir, cp2); err != nil {
		t.Fatalf("SwitchCheckpoint: %v", err)
	}

	// 5. Verify current points to cp_002
	cur, err := GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint: %v", err)
	}
	if cur != "cp_002" {
		t.Fatalf("expected current cp_002, got %s", cur)
	}

	// 6. Load messages from cp_002
	loaded, err := LoadHandoffCheckpointMessages(sessionDir, "cp_002")
	if err != nil {
		t.Fatalf("LoadHandoffCheckpointMessages: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 message in cp_002, got %d", len(loaded))
	}
	if !strings.Contains(loaded[0].ExtractText(), "continued") {
		t.Errorf("unexpected message content: %s", loaded[0].ExtractText())
	}
}
