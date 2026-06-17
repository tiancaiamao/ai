package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

func makeMessageEntry(id, role, text string) session.SessionEntry {
	return session.SessionEntry{
		Type: session.EntryTypeMessage,
		ID:   id,
		Message: &agentctx.AgentMessage{
			Role: role,
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: text},
			},
		},
	}
}

// TestLoadHandoffResumeState tests the handoff resume path: it reads
// current.txt → loads the checkpoint messages.
func TestLoadHandoffResumeState(t *testing.T) {
	sessionDir := t.TempDir()

	// Initialize handoff session
	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	// Write messages to cp_001
	entries := []session.SessionEntry{
		makeMessageEntry("m1", "user", "hello world"),
		makeMessageEntry("m2", "assistant", "hi there"),
	}
	if err := session.WriteHandoffMessages(sessionDir, "cp_001", entries); err != nil {
		t.Fatalf("WriteHandoffMessages: %v", err)
	}

	// Resume should load these messages
	msgs, agentState, err := LoadHandoffResumeState(sessionDir)
	if err != nil {
		t.Fatalf("LoadHandoffResumeState: %v", err)
	}
	if agentState != nil {
		t.Error("expected nil agentState in handoff mode")
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected first message role 'user', got %q", msgs[0].Role)
	}
}

// TestLoadHandoffResumeState_AfterSwitch tests resume after switching
// to a second checkpoint.
func TestLoadHandoffResumeState_AfterSwitch(t *testing.T) {
	sessionDir := t.TempDir()

	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	// Write to cp_001
	entries1 := []session.SessionEntry{
		makeMessageEntry("m1", "user", "first checkpoint"),
	}
	if err := session.WriteHandoffMessages(sessionDir, "cp_001", entries1); err != nil {
		t.Fatalf("WriteHandoffMessages cp_001: %v", err)
	}

	// Create cp_002, write to it, switch
	cp2, err := session.CreateHandoffCheckpoint(sessionDir, 2, "cp_001")
	if err != nil {
		t.Fatalf("CreateHandoffCheckpoint: %v", err)
	}
	entries2 := []session.SessionEntry{
		makeMessageEntry("m2", "user", "second checkpoint"),
	}
	if err := session.WriteHandoffMessages(sessionDir, cp2, entries2); err != nil {
		t.Fatalf("WriteHandoffMessages cp_002: %v", err)
	}
	if err := session.SwitchCheckpoint(sessionDir, cp2); err != nil {
		t.Fatalf("SwitchCheckpoint: %v", err)
	}

	// Resume should load cp_002's messages
	msgs, _, err := LoadHandoffResumeState(sessionDir)
	if err != nil {
		t.Fatalf("LoadHandoffResumeState: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message from cp_002, got %d", len(msgs))
	}
	if msgs[0].ExtractText() != "second checkpoint" {
		t.Errorf("expected 'second checkpoint', got %q", msgs[0].ExtractText())
	}
}

// TestLoadHandoffResumeState_NotHandoff verifies that non-handoff sessions
// return nil, nil, nil.
func TestLoadHandoffResumeState_NotHandoff(t *testing.T) {
	// Empty dir
	msgs, agentState, err := LoadHandoffResumeState("")
	if err != nil {
		t.Fatalf("expected no error for empty dir: %v", err)
	}
	if msgs != nil {
		t.Error("expected nil messages for empty dir")
	}
	if agentState != nil {
		t.Error("expected nil agentState for empty dir")
	}

	// Temp dir with no checkpoints/
	dir := t.TempDir()
	msgs, agentState, err = LoadHandoffResumeState(dir)
	if err != nil {
		t.Fatalf("expected no error for non-handoff dir: %v", err)
	}
	if msgs != nil {
		t.Error("expected nil messages for non-handoff dir")
	}
	if agentState != nil {
		t.Error("expected nil agentState for non-handoff dir")
	}
}

