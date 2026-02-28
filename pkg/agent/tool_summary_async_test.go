package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestAsyncToolSummarizerSchedulesAndAppliesBatch(t *testing.T) {
	origSingle := summarizeToolResultFn
	origBatch := summarizeToolResultsBatchFn
	defer func() {
		summarizeToolResultFn = origSingle
		summarizeToolResultsBatchFn = origBatch
	}()

	callCount := 0
	lastBatchSize := 0
	summarizeToolResultsBatchFn = func(_ context.Context, _ llm.Model, _ string, results []agentctx.AgentMessage) (string, error) {
		callCount++
		lastBatchSize = len(results)
		return "batched summary", nil
	}
	summarizeToolResultFn = func(_ context.Context, _ llm.Model, _ string, _ agentctx.AgentMessage) (string, error) {
		t.Fatal("single-result summarizer should not be called in batch async path")
		return "", nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.Messages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("start"),
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "first"}}, false),
		agentctx.NewToolResultMessage("call-2", "grep", []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "second"}}, false),
		agentctx.NewToolResultMessage("call-3", "bash", []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "third"}}, false),
	}

	cfg := &LoopConfig{
		ToolCallCutoff:        1,
		ToolSummaryStrategy:   "llm",
		ToolSummaryAutomation: "always",
		Model:                 llm.Model{ID: "m", Provider: "p", BaseURL: "https://example.invalid", API: "openai-completions"},
		APIKey:                "k",
	}

	s := newAsyncToolSummarizer(context.Background(), cfg)
	if s == nil {
		t.Fatal("expected async summarizer")
	}
	defer s.Close()

	s.schedule(agentCtx)

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		s.applyReady(agentCtx)
		if countVisibleToolResults(agentCtx.Messages) <= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if callCount != 1 {
		t.Fatalf("expected one batch summary call, got %d", callCount)
	}
	if lastBatchSize != 2 {
		t.Fatalf("expected batch size 2, got %d", lastBatchSize)
	}
	if got := countVisibleToolResults(agentCtx.Messages); got != 1 {
		t.Fatalf("expected 1 visible tool result after async apply, got %d", got)
	}

	last := agentCtx.Messages[len(agentCtx.Messages)-1]
	if last.Metadata == nil || last.Metadata.Kind != "tool_summary" {
		t.Fatalf("expected tool_summary message, got %+v", last.Metadata)
	}
	if !strings.Contains(last.ExtractText(), "batched summary") {
		t.Fatalf("expected batch summary text, got %q", last.ExtractText())
	}
}

func TestAsyncToolSummarizerScheduleIsNonBlocking(t *testing.T) {
	origBatch := summarizeToolResultsBatchFn
	defer func() { summarizeToolResultsBatchFn = origBatch }()

	summarizeToolResultsBatchFn = func(ctx context.Context, _ llm.Model, _ string, _ []agentctx.AgentMessage) (string, error) {
		<-ctx.Done()
		return "", errors.New("canceled")
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.Messages = []agentctx.AgentMessage{
		agentctx.NewToolResultMessage("call-1", "bash", []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "long output"}}, false),
		agentctx.NewToolResultMessage("call-2", "grep", []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "next"}}, false),
	}

	cfg := &LoopConfig{
		ToolCallCutoff:        1,
		ToolSummaryStrategy:   "llm",
		ToolSummaryAutomation: "always",
		Model:                 llm.Model{ID: "m", Provider: "p", BaseURL: "https://example.invalid", API: "openai-completions"},
		APIKey:                "k",
	}

	s := newAsyncToolSummarizer(context.Background(), cfg)
	if s == nil {
		t.Fatal("expected async summarizer")
	}
	defer s.Close()

	start := time.Now()
	s.schedule(agentCtx)
	elapsed := time.Since(start)
	if elapsed > 50*time.Millisecond {
		t.Fatalf("expected non-blocking schedule, took %v", elapsed)
	}
}

func TestNewAsyncToolSummarizerDisabledByAutomationOff(t *testing.T) {
	cfg := &LoopConfig{
		ToolCallCutoff:        1,
		ToolSummaryStrategy:   "llm",
		ToolSummaryAutomation: "off",
	}
	if s := newAsyncToolSummarizer(context.Background(), cfg); s != nil {
		t.Fatal("expected async summarizer to be disabled when automation mode is off")
	}
}
