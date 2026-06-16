package agent

import (
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

// makeTextMsg builds an agent-visible text message for testing.
func makeTextMsg(role, text string) agentctx.AgentMessage {
	return agentctx.AgentMessage{
		Role: role,
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: text},
		},
	}
}

// newHandoffTestLoopState creates a loopState suitable for testing finalizeHandoff.
func newHandoffTestLoopState(sessionDir string) *loopState {
	cfg := &LoopConfig{
		GetSessionDir: func() string { return sessionDir },
	}
	agentCtx := agentctx.NewAgentContext("test system prompt")
	return &loopState{
		config:   cfg,
		agentCtx: agentCtx,
	}
}

func TestFinalizeHandoff_CreatesCheckpoint(t *testing.T) {
	sessionDir := t.TempDir()

	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	// The initial checkpoint should be cp_001.
	initialCp, err := session.GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint: %v", err)
	}
	if initialCp != "cp_001" {
		t.Fatalf("expected initial checkpoint cp_001, got %s", initialCp)
	}

	state := newHandoffTestLoopState(sessionDir)
	state.agentCtx.RecentMessages = []agentctx.AgentMessage{
		makeTextMsg("user", "old context message"),
	}
	// Simulate that the hard floor was crossed.
	state.hardFloorCrossed = true
	state.hardFloorTurns = 3
	state.compactionRecs = 1

	handoffDoc := "# Handoff\n\nTask: implement feature X\nStatus: in progress\nNext: write tests"
	qaTurns := []qaTurn{
		{Question: "What files were modified?", Answer: "main.go and utils.go"},
		{Question: "Any errors?", Answer: "No errors encountered"},
	}

	if err := state.finalizeHandoff(handoffDoc, qaTurns); err != nil {
		t.Fatalf("finalizeHandoff: %v", err)
	}

	// Verify current.txt now points to cp_002.
	currentCp, err := session.GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint after handoff: %v", err)
	}
	if currentCp != "cp_002" {
		t.Errorf("expected checkpoint cp_002, got %s", currentCp)
	}

	// Verify handoff.md exists in the new checkpoint.
	handoffPath := filepath.Join(sessionDir, "checkpoints", "cp_002", "handoff.md")
	if _, err := os.Stat(handoffPath); os.IsNotExist(err) {
		t.Error("handoff.md was not created")
	}

	// Verify messages.jsonl exists.
	msgsPath := filepath.Join(sessionDir, "checkpoints", "cp_002", "messages.jsonl")
	if _, err := os.Stat(msgsPath); os.IsNotExist(err) {
		t.Error("messages.jsonl was not created")
	}
}

func TestFinalizeHandoff_ReloadsContext(t *testing.T) {
	sessionDir := t.TempDir()
	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	state := newHandoffTestLoopState(sessionDir)
	state.agentCtx.RecentMessages = []agentctx.AgentMessage{
		makeTextMsg("user", "this should be replaced"),
	}

	handoffDoc := "Fresh context after handoff"
	qaTurns := []qaTurn{
		{Question: "Q1?", Answer: "A1"},
	}

	if err := state.finalizeHandoff(handoffDoc, qaTurns); err != nil {
		t.Fatalf("finalizeHandoff: %v", err)
	}

	// After reload, RecentMessages should come from the new checkpoint.
	// Expected: 1 handoff doc (user) + 1 Q (user) + 1 A (assistant) = 3 messages.
	msgs := state.agentCtx.RecentMessages
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after reload, got %d", len(msgs))
	}

	// First message should be the handoff doc.
	if msgs[0].Role != "user" {
		t.Errorf("expected first msg role 'user', got %q", msgs[0].Role)
	}
	if text := msgs[0].ExtractText(); text != handoffDoc {
		t.Errorf("expected first msg text=%q, got %q", handoffDoc, text)
	}

	// Second message should be the question.
	if msgs[1].Role != "user" {
		t.Errorf("expected second msg role 'user', got %q", msgs[1].Role)
	}

	// Third message should be the answer.
	if msgs[2].Role != "assistant" {
		t.Errorf("expected third msg role 'assistant', got %q", msgs[2].Role)
	}
}

