package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestCompactHistoryTool_Name(t *testing.T) {
	tool := NewCompactHistoryTool(nil, nil, llm.Model{}, "", "")
	if tool.Name() != "compact_history" {
		t.Errorf("Expected tool name 'compact_history', got '%s'", tool.Name())
	}
}

func TestCompactHistoryTool_Description(t *testing.T) {
	tool := NewCompactHistoryTool(nil, nil, llm.Model{}, "", "")
	desc := tool.Description()

	// Check that description contains key information
	if !contains(desc, "conversation") {
		t.Error("Description should mention 'conversation' target")
	}
	if !contains(desc, "tools") {
		t.Error("Description should mention 'tools' target")
	}
	if !contains(desc, "all") {
		t.Error("Description should mention 'all' target")
	}
	if !contains(desc, "context_meta") {
		t.Error("Description should mention 'context_meta'")
	}
	if !contains(desc, "20%") || !contains(desc, "40%") || !contains(desc, "60%") {
		t.Error("Description should mention compression thresholds")
	}
}

func TestCompactHistoryTool_Parameters(t *testing.T) {
	tool := NewCompactHistoryTool(nil, nil, llm.Model{}, "", "")
	params := tool.Parameters()

	// Check required parameters
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters should have properties")
	}

	// Check target parameter
	if _, ok := props["target"]; !ok {
		t.Error("Parameters should have 'target' property")
	}

	// Check strategy parameter
	if _, ok := props["strategy"]; !ok {
		t.Error("Parameters should have 'strategy' property")
	}

	// Check keep_recent parameter
	if _, ok := props["keep_recent"]; !ok {
		t.Error("Parameters should have 'keep_recent' property")
	}

	// Check archive_to parameter
	if _, ok := props["archive_to"]; !ok {
		t.Error("Parameters should have 'archive_to' property")
	}

	// Check required fields
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatal("Parameters should have required fields")
	}
	if len(required) != 1 || required[0] != "target" {
		t.Errorf("Expected required=['target'], got %v", required)
	}
}

func TestCompactHistoryTool_Execute_InvalidTarget(t *testing.T) {
	tool := NewCompactHistoryTool(nil, nil, llm.Model{}, "", "")
	ctx := context.Background()

	args := map[string]any{
		"target": "invalid",
	}

	_, err := tool.Execute(ctx, args)
	if err == nil {
		t.Error("Expected error for invalid target")
	}
}

func TestCompactHistoryTool_Execute_MissingTarget(t *testing.T) {
	tool := NewCompactHistoryTool(nil, nil, llm.Model{}, "", "")
	ctx := context.Background()

	args := map[string]any{}

	_, err := tool.Execute(ctx, args)
	if err == nil {
		t.Error("Expected error for missing target")
	}
}

func TestCompactHistoryTool_Execute_CompactConversation(t *testing.T) {
	// Create mock agent context
	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "message 1"}}},
			{Role: "assistant", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "response 1"}}},
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "message 2"}}},
			{Role: "assistant", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "response 2"}}},
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "message 3"}}},
		},
	}

	// Create mock compactor
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)

	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")
	ctx := context.Background()

	args := map[string]any{
		"target":      "conversation",
		"keep_recent": 2,
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("Expected non-empty result")
	}

	textResult, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("Expected TextContent result")
	}

	// Check that result contains expected fields
	if !contains(textResult.Text, "conversation") {
		t.Error("Result should mention 'conversation'")
	}
}

func TestCompactHistoryTool_Execute_CompactTools(t *testing.T) {
	// Create mock agent context with tool results
	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			{Role: "toolResult", Content: []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: "Very long tool output that should be truncated. " + string(make([]byte, 3000))},
			}},
			{Role: "toolResult", Content: []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: "Short tool output"},
			}},
		},
	}

	// Create mock compactor
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)

	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")
	ctx := context.Background()

	args := map[string]any{
		"target":      "tools",
		"keep_recent": 1,
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("Expected non-empty result")
	}

	textResult, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("Expected TextContent result")
	}

	// Check that result mentions tools compaction
	if !contains(textResult.Text, "tools") {
		t.Error("Result should mention 'tools'")
	}
}

func TestCompactHistoryTool_Execute_CompactAll(t *testing.T) {
	// Create mock agent context with both conversation and tool results
	// Need more messages to trigger compaction
	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "message 1"}}},
			{Role: "assistant", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "response 1"}}},
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "message 2"}}},
			{Role: "toolResult", Content: []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: "tool output 1"},
			}},
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "message 3"}}},
			{Role: "toolResult", Content: []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: string(make([]byte, 3000))}, // Large output
			}},
		},
	}

	// Create mock compactor
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)

	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")
	ctx := context.Background()

	args := map[string]any{
		"target":      "all",
		"keep_recent": 2,
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("Expected non-empty result")
	}

	textResult, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("Expected TextContent result")
	}

	// Check that result is valid JSON
	if !contains(textResult.Text, "target") {
		t.Error("Result should contain 'target'")
	}
	if !contains(textResult.Text, "all") {
		t.Error("Result should contain 'all'")
	}
}

