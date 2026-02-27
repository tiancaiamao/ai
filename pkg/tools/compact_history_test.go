package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestCompactHistoryTool_Execute_KeepRecentAlias(t *testing.T) {
	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "m1"}}},
			{Role: "assistant", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "a1"}}},
			{Role: "user", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "m2"}}},
			{Role: "assistant", Content: []agent.ContentBlock{agent.TextContent{Type: "text", Text: "a2"}}},
		},
	}

	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)
	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")

	result, err := tool.Execute(context.Background(), map[string]any{
		"target":     "conversation",
		"keepRecent": 1,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	textResult, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("expected text result")
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(textResult.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	if got, ok := parsed["kept_recent"].(float64); !ok || int(got) != 1 {
		t.Fatalf("expected kept_recent=1 from keepRecent alias, got %v", parsed["kept_recent"])
	}
}

func TestCompactHistoryTool_Execute_PrefersExecutionContext(t *testing.T) {
	staleCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			agent.NewUserMessage("stale-u1"),
			agent.NewAssistantMessage(),
			agent.NewUserMessage("stale-u2"),
		},
	}
	staleCtx.Messages[1].Content = []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: "stale-a1"},
	}

	runCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			agent.NewUserMessage("run-u1"),
			agent.NewAssistantMessage(),
			agent.NewUserMessage("run-u2"),
			agent.NewAssistantMessage(),
		},
	}
	runCtx.Messages[1].Content = []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: "run-a1"},
	}
	runCtx.Messages[3].Content = []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: "run-a2"},
	}

	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)
	tool := NewCompactHistoryTool(staleCtx, compactor, llm.Model{}, "", "")

	execCtx := agent.WithToolExecutionAgentContext(context.Background(), runCtx)
	_, err := tool.Execute(execCtx, map[string]any{
		"target":      "conversation",
		"keep_recent": 1,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(runCtx.Messages) != 2 {
		t.Fatalf("expected run context to be compacted, got %d messages", len(runCtx.Messages))
	}
	if got := runCtx.Messages[0].ExtractText(); !strings.Contains(got, "[Previous conversation summary]") {
		t.Fatalf("expected run context summary after compaction, got: %q", got)
	}
	if len(staleCtx.Messages) != 3 {
		t.Fatalf("expected stale context to remain unchanged, got %d messages", len(staleCtx.Messages))
	}
}

func TestCompactHistoryTool_Execute_ToolFallbackSummaryIsBounded(t *testing.T) {
	messages := make([]agent.AgentMessage, 0, 80)
	for i := 0; i < 80; i++ {
		messages = append(messages, agent.AgentMessage{
			Role: "toolResult",
			Content: []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: "long output " + strings.Repeat("x", 300)},
			},
			ToolCallID: fmt.Sprintf("call-%02d", i),
			ToolName:   "bash",
		})
	}
	agentCtx := &agent.AgentContext{Messages: messages}
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)
	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")

	result, err := tool.Execute(context.Background(), map[string]any{
		"target":      "tools",
		"keep_recent": 0,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	textResult, ok := result[0].(agent.TextContent)
	if !ok {
		t.Fatal("expected text result")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(textResult.Text), &parsed); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}
	summary, _ := parsed["summary"].(string)
	if len([]rune(summary)) > 2500 {
		t.Fatalf("expected bounded fallback summary, got len=%d", len([]rune(summary)))
	}
}

func TestCompactHistoryTool_Execute_ToolsCompactionRemovesOldAssistantToolCalls(t *testing.T) {
	oldAssistant := agent.NewAssistantMessage()
	oldAssistant.Content = []agent.ContentBlock{
		agent.ToolCallContent{ID: "call-old", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "echo old"}},
	}
	oldResult := agent.NewToolResultMessage("call-old", "bash", []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: "old output"},
	}, false)

	recentAssistant := agent.NewAssistantMessage()
	recentAssistant.Content = []agent.ContentBlock{
		agent.ToolCallContent{ID: "call-new", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "echo new"}},
	}
	recentResult := agent.NewToolResultMessage("call-new", "bash", []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: "new output"},
	}, false)

	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{oldAssistant, oldResult, recentAssistant, recentResult},
	}
	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)
	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")

	_, err := tool.Execute(context.Background(), map[string]any{
		"target":      "tools",
		"keep_recent": 2,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if agentCtx.Messages[0].IsAgentVisible() {
		t.Fatal("expected old assistant tool-call message to be hidden from agent after compaction")
	}
	recentCalls := agentCtx.Messages[2].ExtractToolCalls()
	if len(recentCalls) != 1 || recentCalls[0].ID != "call-new" {
		t.Fatalf("expected recent tool call to remain, got %+v", recentCalls)
	}
}

func TestCompactHistoryTool_Execute_ConversationCompactionRespectsKeepRecent(t *testing.T) {
	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			agent.NewUserMessage("u1"),
			agent.NewAssistantMessage(),
			agent.NewUserMessage("u2"),
			agent.NewAssistantMessage(),
			agent.NewUserMessage("u3"),
			agent.NewAssistantMessage(),
		},
	}
	// Fill assistant message text blocks so summarizer has meaningful content.
	for i := range agentCtx.Messages {
		if agentCtx.Messages[i].Role == "assistant" {
			agentCtx.Messages[i].Content = []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: "assistant response"},
			}
		}
	}

	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)
	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")

	_, err := tool.Execute(context.Background(), map[string]any{
		"target":      "conversation",
		"keep_recent": 1,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(agentCtx.Messages) != 2 {
		t.Fatalf("expected summary + 1 recent message, got %d", len(agentCtx.Messages))
	}
	if got := agentCtx.Messages[0].ExtractText(); !strings.Contains(got, "[Previous conversation summary]") {
		t.Fatalf("expected first message to be conversation summary, got: %q", got)
	}
}

func TestCompactHistoryTool_Execute_TargetAllKeepsConversationCompaction(t *testing.T) {
	assistantWithToolCall := agent.NewAssistantMessage()
	assistantWithToolCall.Content = []agent.ContentBlock{
		agent.ToolCallContent{ID: "call-old", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "echo old"}},
	}
	toolResult := agent.NewToolResultMessage("call-old", "bash", []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: strings.Repeat("tool output ", 80)},
	}, false)
	recentUser := agent.NewUserMessage("recent-user")

	agentCtx := &agent.AgentContext{
		Messages: []agent.AgentMessage{
			agent.NewUserMessage("old-user-1"),
			agent.NewAssistantMessage().WithKind("assistant"),
			assistantWithToolCall,
			toolResult,
			recentUser,
		},
	}
	// Fill assistant text for deterministic fallback conversation summary.
	agentCtx.Messages[1].Content = []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: "old-assistant-1"},
	}

	compactor := compact.NewCompactor(compact.DefaultConfig(), llm.Model{}, "", "", 128000)
	tool := NewCompactHistoryTool(agentCtx, compactor, llm.Model{}, "", "")

	_, err := tool.Execute(context.Background(), map[string]any{
		"target":      "all",
		"keep_recent": 1,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(agentCtx.Messages) >= 5 {
		t.Fatalf("expected target=all to reduce message count, got %d", len(agentCtx.Messages))
	}
	if got := agentCtx.Messages[0].ExtractText(); !strings.Contains(got, "[Previous conversation summary]") {
		t.Fatalf("expected first message to remain compacted conversation summary, got: %q", got)
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
