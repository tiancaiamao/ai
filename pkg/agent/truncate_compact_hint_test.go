package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

type hintTestCompactor struct {
	compactCalls int
	result       *CompactionResult
	err          error
}

func (m *hintTestCompactor) ShouldCompact(_ []agentctx.AgentMessage) bool {
	return false
}

func (m *hintTestCompactor) Compact(_ []agentctx.AgentMessage, _ string) (*CompactionResult, error) {
	m.compactCalls++
	return m.result, m.err
}

func TestTruncateCompactHintParseSections(t *testing.T) {
	processor := NewTruncateCompactHint(nil)
	content := `
## TRUNCATE
call_a, call_b
call_c

## COMPACT
target: all
strategy: archive
keep_recent: 10
archive_to: llm-context/detail/session-summary.md
confidence_range: 70%-90%
`

	sections, err := processor.parseSections(content)
	if err != nil {
		t.Fatalf("parseSections failed: %v", err)
	}
	if len(sections.Truncate) != 3 {
		t.Fatalf("expected 3 truncate ids, got %d", len(sections.Truncate))
	}
	if sections.Compact == nil {
		t.Fatal("expected compact section")
	}
	if sections.Compact.Target != "all" {
		t.Fatalf("expected target=all, got %q", sections.Compact.Target)
	}
	if sections.Compact.KeepRecent != 10 {
		t.Fatalf("expected keep_recent=10, got %d", sections.Compact.KeepRecent)
	}
	if sections.Compact.ConfidenceMin != 0.7 || sections.Compact.ConfidenceMax != 0.9 {
		t.Fatalf("expected confidence range [0.7,0.9], got [%v,%v]", sections.Compact.ConfidenceMin, sections.Compact.ConfidenceMax)
	}
}

func TestTruncateCompactHintProcessTruncate(t *testing.T) {
	sessionDir := t.TempDir()
	hintPath := filepath.Join(sessionDir, "llm-context", "truncate-compact-hint.md")
	if err := os.MkdirAll(filepath.Dir(hintPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(hintPath, []byte("## TRUNCATE\ncall_a\n"), 0o644); err != nil {
		t.Fatalf("write hint failed: %v", err)
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.LLMContext = agentctx.NewLLMContext(sessionDir)
	agentCtx.Messages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("request"),
		agentctx.NewToolResultMessage("call_a", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "hello"},
		}, false),
		agentctx.NewToolResultMessage("call_b", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "world"},
		}, false),
	}

	processor := NewTruncateCompactHint(nil)
	result, err := processor.Process(context.Background(), agentCtx)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.TruncatedCount != 1 {
		t.Fatalf("expected truncated_count=1, got %d", result.TruncatedCount)
	}
	if result.CompactPerformed {
		t.Fatal("did not expect compact to be performed")
	}

	text := agentCtx.Messages[1].ExtractText()
	if text == "" || !isTruncatedAgentToolTag(text) {
		t.Fatalf("expected truncated tag content, got %q", text)
	}
	if _, err := os.Stat(hintPath); !os.IsNotExist(err) {
		t.Fatalf("expected hint file to be deleted, stat err=%v", err)
	}
}

