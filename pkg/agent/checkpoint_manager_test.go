package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestCheckpointManager_CreateSnapshot(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Create a snapshot
	llmContext := "# Current Task\nTest task"
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("Hello"),
		agentctx.NewAssistantMessage(),
		agentctx.NewUserMessage("World"),
	}
	agentCtx := &agentctx.AgentContext{
		RecentMessages: messages,
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}

	turn, err := mgr.CreateSnapshot(agentCtx, llmContext, 5)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	if turn != 5 {
		t.Errorf("Expected turn 5, got %d", turn)
	}

	// Verify checkpoint directory exists
	checkpointsDir := filepath.Join(tmpDir, "checkpoints")
	if _, err := os.Stat(checkpointsDir); os.IsNotExist(err) {
		t.Error("Checkpoints directory should exist")
	}

	// Verify current/ symlink exists
	currentLink := filepath.Join(tmpDir, "current")
	if _, err := os.Lstat(currentLink); os.IsNotExist(err) {
		t.Error("current/ symlink should exist")
	}
}

func TestCheckpointManager_JournalAppend(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Append messages
	msg1 := agentctx.NewUserMessage("Message 1")
	if err := mgr.AppendMessage(msg1); err != nil {
		t.Fatalf("Failed to append message: %v", err)
	}

	msg2 := agentctx.NewAssistantMessage()
	if err := mgr.AppendMessage(msg2); err != nil {
		t.Fatalf("Failed to append message: %v", err)
	}

	// Verify journal file exists
	journalPath := filepath.Join(tmpDir, "messages.jsonl")
	if _, err := os.Stat(journalPath); os.IsNotExist(err) {
		t.Error("Journal file should exist")
	}
}

func TestCheckpointManager_Reconstruct(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Create initial snapshot
	llmContext := "# Current Task\nInitial task"
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("Initial"),
	}

	agentCtx := &agentctx.AgentContext{
		RecentMessages: messages,
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}
	_, _ = mgr.CreateSnapshot(agentCtx, llmContext, 1)

	// Append more messages
	for i := 0; i < 3; i++ {
		msg := agentctx.NewUserMessage("Message")
		if err := mgr.AppendMessage(msg); err != nil {
			t.Fatalf("Failed to append message: %v", err)
		}
	}

	// Reconstruct
	recoveredLLMContext, recoveredMessages, recoveredTurns, err := mgr.Reconstruct()
	if err != nil {
		t.Fatalf("Failed to reconstruct: %v", err)
	}

	if recoveredLLMContext != llmContext {
		t.Errorf("LLMContext mismatch: expected %q, got %q", llmContext, recoveredLLMContext)
	}

	// Should have 1 + 3 = 4 messages
	if len(recoveredMessages) != 4 {
		t.Errorf("Expected 4 messages, got %d", len(recoveredMessages))
	}

	if recoveredTurns != 1 {
		t.Errorf("Expected turns 1, got %d", recoveredTurns)
	}
}

func TestCheckpointManager_ShouldCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// ShouldCheckpoint now always returns true when enabled (event-driven)
	if !mgr.ShouldCheckpoint() {
		t.Error("ShouldCheckpoint should return true when enabled")
	}
}

func TestCheckpointManager_ShouldCheckpoint_Disabled(t *testing.T) {
	mgr, err := NewAgentContextCheckpointManager("")
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}

	// ShouldCheckpoint returns false when disabled (empty sessionDir)
	if mgr.ShouldCheckpoint() {
		t.Error("ShouldCheckpoint should return false when disabled")
	}
}

