package compact

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestSaveArchivedMessages_EmptySessionDir(t *testing.T) {
	msgs := []agentctx.AgentMessage{agentctx.NewUserMessage("hello")}
	if got := saveArchivedMessages("", msgs); got != "" {
		t.Errorf("expected empty path for empty sessionDir, got %q", got)
	}
}

func TestSaveArchivedMessages_EmptyMessages(t *testing.T) {
	dir := t.TempDir()
	if got := saveArchivedMessages(dir, nil); got != "" {
		t.Errorf("expected empty path for no messages, got %q", got)
	}
}

func TestSaveArchivedMessages_WritesFile(t *testing.T) {
	dir := t.TempDir()
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("first"),
		agentctx.NewUserMessage("second"),
	}

	path := saveArchivedMessages(dir, msgs)
	if path == "" {
		t.Fatal("expected non-empty path")
	}

	if !strings.HasSuffix(path, "archived_00001.jsonl") {
		t.Errorf("expected archived_00001.jsonl suffix, got %q", filepath.Base(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read archive: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var msg agentctx.AgentMessage
	if err := json.Unmarshal([]byte(lines[0]), &msg); err != nil {
		t.Fatalf("failed to decode first message: %v", err)
	}
	if msg.ExtractText() != "first" {
		t.Errorf("expected 'first', got %q", msg.ExtractText())
	}
}

func TestSaveArchivedMessages_SequentialNumbering(t *testing.T) {
	dir := t.TempDir()
	msgs := []agentctx.AgentMessage{agentctx.NewUserMessage("a")}

	p1 := saveArchivedMessages(dir, msgs)
	p2 := saveArchivedMessages(dir, msgs)

	if !strings.HasSuffix(p1, "archived_00001.jsonl") {
		t.Errorf("first archive should be 00001, got %q", filepath.Base(p1))
	}
	if !strings.HasSuffix(p2, "archived_00002.jsonl") {
		t.Errorf("second archive should be 00002, got %q", filepath.Base(p2))
	}
}

// TestCompact_SummaryContainsArchivePath verifies that after Compact, the
// summary message includes the archive path and the archive file exists.
func TestCompact_SummaryContainsArchivePath(t *testing.T) {
	dir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseTextResponse("mock summary content"))
	}))
	defer server.Close()

	cfg := &Config{
		MaxMessages:      10,
		MaxTokens:        1000,
		KeepRecent:       2,
		KeepRecentTokens: 100,
		AutoCompact:      true,
	}

	model := llm.Model{
		ID:            "test",
		BaseURL:       server.URL,
		API:           "openai",
		ContextWindow: 200000,
	}

	compactor := NewCompactor(cfg, model, "key", "sys", 200000, dir)

	messages := make([]agentctx.AgentMessage, 10)
	for i := range messages {
		messages[i] = agentctx.NewUserMessage(strings.Repeat("x", 200))
	}

	agentCtx := &agentctx.AgentContext{
		RecentMessages: messages,
		AgentState:     &agentctx.AgentState{},
	}

	_, err := compactor.Compact(t.Context(), agentCtx)
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}

	// First message should be the compaction summary with archive path
	if len(agentCtx.RecentMessages) == 0 {
		t.Fatal("no messages after compact")
	}
	summaryText := agentCtx.RecentMessages[0].ExtractText()

	if !strings.Contains(summaryText, "mock summary content") {
		t.Errorf("summary should contain LLM-generated content, got: %s", summaryText)
	}
	if !strings.Contains(summaryText, "archived_") {
		t.Errorf("summary should contain archive path reference, got: %s", summaryText)
	}
	if !strings.Contains(summaryText, dir) {
		t.Errorf("summary should contain session dir path, got: %s", summaryText)
	}

	// Archive file should exist with valid JSONL content
	compactionsDir := filepath.Join(dir, "compactions")
	entries, err := os.ReadDir(compactionsDir)
	if err != nil {
		t.Fatalf("failed to read compactions dir: %v", err)
	}

	found := false
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "archived_") {
			continue
		}
		found = true
		data, err := os.ReadFile(filepath.Join(compactionsDir, e.Name()))
		if err != nil {
			t.Fatalf("failed to read archive file: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) == 0 {
			t.Error("archive file is empty")
		}
		// Each line should be valid JSON
		var msg agentctx.AgentMessage
		if err := json.Unmarshal([]byte(lines[0]), &msg); err != nil {
			t.Errorf("archive line is not valid AgentMessage JSON: %v", err)
		}
		break
	}
	if !found {
		t.Error("no archived_*.jsonl file found in compactions dir")
	}
}
