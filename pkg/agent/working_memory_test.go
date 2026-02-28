package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestWorkingMemory_Load(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewWorkingMemory(sessionDir)

	content, err := wm.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should contain the template
	if !strings.Contains(content, "# Working Memory") {
		t.Error("Expected template content")
	}
}

func TestWorkingMemory_GetReminderUserMessage(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewWorkingMemory(sessionDir)

	// Simulate rounds without update
	for i := 0; i < 6; i++ {
		wm.IncrementRound()
	}

	// Update meta
	wm.UpdateMeta(45000, 128000, 10)

	// Get reminder message
	msg := wm.GetReminderUserMessage()

	// Check message content
	if !strings.Contains(msg, "[system message by agent, not from real user]") {
		t.Error("Expected marker for agent-generated message")
	}
	if !strings.Contains(msg, "💡") {
		t.Error("Expected reminder emoji")
	}
	if !strings.Contains(msg, "<context_meta>") {
		t.Error("Expected context_meta section")
	}
	if !strings.Contains(msg, "rounds_since_update: 6") {
		t.Error("Expected rounds count in message")
	}
}

func TestWorkingMemory_NeedsReminderMessage(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewWorkingMemory(sessionDir)

	// Initially should not need reminder
	if wm.NeedsReminderMessage() {
		t.Error("Should not need reminder initially")
	}

	// Increment rounds but not enough
	for i := 0; i < agentctx.MaxRoundsWithoutUpdate-1; i++ {
		wm.IncrementRound()
	}
	if wm.NeedsReminderMessage() {
		t.Fatalf("Should not need reminder before %d rounds", agentctx.MaxRoundsWithoutUpdate)
	}

	// Increment to threshold
	wm.IncrementRound()
	if !wm.NeedsReminderMessage() {
		t.Fatalf("Should need reminder at %d rounds", agentctx.MaxRoundsWithoutUpdate)
	}

	// Mark updated should reset
	wm.MarkUpdated(0, false)
	if wm.NeedsReminderMessage() {
		t.Error("Should not need reminder after update")
	}
}

func TestWorkingMemory_MarkUpdated(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewWorkingMemory(sessionDir)

	// Simulate rounds
	for i := 0; i < 6; i++ {
		wm.IncrementRound()
	}

	if wm.GetRoundsSinceUpdate() != 6 {
		t.Fatalf("Expected 6 rounds, got %d", wm.GetRoundsSinceUpdate())
	}

	// Mark updated
	wm.MarkUpdated(0, false)

	if wm.GetRoundsSinceUpdate() != 0 {
		t.Fatalf("Expected 0 rounds after update, got %d", wm.GetRoundsSinceUpdate())
	}
}

func TestWorkingMemory_PathMethods(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewWorkingMemory(sessionDir)

	expectedOverview := filepath.Join(sessionDir, "working-memory", "overview.md")
	if wm.GetPath() != expectedOverview {
		t.Errorf("Expected path %s, got %s", expectedOverview, wm.GetPath())
	}

	expectedDetail := filepath.Join(sessionDir, "working-memory", "detail")
	if wm.GetDetailDir() != expectedDetail {
		t.Errorf("Expected detail dir %s, got %s", expectedDetail, wm.GetDetailDir())
	}
}

func TestWorkingMemory_DirectoryCreation(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewWorkingMemory(sessionDir)

	// Load should create directories
	_, err := wm.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check directories exist
	wmDir := filepath.Join(sessionDir, "working-memory")
	if _, err := os.Stat(wmDir); os.IsNotExist(err) {
		t.Error("working-memory directory not created")
	}

	detailDir := filepath.Join(sessionDir, "working-memory", "detail")
	if _, err := os.Stat(detailDir); os.IsNotExist(err) {
		t.Error("detail directory not created")
	}

	overviewPath := filepath.Join(sessionDir, "working-memory", "overview.md")
	if _, err := os.Stat(overviewPath); os.IsNotExist(err) {
		t.Error("overview.md not created")
	}
}

func TestWorkingMemory_ContextMeta(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewWorkingMemory(sessionDir)

	wm.UpdateMeta(50000, 128000, 25)

	meta := wm.GetMeta()

	if meta.TokensUsed != 50000 {
		t.Errorf("Expected tokens_used 50000, got %d", meta.TokensUsed)
	}
	if meta.TokensMax != 128000 {
		t.Errorf("Expected tokens_max 128000, got %d", meta.TokensMax)
	}
	expectedPercent := float64(50000) / float64(128000) * 100
	if meta.TokensPercent != expectedPercent {
		t.Errorf("Expected tokens_percent %.2f, got %.2f", expectedPercent, meta.TokensPercent)
	}
	if meta.MessagesInHistory != 25 {
		t.Errorf("Expected messages_in_history 25, got %d", meta.MessagesInHistory)
	}
}