func TestCheckpointManager_CreateSnapshot_EmptyLLMContext_CarriesForward(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Step 1: Create initial checkpoint with non-empty LLMContext
	initialContext := "# Current Task\nImportant context that must not be lost"
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{agentctx.NewUserMessage("Hello")},
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}

	_, err = mgr.CreateSnapshot(agentCtx, initialContext, 5)
	if err != nil {
		t.Fatalf("Failed to create initial snapshot: %v", err)
	}

	// Step 2: Create snapshot with empty LLMContext (simulates truncate-without-update)
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("More messages"))
	_, err = mgr.CreateSnapshot(agentCtx, "", 8)
	if err != nil {
		t.Fatalf("Failed to create snapshot with empty context: %v", err)
	}

	// Step 3: Reconstruct and verify context was carried forward
	recoveredLLMContext, _, _, err := mgr.Reconstruct()
	if err != nil {
		t.Fatalf("Failed to reconstruct: %v", err)
	}

	if recoveredLLMContext != initialContext {
		t.Errorf("LLMContext should be carried forward from previous checkpoint.\nExpected: %q\nGot: %q", initialContext, recoveredLLMContext)
	}

	// Step 4: Verify the checkpoint file on disk has the carried-forward content
	latestInfo, err := agentctx.LoadLatestCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load latest checkpoint info: %v", err)
	}
	onDisk, err := agentctx.LoadCheckpointLLMContext(filepath.Join(tmpDir, latestInfo.Path))
	if err != nil {
		t.Fatalf("Failed to load LLM context from disk: %v", err)
	}
	if onDisk != initialContext {
		t.Errorf("On-disk LLMContext should match carried-forward content.\nExpected: %q\nGot: %q", initialContext, onDisk)
	}
}

func TestCheckpointManager_CreateSnapshot_NonEmptyContext_Unchanged(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Create first checkpoint with content
	firstContext := "First context"
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{},
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}
	_, err = mgr.CreateSnapshot(agentCtx, firstContext, 1)
	if err != nil {
		t.Fatalf("Failed to create first snapshot: %v", err)
	}

	// Create second checkpoint with different content — should NOT carry forward
	secondContext := "Second context (updated)"
	_, err = mgr.CreateSnapshot(agentCtx, secondContext, 3)
	if err != nil {
		t.Fatalf("Failed to create second snapshot: %v", err)
	}

	// Reconstruct should return the second (latest) context
	recoveredLLMContext, _, _, err := mgr.Reconstruct()
	if err != nil {
		t.Fatalf("Failed to reconstruct: %v", err)
	}

	if recoveredLLMContext != secondContext {
		t.Errorf("LLMContext should be the latest non-empty value.\nExpected: %q\nGot: %q", secondContext, recoveredLLMContext)
	}
}

func TestCheckpointManager_CreateSnapshot_EmptyFirst_NoPrevious(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Create very first snapshot with empty context — no previous to carry forward from
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{},
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}

	_, err = mgr.CreateSnapshot(agentCtx, "", 0)
	if err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	// Should succeed but context stays empty (no previous to carry forward from)
	recoveredLLMContext, _, _, err := mgr.Reconstruct()
	if err != nil {
		t.Fatalf("Failed to reconstruct: %v", err)
	}

	if recoveredLLMContext != "" {
		t.Errorf("LLMContext should remain empty when no previous checkpoint exists, got: %q", recoveredLLMContext)
	}
}

// TestSavePreCompactionCheckpoint_EmptyMessages_SkipsCheckpoint verifies
// the first defense layer: savePreCompactionCheckpoint skips when there are
// no messages to checkpoint, preventing creation of empty checkpoints. The
// guard keys off message count, NOT LLMContext (which may legitimately be
// empty in the delta-compaction design).
func TestSavePreCompactionCheckpoint_EmptyMessages_SkipsCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Create an initial checkpoint with real content
	initialContext := "# Important context\nDo not lose this"
	agentCtx := &agentctx.AgentContext{
		LLMContext:     initialContext,
		RecentMessages: []agentctx.AgentMessage{agentctx.NewUserMessage("Hello")},
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}
	_, err = mgr.CreateSnapshot(agentCtx, initialContext, 1)
	if err != nil {
		t.Fatalf("Failed to create initial snapshot: %v", err)
	}

	// Simulate a state with no messages (nothing useful to checkpoint)
	agentCtx.RecentMessages = nil

	// Create a loopState with a compactor that would normally trigger
	ls := &loopState{
		config: &LoopConfig{
			Compactors: []agentctx.Compactor{
				&alwaysCompactCompactor{},
			},
		},
		agentCtx:      agentCtx,
		checkpointMgr: mgr,
		turnCount:     5,
	}

	// Call savePreCompactionCheckpoint — it should skip because there are no messages
	ls.savePreCompactionCheckpoint("pre_llm_threshold")

	// Verify no new checkpoint was created by checking the count
	idx, err := agentctx.LoadCheckpointIndex(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load checkpoint index: %v", err)
	}
	if len(idx.Checkpoints) != 1 {
		t.Errorf("Expected 1 checkpoint (initial only), got %d", len(idx.Checkpoints))
	}
}

