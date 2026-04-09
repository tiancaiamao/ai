package agent

import (
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

func TestHasToolResultNamed(t *testing.T) {
	results := []agentctx.AgentMessage{
		agentctx.NewToolResultMessage("id1", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "output"},
		}, false),
		agentctx.NewToolResultMessage("id2", "update_llm_context", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "LLM Context updated."},
		}, false),
		agentctx.NewToolResultMessage("id3", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "file content"},
		}, false),
	}

	if !hasToolResultNamed(results, "update_llm_context") {
		t.Error("Expected to find update_llm_context")
	}
	if !hasToolResultNamed(results, "bash") {
		t.Error("Expected to find bash")
	}
	if hasToolResultNamed(results, "write") {
		t.Error("Should not find write")
	}
	if hasToolResultNamed(nil, "update_llm_context") {
		t.Error("Should return false for nil results")
	}
	if hasToolResultNamed([]agentctx.AgentMessage{}, "update_llm_context") {
		t.Error("Should return false for empty results")
	}
}