func TestTruncateCompactHintProcessCompactUsesCompactor(t *testing.T) {
	sessionDir := t.TempDir()
	hintPath := filepath.Join(sessionDir, "llm-context", "truncate-compact-hint.md")
	if err := os.MkdirAll(filepath.Dir(hintPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(hintPath, []byte("## COMPACT\ntarget: all\nconfidence: 100%\n"), 0o644); err != nil {
		t.Fatalf("write hint failed: %v", err)
	}

	compactedMessages := []agentctx.AgentMessage{agentctx.NewUserMessage("compacted")}
	mockCompactor := &hintTestCompactor{
		result: &CompactionResult{
			Summary:  "summary",
			Messages: compactedMessages,
		},
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.LLMContext = agentctx.NewLLMContext(sessionDir)
	agentCtx.Messages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("before"),
		agentctx.NewAssistantMessage(),
	}

	processor := NewTruncateCompactHint(mockCompactor)
	result, err := processor.Process(context.Background(), agentCtx)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if !result.CompactPerformed {
		t.Fatal("expected compact to be performed")
	}
	if mockCompactor.compactCalls != 1 {
		t.Fatalf("expected compactor call count=1, got %d", mockCompactor.compactCalls)
	}
	if agentCtx.LastCompactionSummary != "summary" {
		t.Fatalf("expected summary to be updated, got %q", agentCtx.LastCompactionSummary)
	}
	if len(agentCtx.Messages) != 1 || agentCtx.Messages[0].ExtractText() != "compacted" {
		t.Fatalf("unexpected compacted messages: %+v", agentCtx.Messages)
	}
}

func TestTruncateCompactHintProcessCompactSkipsWhenConfidenceZero(t *testing.T) {
	sessionDir := t.TempDir()
	hintPath := filepath.Join(sessionDir, "llm-context", "truncate-compact-hint.md")
	if err := os.MkdirAll(filepath.Dir(hintPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(hintPath, []byte("## COMPACT\ntarget: all\nconfidence: 0%\n"), 0o644); err != nil {
		t.Fatalf("write hint failed: %v", err)
	}

	mockCompactor := &hintTestCompactor{
		result: &CompactionResult{
			Summary:  "summary",
			Messages: []agentctx.AgentMessage{agentctx.NewUserMessage("compacted")},
		},
	}
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.LLMContext = agentctx.NewLLMContext(sessionDir)
	agentCtx.Messages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("before"),
		agentctx.NewAssistantMessage(),
	}

	processor := NewTruncateCompactHint(mockCompactor)
	result, err := processor.Process(context.Background(), agentCtx)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.CompactPerformed {
		t.Fatal("expected compact to be skipped")
	}
	if mockCompactor.compactCalls != 0 {
		t.Fatalf("expected compactor not called, got %d", mockCompactor.compactCalls)
	}
}

func TestTruncateCompactHintSkipsAlreadyTruncatedOutput(t *testing.T) {
	sessionDir := t.TempDir()
	hintPath := filepath.Join(sessionDir, "llm-context", "truncate-compact-hint.md")
	if err := os.MkdirAll(filepath.Dir(hintPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(hintPath, []byte("## TRUNCATE\ncall_x\n"), 0o644); err != nil {
		t.Fatalf("write hint failed: %v", err)
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.LLMContext = agentctx.NewLLMContext(sessionDir)
	already := `<agent:tool id="call_x" name="read" chars="123" truncated="true" />`
	agentCtx.Messages = []agentctx.AgentMessage{
		agentctx.NewToolResultMessage("call_x", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: already},
		}, false),
	}

	processor := NewTruncateCompactHint(nil)
	result, err := processor.Process(context.Background(), agentCtx)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.TruncatedCount != 0 {
		t.Fatalf("expected truncated_count=0, got %d", result.TruncatedCount)
	}
	if got := agentCtx.Messages[0].ExtractText(); got != already {
		t.Fatalf("expected message unchanged, got %q", got)
	}
}

func TestTruncateCompactHintCanTruncateStaleTaggedOutput(t *testing.T) {
	sessionDir := t.TempDir()
	hintPath := filepath.Join(sessionDir, "llm-context", "truncate-compact-hint.md")
	if err := os.MkdirAll(filepath.Dir(hintPath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(hintPath, []byte("## TRUNCATE\ncall_s\n"), 0o644); err != nil {
		t.Fatalf("write hint failed: %v", err)
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.LLMContext = agentctx.NewLLMContext(sessionDir)
	staleContent := `<agent:tool id="call_s" name="bash" chars="456" stale="true" />` + "\n" + "payload"
	agentCtx.Messages = []agentctx.AgentMessage{
		agentctx.NewToolResultMessage("call_s", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: staleContent},
		}, false),
	}

	processor := NewTruncateCompactHint(nil)
	result, err := processor.Process(context.Background(), agentCtx)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}
	if result.TruncatedCount != 1 {
		t.Fatalf("expected truncated_count=1, got %d", result.TruncatedCount)
	}
	text := agentCtx.Messages[0].ExtractText()
	if !isTruncatedAgentToolTag(text) {
		t.Fatalf("expected truncated tag, got %q", text)
	}
	if !strings.Contains(text, `chars="456"`) {
		t.Fatalf("expected original chars preserved as 456, got %q", text)
	}
}