// TestSavePreCompactionCheckpoint_EmptyLLMContext_NonEmptyMessages_CreatesCheckpoint
// verifies the key behavior change from the checkpoint-guard fix: an empty
// LLMContext no longer blocks checkpointing when there are messages. This is
// essential in the delta-compaction design where LLMContext is not actively
// written. CreateSnapshot carries forward any previous LLMContext.
func TestSavePreCompactionCheckpoint_EmptyLLMContext_NonEmptyMessages_CreatesCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Create an initial checkpoint with non-empty LLMContext (to be carried forward)
	initialContext := "# Important context\nDo not lose this"
	agentCtx := &agentctx.AgentContext{
		LLMContext:     initialContext,
		RecentMessages: []agentctx.AgentMessage{agentctx.NewUserMessage("Hello")},
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}
	_, err = mgr.CreateSnapshot(agentCtx, initialContext, 1)
	if err != nil {
		t.Fatalf("Failed to create initial snapshot: %v", err)
	}

	// Empty LLMContext but non-empty messages — checkpoint MUST be created.
	agentCtx.LLMContext = ""
	agentCtx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("Hello"),
		agentctx.NewUserMessage("World"),
	}

	ls := &loopState{
		config: &LoopConfig{
			Compactors: []agentctx.Compactor{
				&alwaysCompactCompactor{},
			},
		},
		agentCtx:      agentCtx,
		checkpointMgr: mgr,
		turnCount:     5,
	}

	ls.savePreCompactionCheckpoint("pre_llm_threshold")

	// A new checkpoint should have been created (2 total)
	idx, err := agentctx.LoadCheckpointIndex(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load checkpoint index: %v", err)
	}
	if len(idx.Checkpoints) != 2 {
		t.Errorf("Expected 2 checkpoints (initial + new), got %d", len(idx.Checkpoints))
	}

	// The carried-forward LLMContext should be preserved by CreateSnapshot.
	recoveredLLMContext, _, _, err := mgr.Reconstruct()
	if err != nil {
		t.Fatalf("Failed to reconstruct: %v", err)
	}
	if recoveredLLMContext != initialContext {
		t.Errorf("LLMContext should be carried forward.\nExpected: %q\nGot: %q", initialContext, recoveredLLMContext)
	}
}

// TestSavePreCompactionCheckpoint_NonEmptyLLMContext_CreatesCheckpoint verifies
// that savePreCompactionCheckpoint still works correctly when LLMContext is non-empty.
func TestSavePreCompactionCheckpoint_NonEmptyLLMContext_CreatesCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create checkpoint manager: %v", err)
	}
	defer mgr.Close()

	// Create initial checkpoint
	agentCtx := &agentctx.AgentContext{
		LLMContext:     "# Task context",
		RecentMessages: []agentctx.AgentMessage{agentctx.NewUserMessage("Hello")},
		AgentState:     agentctx.NewAgentState("test-session", "/workspace"),
	}
	_, err = mgr.CreateSnapshot(agentCtx, "# Task context", 1)
	if err != nil {
		t.Fatalf("Failed to create initial snapshot: %v", err)
	}

	// Now with non-empty LLMContext, savePreCompactionCheckpoint should create a new one
	ls := &loopState{
		config: &LoopConfig{
			Compactors: []agentctx.Compactor{
				&alwaysCompactCompactor{},
			},
		},
		agentCtx:      agentCtx,
		checkpointMgr: mgr,
		turnCount:     3,
	}

	ls.savePreCompactionCheckpoint("pre_llm_threshold")

	// Verify a new checkpoint was created
	idx, err := agentctx.LoadCheckpointIndex(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load checkpoint index: %v", err)
	}
	if len(idx.Checkpoints) != 2 {
		t.Errorf("Expected 2 checkpoints, got %d", len(idx.Checkpoints))
	}
}

