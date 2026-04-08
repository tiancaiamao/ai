package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestLLMContext_Load(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewLLMContext(sessionDir)

	content, err := wm.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should contain the template
	if !strings.Contains(content, "# LLM Context") {
		t.Error("Expected template content")
	}
}


func TestLLMContext_PathMethods(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewLLMContext(sessionDir)

	expectedOverview := filepath.Join(sessionDir, "llm-context", "overview.md")
	if wm.GetPath() != expectedOverview {
		t.Errorf("Expected path %s, got %s", expectedOverview, wm.GetPath())
	}
}

func TestLLMContext_Directories(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewLLMContext(sessionDir)

	// Load should create directories
	_, err := wm.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check directories exist
	wmDir := filepath.Join(sessionDir, "llm-context")
	if _, err := os.Stat(wmDir); os.IsNotExist(err) {
		t.Error("llm-context directory not created")
	}

	detailDir := filepath.Join(sessionDir, "llm-context", "detail")
	if _, err := os.Stat(detailDir); os.IsNotExist(err) {
		t.Error("detail directory not created")
	}

	overviewPath := filepath.Join(sessionDir, "llm-context", "overview.md")
	if _, err := os.Stat(overviewPath); os.IsNotExist(err) {
		t.Error("overview.md not created")
	}
}

func TestLLMContext_ContextMeta(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewLLMContext(sessionDir)

	wm.SetMeta(50000, 128000, 25)

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