func TestFinalizeHandoff_ResetsState(t *testing.T) {
	sessionDir := t.TempDir()
	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	state := newHandoffTestLoopState(sessionDir)
	state.hardFloorCrossed = true
	state.hardFloorTurns = 5
	state.compactionRecs = 2
	state.emptyRetries = 1
	state.guardAbortRecovery = true

	if err := state.finalizeHandoff("test doc", nil); err != nil {
		t.Fatalf("finalizeHandoff: %v", err)
	}

	if state.hardFloorCrossed {
		t.Error("hardFloorCrossed should be reset to false")
	}
	if state.hardFloorTurns != 0 {
		t.Errorf("hardFloorTurns should be 0, got %d", state.hardFloorTurns)
	}
	if state.compactionRecs != 0 {
		t.Errorf("compactionRecs should be 0, got %d", state.compactionRecs)
	}
	if state.emptyRetries != 0 {
		t.Errorf("emptyRetries should be 0, got %d", state.emptyRetries)
	}
	if state.guardAbortRecovery {
		t.Error("guardAbortRecovery should be reset to false")
	}
}

func TestFinalizeHandoff_NoSessionDir(t *testing.T) {
	state := newHandoffTestLoopState("")
	err := state.finalizeHandoff("test doc", nil)
	if err == nil {
		t.Fatal("expected error for empty session dir")
	}
}

func TestFinalizeHandoff_HandoffDocWritten(t *testing.T) {
	sessionDir := t.TempDir()
	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	state := newHandoffTestLoopState(sessionDir)

	handoffDoc := "# Detailed Handoff\n\n## Task\nImplement feature Y\n## Files\n- a.go\n- b.go"
	if err := state.finalizeHandoff(handoffDoc, nil); err != nil {
		t.Fatalf("finalizeHandoff: %v", err)
	}

	currentCp, _ := session.GetCurrentCheckpoint(sessionDir)
	handoffPath := filepath.Join(sessionDir, "checkpoints", currentCp, "handoff.md")

	data, err := os.ReadFile(handoffPath)
	if err != nil {
		t.Fatalf("reading handoff.md: %v", err)
	}

	if string(data) != handoffDoc {
		t.Errorf("handoff.md content mismatch:\n got=%q\nwant=%q", string(data), handoffDoc)
	}
}

func TestFinalizeHandoff_MultipleChainedHandoffs(t *testing.T) {
	sessionDir := t.TempDir()
	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	state := newHandoffTestLoopState(sessionDir)

	// Perform three chained handoffs.
	for i := 0; i < 3; i++ {
		doc := "Handoff round " + string(rune('A'+i))
		if err := state.finalizeHandoff(doc, []qaTurn{
			{Question: "Q" + string(rune('A'+i)), Answer: "A" + string(rune('A'+i))},
		}); err != nil {
			t.Fatalf("finalizeHandoff round %d: %v", i, err)
		}
	}

	// After 3 handoffs: cp_001 (init) → cp_002 → cp_003 → cp_004.
	currentCp, err := session.GetCurrentCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("GetCurrentCheckpoint: %v", err)
	}
	if currentCp != "cp_004" {
		t.Errorf("expected checkpoint cp_004, got %s", currentCp)
	}

	// All 4 checkpoint directories should exist.
	for i := 1; i <= 4; i++ {
		cpDir := filepath.Join(sessionDir, "checkpoints", "cp_"+padZero(i))
		if _, err := os.Stat(cpDir); os.IsNotExist(err) {
			t.Errorf("checkpoint directory %s does not exist", cpDir)
		}
	}

	// RecentMessages should have 3 messages from the last checkpoint
	// (1 handoff doc + 1 Q + 1 A).
	if len(state.agentCtx.RecentMessages) != 3 {
		t.Errorf("expected 3 messages after final handoff, got %d",
			len(state.agentCtx.RecentMessages))
	}
}