// alwaysCompactCompactor is a test compactor that always reports ShouldCompact=true.
type alwaysCompactCompactor struct{}

func (a *alwaysCompactCompactor) ShouldCompact(_ context.Context, _ *agentctx.AgentContext) bool {
	return true
}

func (a *alwaysCompactCompactor) Compact(_ *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	return &agentctx.CompactionResult{Summary: "test compaction"}, nil
}

func (a *alwaysCompactCompactor) CalculateDynamicThreshold() int {
	return 0
}

// TestCreateSnapshot_PersistsJournalLength verifies that CreateSnapshot saves
// MessageIndex equal to the current journal length, so that a subsequent
// Reconstruct() can correctly replay only entries written AFTER the checkpoint.
//
// Bug (pre-fix): CreateSnapshot uses m.messageIndex (incremented only by
// AgentContextCheckpointManager.AppendMessage), but production code never
// calls AppendMessage — it writes via Session.AppendMessage instead. The
// result is that every saved checkpoint has MessageIndex=0, causing
// Reconstruct() to replay ALL journal entries on top of the snapshot and
// producing duplicated messages.
func TestCreateSnapshot_PersistsJournalLength(t *testing.T) {
	sessionDir := t.TempDir()

	mgr, err := NewAgentContextCheckpointManager(sessionDir)
	if err != nil {
		t.Fatalf("NewAgentContextCheckpointManager: %v", err)
	}
	defer mgr.Close()

	// Simulate the production write path: messages go directly into the
	// session journal, bypassing checkpointMgr.AppendMessage. Append 3 user
	// messages via Journal (same on-disk format as Session writes).
	for i := 0; i < 3; i++ {
		if err := mgr.journal.AppendMessage(agentctx.NewUserMessage("pre")); err != nil {
			t.Fatalf("AppendMessage %d: %v", i, err)
		}
	}

	// Create a snapshot — this should record MessageIndex = 3 (current
	// journal length), so future replays know where the snapshot ends.
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{agentctx.NewUserMessage("dummy")},
		AgentState:     agentctx.NewAgentState("test", "/workspace"),
	}
	if _, err := mgr.CreateSnapshot(agentCtx, "# ctx", 1); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}

	// Load the saved checkpoint and verify MessageIndex.
	cpInfo, err := agentctx.LoadLatestCheckpoint(sessionDir)
	if err != nil {
		t.Fatalf("LoadLatestCheckpoint: %v", err)
	}
	if cpInfo.MessageIndex != 3 {
		t.Errorf("checkpoint.MessageIndex = %d, want 3 (journal length at snapshot time)", cpInfo.MessageIndex)
	}

	// Additionally verify by appending 2 more messages and reconstructing:
	// we should get 1 (snapshot) + 2 (replayed) = 3 messages, NOT 1 + 5 = 6.
	for i := 0; i < 2; i++ {
		if err := mgr.journal.AppendMessage(agentctx.NewUserMessage("post")); err != nil {
			t.Fatalf("AppendMessage post %d: %v", i, err)
		}
	}

	_, msgs, _, err := mgr.Reconstruct()
	if err != nil {
		t.Fatalf("Reconstruct: %v", err)
	}
	// Snapshot has 1 message ("dummy"); replay adds 2 post-checkpoint entries.
	// If MessageIndex were 0 (the bug), replay would add all 5 journal messages
	// and we'd see 1 + 5 = 6.
	if got := len(msgs); got != 3 {
		t.Errorf("after Reconstruct: message count = %d, want 3 (1 snapshot + 2 replayed)", got)
		for i, m := range msgs {
			t.Logf("  msg[%d] role=%s text=%q", i, m.Role, firstText(m))
		}
	}
}