// TestLoadResumeState_HandoffPath verifies that LoadResumeState delegates to
// the handoff path for handoff sessions.
func TestLoadResumeState_HandoffPath(t *testing.T) {
	sessionDir := t.TempDir()

	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	entries := []session.SessionEntry{
		makeMessageEntry("m1", "user", "handoff message"),
	}
	if err := session.WriteHandoffMessages(sessionDir, "cp_001", entries); err != nil {
		t.Fatalf("WriteHandoffMessages: %v", err)
	}

	// LoadResumeState should detect handoff and load checkpoint messages
	msgs, llmCtx, agentState, err := LoadResumeState(sessionDir, nil)
	if err != nil {
		t.Fatalf("LoadResumeState: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if llmCtx != "" {
		t.Errorf("expected empty llmContext in handoff mode, got %q", llmCtx)
	}
	if agentState != nil {
		t.Error("expected nil agentState in handoff mode")
	}
}

// TestLoadResumeState_OldSessionCompat verifies that old sessions (no
// checkpoints/ directory, no current.txt) fall through to the legacy path.
func TestLoadResumeState_OldSessionCompat(t *testing.T) {
	sessionDir := t.TempDir()

	// Create a plain messages.jsonl without any checkpoint structure
	// The legacy LoadResumeState will try to load the latest checkpoint and
	// find none, falling back to fallbackMessages.
	fallback := []agentctx.AgentMessage{
		{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "legacy"},
			},
		},
	}

	msgs, llmCtx, agentState, err := LoadResumeState(sessionDir, fallback)
	if err != nil {
		t.Fatalf("LoadResumeState should not error: %v", err)
	}
	// Legacy path with no checkpoint → returns fallback messages
	if len(msgs) != len(fallback) {
		t.Errorf("expected %d fallback messages, got %d", len(fallback), len(msgs))
	}
	if llmCtx != "" {
		t.Errorf("expected empty llmContext for legacy, got %q", llmCtx)
	}
	if agentState != nil {
		t.Error("expected nil agentState for legacy")
	}

	// Verify it's NOT detected as handoff
	if session.IsHandoffSession(sessionDir) {
		t.Error("legacy session should not be detected as handoff")
	}
}

// TestLoadResumeState_EmptySessionDir verifies behavior with empty sessionDir.
func TestLoadResumeState_EmptySessionDir(t *testing.T) {
	fallback := []agentctx.AgentMessage{
		{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "x"}}},
	}
	msgs, _, _, err := LoadResumeState("", fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected fallback, got %d messages", len(msgs))
	}
}

// TestLoadResumeState_HandoffCheckpointFiles verifies the actual file
// structure on disk matches the design (checkpoints/cp_NNN/messages.jsonl +
// current.txt).
func TestLoadResumeState_HandoffCheckpointFiles(t *testing.T) {
	sessionDir := t.TempDir()

	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	// Verify file structure
	cp001Msgs := filepath.Join(sessionDir, "checkpoints", "cp_001", "messages.jsonl")
	if _, err := os.Stat(cp001Msgs); err != nil {
		t.Errorf("cp_001/messages.jsonl should exist: %v", err)
	}

	currentTxt := filepath.Join(sessionDir, "current.txt")
	data, err := os.ReadFile(currentTxt)
	if err != nil {
		t.Fatalf("current.txt should exist: %v", err)
	}
	if string(data) != "cp_001" {
		t.Errorf("current.txt should contain 'cp_001', got %q", string(data))
	}
}

// --- P0-1: Post-handoff conversation replayed on resume ---

// writeRootMessages writes SessionEntry lines to the root messages.jsonl.
// The first line is a SessionHeader, followed by message entries. All entries
// use the given timestamp.
func writeRootMessages(t *testing.T, sessionDir string, entries []session.SessionEntry, headerTimestamp string) {
	t.Helper()
	rootPath := filepath.Join(sessionDir, "messages.jsonl")
	f, err := os.Create(rootPath)
	if err != nil {
		t.Fatalf("create root messages.jsonl: %v", err)
	}
	defer f.Close()

	header := session.SessionHeader{
		Type:      session.EntryTypeSession,
		Version:   session.CurrentSessionVersion,
		ID:        "test-session",
		Timestamp: headerTimestamp,
	}
	headerData, _ := json.Marshal(header)
	if _, err := f.Write(append(headerData, '\n')); err != nil {
		t.Fatalf("write header: %v", err)
	}

	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(&e); err != nil {
			t.Fatalf("encode entry: %v", err)
		}
	}
}