// padZero converts an int to a zero-padded 3-digit string (1 → "001").
func padZero(n int) string {
	if n < 10 {
		return "00" + string(rune('0'+n))
	}
	if n < 100 {
		return "0" + string(rune('0'+n/10)) + string(rune('0'+n%10))
	}
	return string(rune('0'+n/100)) + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
}

func TestBuildHandoffEntries(t *testing.T) {
	handoffDoc := "The handoff doc text"
	qaTurns := []qaTurn{
		{Question: "Question 1", Answer: "Answer 1"},
		{Question: "Question 2", Answer: "Answer 2"},
	}

	entries := buildHandoffEntries(handoffDoc, qaTurns)

	// Expected: 1 handoff doc + 2 * 2 Q&A = 5 entries.
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	// First entry is the handoff doc as a user message.
	if entries[0].Type != session.EntryTypeMessage {
		t.Errorf("expected type %q, got %q", session.EntryTypeMessage, entries[0].Type)
	}
	if entries[0].Message.Role != "user" {
		t.Errorf("expected first msg role 'user', got %q", entries[0].Message.Role)
	}
	if entries[0].Message.ExtractText() != handoffDoc {
		t.Errorf("expected first msg text=%q, got %q",
			handoffDoc, entries[0].Message.ExtractText())
	}

	// Second entry is Q1 (user).
	if entries[1].Message.Role != "user" {
		t.Errorf("expected Q1 role 'user', got %q", entries[1].Message.Role)
	}
	if entries[1].Message.ExtractText() != "Question 1" {
		t.Errorf("expected Q1 text, got %q", entries[1].Message.ExtractText())
	}

	// Third entry is A1 (assistant).
	if entries[2].Message.Role != "assistant" {
		t.Errorf("expected A1 role 'assistant', got %q", entries[2].Message.Role)
	}
	if entries[2].Message.ExtractText() != "Answer 1" {
		t.Errorf("expected A1 text, got %q", entries[2].Message.ExtractText())
	}

	// All entries should have unique IDs.
	seen := make(map[string]bool)
	for _, e := range entries {
		if seen[e.ID] {
			t.Errorf("duplicate entry ID: %s", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestBuildHandoffEntries_NoQA(t *testing.T) {
	handoffDoc := "Just the doc"
	entries := buildHandoffEntries(handoffDoc, nil)

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry with no Q&A, got %d", len(entries))
	}
	if entries[0].Message.ExtractText() != handoffDoc {
		t.Errorf("expected handoff doc text, got %q",
			entries[0].Message.ExtractText())
	}
}

// TestFinalizeHandoff_AgentStateWritten verifies that finalizeHandoff writes
// agent_state.json to the new checkpoint (P1-3).
func TestFinalizeHandoff_AgentStateWritten(t *testing.T) {
	sessionDir := t.TempDir()
	if err := session.InitHandoffSession(sessionDir); err != nil {
		t.Fatalf("InitHandoffSession: %v", err)
	}

	state := newHandoffTestLoopState(sessionDir)
	// Set up meaningful agent state.
	state.agentCtx.AgentState.WorkspaceRoot = "/project"
	state.agentCtx.AgentState.CurrentWorkingDir = "/project/src"
	state.agentCtx.AgentState.TotalTurns = 10

	if err := state.finalizeHandoff("test doc", nil); err != nil {
		t.Fatalf("finalizeHandoff: %v", err)
	}

	currentCp, _ := session.GetCurrentCheckpoint(sessionDir)

	// Read back agent_state.json from the new checkpoint.
	loaded, err := session.LoadHandoffCheckpointAgentState(sessionDir, currentCp)
	if err != nil {
		t.Fatalf("LoadHandoffCheckpointAgentState: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil agent state")
	}
	if loaded.WorkspaceRoot != "/project" {
		t.Errorf("expected WorkspaceRoot /project, got %q", loaded.WorkspaceRoot)
	}
	if loaded.CurrentWorkingDir != "/project/src" {
		t.Errorf("expected CurrentWorkingDir /project/src, got %q", loaded.CurrentWorkingDir)
	}
	if loaded.TotalTurns != 10 {
		t.Errorf("expected TotalTurns 10, got %d", loaded.TotalTurns)
	}
}