func TestCompactHistoryTool_Execute_KeepRecentAll(t *testing.T) {
	// Create mock agent context with fewer messages than keep_recent
	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "message 1"}}},
		},
	}

	// Create mock compactor
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)

	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")
	ctx := context.Background()

	args := map[string]any{
		"target":      "conversation",
		"keep_recent": 5,
	}

	result, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("Expected non-empty result")
	}

	textResult, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("Expected TextContent result")
	}

	// Check that result indicates not enough messages
	if !contains(textResult.Text, "Not enough messages") {
		t.Error("Result should indicate not enough messages to compact")
	}
}

func TestCompactHistoryTool_Execute_ArchiveWritesFile(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agent.NewWorkingMemory(sessionDir)
	if _, err := wm.Load(); err != nil {
		t.Fatalf("failed to initialize working memory: %v", err)
	}

	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			{Role: "toolResult", Content: []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: "Very long tool output that should be archived. " + string(make([]byte, 3000))},
			}},
			{Role: "toolResult", Content: []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: "recent output"},
			}},
		},
		WorkingMemory: wm,
	}

	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)
	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")

	resultBlocks, err := tool.Execute(context.Background(), map[string]any{
		"target":      "tools",
		"strategy":    "archive",
		"keep_recent": 1,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	text, ok := resultBlocks[0].(agent.TextContent)
	if !ok {
		t.Fatal("expected text content result")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(text.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	archivedTo, ok := parsed["archived_to"].(string)
	if !ok || archivedTo == "" {
		t.Fatalf("expected archived_to path, got: %v", parsed["archived_to"])
	}
	if _, err := os.Stat(archivedTo); err != nil {
		t.Fatalf("archive file not found at %s: %v", archivedTo, err)
	}

	if filepath.Dir(archivedTo) != wm.GetDetailDir() {
		t.Fatalf("expected archive in detail dir %s, got %s", wm.GetDetailDir(), filepath.Dir(archivedTo))
	}

	detailRefs, ok := parsed["detail_refs"].([]any)
	if !ok || len(detailRefs) == 0 {
		t.Fatalf("expected detail_refs to include index path, got: %v", parsed["detail_refs"])
	}
	indexPath, ok := detailRefs[len(detailRefs)-1].(string)
	if !ok || indexPath == "" {
		t.Fatalf("expected index path in detail_refs, got: %v", detailRefs)
	}
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("detail index file not found at %s: %v", indexPath, err)
	}
}

func TestCompactHistoryTool_Execute_DefaultArchiveWhenWorkingMemoryPresent(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agent.NewWorkingMemory(sessionDir)
	if _, err := wm.Load(); err != nil {
		t.Fatalf("failed to initialize working memory: %v", err)
	}

	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "task 1"}}},
			{Role: "assistant", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "response 1"}}},
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "task 2"}}},
			{Role: "assistant", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "response 2"}}},
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "task 3"}}},
			{Role: "assistant", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "response 3"}}},
		},
		WorkingMemory: wm,
	}

	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)
	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")

	resultBlocks, err := tool.Execute(context.Background(), map[string]any{
		"target":      "conversation",
		"keep_recent": 2,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	text, ok := resultBlocks[0].(agent.TextContent)
	if !ok {
		t.Fatal("expected text content result")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(text.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	archivedTo, ok := parsed["archived_to"].(string)
	if !ok || archivedTo == "" {
		t.Fatalf("expected archived_to path with default strategy, got: %v", parsed["archived_to"])
	}
	if _, err := os.Stat(archivedTo); err != nil {
		t.Fatalf("archive file not found at %s: %v", archivedTo, err)
	}

	if got, _ := parsed["strategy_selected"].(string); got != "archive" {
		t.Fatalf("expected strategy_selected=archive, got %v", parsed["strategy_selected"])
	}
	if got, _ := parsed["strategy_reason"].(string); got == "" {
		t.Fatalf("expected strategy_reason to be non-empty")
	}
}

func TestCompactHistoryTool_DefaultStrategyWithoutWorkingMemory(t *testing.T) {
	tool := NewCompactHistoryTool(&agent.AgentContext{}, nil, llm.Model{}, "", "")
	if got := tool.defaultStrategy("conversation"); got != "summarize" {
		t.Fatalf("expected summarize without working memory, got %q", got)
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		(s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
