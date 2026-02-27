package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestMaybeSummarizeToolResultsAboveCutoff(t *testing.T) {
	orig := summarizeToolResultFn
	defer func() { summarizeToolResultFn = orig }()

	summarizeToolResultFn = func(_ context.Context, _ llm.Model, _ string, result AgentMessage) (string, error) {
		return "summary for " + result.ToolName, nil
	}

	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = []AgentMessage{
		NewUserMessage("start"),
		NewToolResultMessage("call-1", "read", []ContentBlock{TextContent{Type: "text", Text: "first"}}, false),
		NewToolResultMessage("call-2", "grep", []ContentBlock{TextContent{Type: "text", Text: "second"}}, false),
	}

	cfg := &LoopConfig{ToolCallCutoff: 1}
	maybeSummarizeToolResults(context.Background(), agentCtx, cfg)

	if got := countVisibleToolResults(agentCtx.Messages); got != 1 {
		t.Fatalf("expected 1 visible tool result, got %d", got)
	}

	archived := agentCtx.Messages[1]
	if archived.IsAgentVisible() {
		t.Fatal("expected oldest tool result to be archived from agent-visible context")
	}
	if archived.Metadata == nil || archived.Metadata.Kind != "tool_result_archived" {
		t.Fatalf("expected archived kind, got %+v", archived.Metadata)
	}

	last := agentCtx.Messages[len(agentCtx.Messages)-1]
	if last.Metadata == nil || last.Metadata.Kind != "tool_summary" {
		t.Fatalf("expected tool_summary message, got %+v", last.Metadata)
	}
	if !last.IsAgentVisible() || last.IsUserVisible() {
		t.Fatal("expected tool_summary to be agent-visible and user-hidden")
	}
	if !strings.Contains(last.ExtractText(), "summary for read") {
		t.Fatalf("expected generated summary text, got %q", last.ExtractText())
	}
}

func TestMaybeSummarizeToolResultsCutoffDisabled(t *testing.T) {
	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = []AgentMessage{
		NewToolResultMessage("call-1", "read", []ContentBlock{TextContent{Type: "text", Text: "one"}}, false),
		NewToolResultMessage("call-2", "grep", []ContentBlock{TextContent{Type: "text", Text: "two"}}, false),
	}

	before := len(agentCtx.Messages)
	maybeSummarizeToolResults(context.Background(), agentCtx, &LoopConfig{ToolCallCutoff: 0})

	if len(agentCtx.Messages) != before {
		t.Fatalf("expected no mutation when cutoff=0, before=%d after=%d", before, len(agentCtx.Messages))
	}
	if got := countVisibleToolResults(agentCtx.Messages); got != 2 {
		t.Fatalf("expected all tool results visible, got %d", got)
	}
}

func TestMaybeSummarizeToolResultsFallbackOnError(t *testing.T) {
	orig := summarizeToolResultFn
	defer func() { summarizeToolResultFn = orig }()

	summarizeToolResultFn = func(_ context.Context, _ llm.Model, _ string, _ AgentMessage) (string, error) {
		return "", errors.New("summary failed")
	}

	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = []AgentMessage{
		NewToolResultMessage("call-1", "bash", []ContentBlock{TextContent{Type: "text", Text: "line1\nline2"}}, true),
		NewToolResultMessage("call-2", "grep", []ContentBlock{TextContent{Type: "text", Text: "line3"}}, false),
	}

	maybeSummarizeToolResults(context.Background(), agentCtx, &LoopConfig{ToolCallCutoff: 1})

	last := agentCtx.Messages[len(agentCtx.Messages)-1]
	if !strings.Contains(last.ExtractText(), "Tool \"bash\" finished with status error") {
		t.Fatalf("expected fallback summary content, got %q", last.ExtractText())
	}
}

func TestMaybeSummarizeToolResultsHeuristicStrategy(t *testing.T) {
	orig := summarizeToolResultFn
	defer func() { summarizeToolResultFn = orig }()

	summarizeToolResultFn = func(_ context.Context, _ llm.Model, _ string, _ AgentMessage) (string, error) {
		t.Fatal("llm summarizer should not be called in heuristic strategy")
		return "", nil
	}

	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = []AgentMessage{
		NewToolResultMessage("call-1", "bash", []ContentBlock{TextContent{Type: "text", Text: "line1"}}, false),
		NewToolResultMessage("call-2", "grep", []ContentBlock{TextContent{Type: "text", Text: "line2"}}, false),
	}

	maybeSummarizeToolResults(context.Background(), agentCtx, &LoopConfig{
		ToolCallCutoff:      1,
		ToolSummaryStrategy: "heuristic",
	})

	last := agentCtx.Messages[len(agentCtx.Messages)-1]
	if !strings.Contains(last.ExtractText(), "Tool \"bash\" finished with status ok") {
		t.Fatalf("expected heuristic summary, got %q", last.ExtractText())
	}
}

func TestNormalizeToolSummaryStrategy(t *testing.T) {
	cases := map[string]string{
		"":          "llm",
		"llm":       "llm",
		"LLM":       "llm",
		"heuristic": "heuristic",
		"off":       "off",
		"unknown":   "llm",
	}

	for input, expected := range cases {
		if got := normalizeToolSummaryStrategy(input); got != expected {
			t.Fatalf("normalizeToolSummaryStrategy(%q)=%q, want %q", input, got, expected)
		}
	}
}

type summaryDecisionCompactor struct {
	shouldCompact bool
}

func (c *summaryDecisionCompactor) ShouldCompact(_ []AgentMessage) bool {
	return c.shouldCompact
}

func (c *summaryDecisionCompactor) Compact(messages []AgentMessage, _ string) (*CompactionResult, error) {
	return &CompactionResult{Messages: messages}, nil
}

func TestNormalizeToolSummaryAutomation(t *testing.T) {
	cases := map[string]string{
		"":         "always",
		"always":   "always",
		"fallback": "fallback",
		"off":      "off",
		"unknown":  "always",
	}

	for input, expected := range cases {
		if got := normalizeToolSummaryAutomation(input); got != expected {
			t.Fatalf("normalizeToolSummaryAutomation(%q)=%q, want %q", input, got, expected)
		}
	}
}

func TestShouldAutoSummarizeToolResults_FallbackMode(t *testing.T) {
	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = []AgentMessage{
		NewToolResultMessage("call-1", "read", []ContentBlock{TextContent{Type: "text", Text: "one"}}, false),
	}

	cfg := &LoopConfig{
		ToolCallCutoff:        1,
		ToolSummaryAutomation: "fallback",
		Compactor:             &summaryDecisionCompactor{shouldCompact: true},
	}
	if !shouldAutoSummarizeToolResults(agentCtx, cfg) {
		t.Fatal("expected fallback mode to enable auto summary when compactor reports pressure")
	}

	cfg.Compactor = &summaryDecisionCompactor{shouldCompact: false}
	if shouldAutoSummarizeToolResults(agentCtx, cfg) {
		t.Fatal("expected fallback mode to skip auto summary when compactor does not report pressure")
	}
}