// TestLoadHandoffResumeState_RootJournalReplay verifies that post-handoff
// conversation from the root messages.jsonl is replayed on resume (P0-1).
func TestLoadHandoffResumeState_RootJournalReplay(t *testing.T) {
	sessionDir := t.TempDir()

	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	// Read the checkpoint header timestamp (created just now).
	cpName, err := session.GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint: %v", err)
	}
	header, err := session.ReadHandoffCheckpointHeader(sessionDir, cpName)
	if err != nil {
		t.Fatalf("ReadHandoffCheckpointHeader: %v", err)
	}
	cpTime, err := time.Parse(time.RFC3339Nano, header.Timestamp)
	if err != nil {
		t.Fatalf("parse checkpoint timestamp: %v", err)
	}

	// Write checkpoint messages.
	cpEntries := []session.SessionEntry{
		makeMessageEntry("cp_m1", "user", "handoff doc content"),
	}
	if err := session.WriteHandoffMessages(sessionDir, cpName, cpEntries); err != nil {
		t.Fatalf("WriteHandoffMessages: %v", err)
	}

	// Write root messages.jsonl with entries BEFORE and AFTER the checkpoint.
	beforeTime := cpTime.Add(-1 * time.Second).UTC().Format(time.RFC3339Nano)
	afterTime := cpTime.Add(1 * time.Second).UTC().Format(time.RFC3339Nano)

	rootEntries := []session.SessionEntry{
		// This entry is BEFORE the checkpoint — should NOT be replayed.
		{
			Type:      session.EntryTypeMessage,
			ID:        "root_before",
			Timestamp: beforeTime,
			Message:   &agentctx.AgentMessage{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "old pre-handoff message"}}},
		},
		// This entry is AFTER the checkpoint — SHOULD be replayed.
		{
			Type:      session.EntryTypeMessage,
			ID:        "root_after_1",
			Timestamp: afterTime,
			Message:   &agentctx.AgentMessage{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "post-handoff question"}}},
		},
		// Another entry AFTER the checkpoint.
		{
			Type:      session.EntryTypeMessage,
			ID:        "root_after_2",
			Timestamp: afterTime,
			Message:   &agentctx.AgentMessage{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "post-handoff answer"}}},
		},
	}
	writeRootMessages(t, sessionDir, rootEntries, beforeTime)

	// Resume should return checkpoint messages + replayed root messages.
	msgs, _, err := LoadHandoffResumeState(sessionDir)
	if err != nil {
		t.Fatalf("LoadHandoffResumeState: %v", err)
	}

	// Should be 1 checkpoint message + 2 replayed root messages = 3 total.
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (1 checkpoint + 2 replayed), got %d", len(msgs))
	}

	// First message should be from checkpoint.
	if msgs[0].ExtractText() != "handoff doc content" {
		t.Errorf("expected checkpoint message first, got %q", msgs[0].ExtractText())
	}

	// Remaining should be replayed root messages.
	if msgs[1].ExtractText() != "post-handoff question" {
		t.Errorf("expected 'post-handoff question', got %q", msgs[1].ExtractText())
	}
	if msgs[2].ExtractText() != "post-handoff answer" {
		t.Errorf("expected 'post-handoff answer', got %q", msgs[2].ExtractText())
	}
}

// --- P1-3: AgentState restored on handoff resume ---

// TestLoadHandoffResumeState_AgentState verifies that agent_state.json in the
// checkpoint is loaded and returned by LoadHandoffResumeState (P1-3).
func TestLoadHandoffResumeState_AgentState(t *testing.T) {
	sessionDir := t.TempDir()

	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	cpName, err := session.GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint: %v", err)
	}

	// Write some messages so the checkpoint isn't empty.
	if err := session.WriteHandoffMessages(sessionDir, cpName, []session.SessionEntry{
		makeMessageEntry("m1", "user", "hello"),
	}); err != nil {
		t.Fatalf("WriteHandoffMessages: %v", err)
	}

	// Write agent_state.json to the checkpoint.
	expectedState := &agentctx.AgentState{
		WorkspaceRoot:     "/workspace",
		CurrentWorkingDir: "/workspace/subdir",
		TotalTurns:        42,
		TokensUsed:        12345,
	}
	if err := session.WriteHandoffAgentState(sessionDir, cpName, expectedState); err != nil {
		t.Fatalf("WriteHandoffAgentState: %v", err)
	}

	msgs, agentState, err := LoadHandoffResumeState(sessionDir)
	if err != nil {
		t.Fatalf("LoadHandoffResumeState: %v", err)
	}

	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	if agentState == nil {
		t.Fatal("expected agentState to be non-nil")
	}
	if agentState.WorkspaceRoot != expectedState.WorkspaceRoot {
		t.Errorf("expected WorkspaceRoot %q, got %q", expectedState.WorkspaceRoot, agentState.WorkspaceRoot)
	}
	if agentState.CurrentWorkingDir != expectedState.CurrentWorkingDir {
		t.Errorf("expected CurrentWorkingDir %q, got %q", expectedState.CurrentWorkingDir, agentState.CurrentWorkingDir)
	}
	if agentState.TotalTurns != expectedState.TotalTurns {
		t.Errorf("expected TotalTurns %d, got %d", expectedState.TotalTurns, agentState.TotalTurns)
	}
	if agentState.TokensUsed != expectedState.TokensUsed {
		t.Errorf("expected TokensUsed %d, got %d", expectedState.TokensUsed, agentState.TokensUsed)
	}
}

// TestLoadHandoffResumeState_NoAgentState verifies that when agent_state.json
// does not exist, the returned agentState is nil (no error).
func TestLoadHandoffResumeState_NoAgentState(t *testing.T) {
	sessionDir := t.TempDir()

	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	cpName, err := session.GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint: %v", err)
	}

	if err := session.WriteHandoffMessages(sessionDir, cpName, []session.SessionEntry{
		makeMessageEntry("m1", "user", "hello"),
	}); err != nil {
		t.Fatalf("WriteHandoffMessages: %v", err)
	}

	_, agentState, err := LoadHandoffResumeState(sessionDir)
	if err != nil {
		t.Fatalf("LoadHandoffResumeState: %v", err)
	}
	if agentState != nil {
		t.Errorf("expected nil agentState when no agent_state.json, got %+v", agentState)
	}
}
