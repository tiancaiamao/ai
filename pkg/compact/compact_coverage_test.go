package compact

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// --- pure helpers ---

func TestEstimateStringTokens(t *testing.T) {
	if estimateStringTokens("") != 0 {
		t.Error("empty string should be 0 tokens")
	}
	if estimateStringTokens("abcd") != 1 {
		t.Errorf("expected 1 token for 4 chars, got %d", estimateStringTokens("abcd"))
	}
	if estimateStringTokens("abcdefgh") != 2 {
		t.Errorf("expected 2 tokens for 8 chars, got %d", estimateStringTokens("abcdefgh"))
	}
}

func TestExtractText(t *testing.T) {
	// With content blocks
	msg := agentctx.AgentMessage{
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "hello "},
			agentctx.TextContent{Type: "text", Text: "world"},
		},
	}
	if got := msg.ExtractText(); got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}

	// Without content blocks — fallback to ExtractText
	msg = agentctx.AgentMessage{
		Role:    "user",
		Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "fallback"}},
	}
	if got := msg.ExtractText(); got != "fallback" {
		t.Errorf("expected 'fallback', got %q", got)
	}
}

// --- compactor getters / setters ---

func TestNewCompactor_NilConfig(t *testing.T) {
	c := NewCompactor(nil, llm.Model{}, "key", "sys", 0, "")
	if c.GetConfig() == nil {
		t.Error("expected non-nil default config")
	}
	if c.GetConfig().MaxTokens != DefaultConfig().MaxTokens {
		t.Errorf("expected default MaxTokens=%d, got %d", DefaultConfig().MaxTokens, c.GetConfig().MaxTokens)
	}
}

func TestCompactor_ContextWindowAndReserve(t *testing.T) {
	c := NewCompactor(&Config{ReserveTokens: 0}, llm.Model{}, "", "", 5000, "")
	if c.ContextWindow() != 5000 {
		t.Errorf("expected context window 5000, got %d", c.ContextWindow())
	}

	// Default ReserveTokens
	if c.ReserveTokens() != DefaultConfig().ReserveTokens {
		t.Errorf("expected default ReserveTokens=%d, got %d", DefaultConfig().ReserveTokens, c.ReserveTokens())
	}

	c.SetContextWindow(200000)
	if c.ContextWindow() != 200000 {
		t.Errorf("expected context window 200000 after SetContextWindow, got %d", c.ContextWindow())
	}

	// Configured ReserveTokens
	c2 := NewCompactor(&Config{ReserveTokens: 4096}, llm.Model{}, "", "", 0, "")
	if c2.ReserveTokens() != 4096 {
		t.Errorf("expected 4096, got %d", c2.ReserveTokens())
	}
}

func TestCompactor_KeepRecentAccessors(t *testing.T) {
	// Default KeepRecent fallback
	c := NewCompactor(&Config{KeepRecent: 0, KeepRecentTokens: 0}, llm.Model{}, "", "", 0, "")
	if c.KeepRecentMessages() != DefaultConfig().KeepRecent {
		t.Errorf("expected default KeepRecent=%d, got %d", DefaultConfig().KeepRecent, c.KeepRecentMessages())
	}
	if c.KeepRecentTokens() != 0 {
		t.Errorf("expected 0 when KeepRecentTokens=0, got %d", c.KeepRecentTokens())
	}

	// Explicit KeepRecent
	c2 := NewCompactor(&Config{KeepRecent: 7}, llm.Model{}, "", "", 0, "")
	if c2.KeepRecentMessages() != 7 {
		t.Errorf("expected 7, got %d", c2.KeepRecentMessages())
	}

	// KeepRecentTokens capped by EffectiveTokenLimit/2
	c3 := NewCompactor(&Config{KeepRecentTokens: 100000}, llm.Model{}, "", "", 100000, "")
	got := c3.KeepRecentTokens()
	if got >= 100000 {
		t.Errorf("expected KeepRecentTokens to be capped, got %d", got)
	}
}

func TestCompactor_EffectiveTokenLimit(t *testing.T) {
	// nil receiver
	var nilC *Compactor
	if limit, src := nilC.EffectiveTokenLimit(); limit != 0 || src != "none" {
		t.Errorf("expected (0, none), got (%d, %s)", limit, src)
	}

	// context window wins
	c := NewCompactor(&Config{MaxTokens: 9999}, llm.Model{}, "", "", 100000, "")
	limit, src := c.EffectiveTokenLimit()
	if src != "context_window" {
		t.Errorf("expected context_window source, got %s", src)
	}
	if limit <= 0 {
		t.Errorf("expected positive limit, got %d", limit)
	}

	// falls back to MaxTokens when window=0
	c2 := NewCompactor(&Config{MaxTokens: 8000}, llm.Model{}, "", "", 0, "")
	limit2, src2 := c2.EffectiveTokenLimit()
	if src2 != "max_tokens" || limit2 != 8000 {
		t.Errorf("expected (8000, max_tokens), got (%d, %s)", limit2, src2)
	}

	// none when both unset
	c3 := NewCompactor(&Config{}, llm.Model{}, "", "", 0, "")
	if limit3, src3 := c3.EffectiveTokenLimit(); limit3 != 0 || src3 != "none" {
		t.Errorf("expected (0, none), got (%d, %s)", limit3, src3)
	}

	// context window with reserve exceeding window → fall back to max_tokens
	c4 := NewCompactor(&Config{MaxTokens: 1234, ReserveTokens: 200000}, llm.Model{}, "", "", 1000, "")
	limit4, src4 := c4.EffectiveTokenLimit()
	if src4 != "max_tokens" || limit4 != 1234 {
		t.Errorf("expected fall-back to max_tokens, got (%d, %s)", limit4, src4)
	}
}

func TestCalculateDynamicThreshold_EdgeCases(t *testing.T) {
	// No context window → use config MaxTokens
	c := NewCompactor(&Config{MaxTokens: 7777}, llm.Model{}, "", "", 0, "")
	if got := c.CalculateDynamicThreshold(); got != 7777 {
		t.Errorf("expected 7777, got %d", got)
	}

	// Tiny context window (overhead > window) → fall back to MaxTokens
	c2 := NewCompactor(&Config{MaxTokens: 3333, ReserveTokens: 50000}, llm.Model{}, "", "huge system prompt", 1000, "")
	if got := c2.CalculateDynamicThreshold(); got != 3333 {
		t.Errorf("expected fall-back 3333, got %d", got)
	}

	// Window with all overhead but small available → minimum threshold 4000 applies
	c3 := NewCompactor(&Config{ReserveTokens: 16384, MaxTokens: 5000}, llm.Model{}, "", "", 20000, "")
	got := c3.CalculateDynamicThreshold()
	if got < 4000 {
		t.Errorf("expected minimum threshold 4000, got %d", got)
	}
}

// --- token estimation helpers ---

func TestUsageTotalTokens(t *testing.T) {
	if usageTotalTokens(nil) != 0 {
		t.Error("expected 0 for nil usage")
	}
	if got := usageTotalTokens(&agentctx.Usage{TotalTokens: 100}); got != 100 {
		t.Errorf("expected 100, got %d", got)
	}
	// No TotalTokens → sum components
	u := &agentctx.Usage{InputTokens: 10, OutputTokens: 20, CacheRead: 5, CacheWrite: 5}
	if got := usageTotalTokens(u); got != 40 {
		t.Errorf("expected 40, got %d", got)
	}
}

func TestLastAssistantUsageTokens(t *testing.T) {
	// Empty slice
	if tokens, idx := lastAssistantUsageTokens(nil); tokens != 0 || idx != -1 {
		t.Errorf("expected (0,-1), got (%d,%d)", tokens, idx)
	}

	// No assistant with usage
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("hi"),
		agentctx.NewAssistantMessage(),
	}
	if tokens, idx := lastAssistantUsageTokens(msgs); tokens != 0 || idx != -1 {
		t.Errorf("expected (0,-1), got (%d,%d)", tokens, idx)
	}

	// Assistant with valid usage
	m := agentctx.NewAssistantMessage()
	m.Usage = &agentctx.Usage{TotalTokens: 1234}
	m2 := agentctx.NewAssistantMessage()
	m2.Usage = &agentctx.Usage{TotalTokens: 0, InputTokens: 100, OutputTokens: 50}
	msgs = []agentctx.AgentMessage{m, m2}
	tokens, idx := lastAssistantUsageTokens(msgs)
	if tokens != 150 || idx != 1 {
		t.Errorf("expected (150, 1), got (%d, %d)", tokens, idx)
	}

	// Assistant with aborted stop reason — skipped
	m3 := agentctx.NewAssistantMessage()
	m3.Usage = &agentctx.Usage{TotalTokens: 999}
	m3.StopReason = "aborted"
	// Assistant with error stop reason — skipped
	m4 := agentctx.NewAssistantMessage()
	m4.Usage = &agentctx.Usage{TotalTokens: 998}
	m4.StopReason = "  ERROR  "
	// Agent-invisible assistant — skipped
	m5 := agentctx.NewAssistantMessage()
	m5.Usage = &agentctx.Usage{TotalTokens: 997}
	m5 = m5.WithVisibility(false, true)

	msgs = []agentctx.AgentMessage{m3, m4, m5}
	if tokens, idx := lastAssistantUsageTokens(msgs); tokens != 0 || idx != -1 {
		t.Errorf("expected all skipped → (0,-1), got (%d,%d)", tokens, idx)
	}
}

func TestEstimateMessageTokens_Variants(t *testing.T) {
	// Agent-invisible message returns 0
	invisible := agentctx.NewUserMessage("hello").WithVisibility(false, true)
	if EstimateMessageTokens(invisible) != 0 {
		t.Error("expected 0 for agent-invisible message")
	}

	// Text content
	tokens := EstimateMessageTokens(agentctx.NewUserMessage("hello world"))
	if tokens <= 0 {
		t.Errorf("expected positive tokens, got %d", tokens)
	}

	// Thinking content
	m := agentctx.NewAssistantMessage()
	m.Content = []agentctx.ContentBlock{
		agentctx.ThinkingContent{Type: "thinking", Thinking: "thought " + strings.Repeat("x", 40)},
	}
	if got := EstimateMessageTokens(m); got <= 0 {
		t.Errorf("expected positive tokens for thinking, got %d", got)
	}

	// Tool call content
	m2 := agentctx.NewAssistantMessage()
	m2.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			Type:      "toolCall",
			ID:        "call-x",
			Name:      "bash",
			Arguments: map[string]any{"command": "ls -la"},
		},
	}
	if got := EstimateMessageTokens(m2); got <= 0 {
		t.Errorf("expected positive tokens for tool call, got %d", got)
	}

	// Image content
	m3 := agentctx.NewAssistantMessage()
	m3.Content = []agentctx.ContentBlock{
		agentctx.ImageContent{Type: "image", Data: "abc"},
	}
	if got := EstimateMessageTokens(m3); got != 1200 {
		t.Errorf("expected 1200 for image, got %d", got)
	}

	// Truly empty → 0
	m5 := agentctx.AgentMessage{Role: "user"}
	if got := EstimateMessageTokens(m5); got != 0 {
		t.Errorf("expected 0 for truly empty message, got %d", got)
	}
}

// --- splitMessagesByTokenBudget edge cases ---

func TestSplitMessagesByTokenBudget_EdgeCases(t *testing.T) {
	// Empty input
	old, recent := splitMessagesByTokenBudget(nil, 100)
	if len(old) != 0 || len(recent) != 0 {
		t.Errorf("expected empty for nil input, got old=%d recent=%d", len(old), len(recent))
	}

	// Zero/negative budget → split off the last message
	messages := []agentctx.AgentMessage{
		agentctx.NewUserMessage("a"),
		agentctx.NewUserMessage("b"),
		agentctx.NewUserMessage("c"),
	}
	old, recent = splitMessagesByTokenBudget(messages, 0)
	if len(recent) != 1 || len(old) != 2 {
		t.Errorf("budget=0: expected old=2 recent=1, got old=%d recent=%d", len(old), len(recent))
	}
	old, recent = splitMessagesByTokenBudget(messages, -5)
	if len(recent) != 1 || len(old) != 2 {
		t.Errorf("budget<0: expected old=2 recent=1, got old=%d recent=%d", len(old), len(recent))
	}

	// All messages fit → return nil old, all in recent
	big := []agentctx.AgentMessage{agentctx.NewUserMessage("a")}
	old, recent = splitMessagesByTokenBudget(big, 1000000)
	if len(old) != 0 || len(recent) != 1 {
		t.Errorf("expected all in recent, got old=%d recent=%d", len(old), len(recent))
	}

	// Compaction summary must stay in recent
	summary := agentctx.NewCompactionSummaryMessage("previous summary")
	messages = append(messages, summary)
	old, recent = splitMessagesByTokenBudget(messages, 1)
	// recent must include the compaction summary
	foundSummary := false
	for _, m := range recent {
		if m.Metadata != nil && m.Metadata.Kind == "compactionSummary" {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Errorf("expected compaction summary in recent, got old=%d recent=%d", len(old), len(recent))
	}
}

// --- Compactor.Compact edge cases ---

func TestCompact_EmptyMessages(t *testing.T) {
	c := NewCompactor(DefaultConfig(), llm.Model{}, "", "", 0, "")
	ctx := agentctx.NewAgentContext("sys")
	r, err := c.Compact(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil result")
	}
	if r.TokensBefore != 0 || r.TokensAfter != 0 {
		t.Errorf("expected zero tokens, got before=%d after=%d", r.TokensBefore, r.TokensAfter)
	}
}

func TestCompact_NoKeepRecentTokens_TooFewMessages(t *testing.T) {
	cfg := &Config{
		KeepRecent:       3,
		KeepRecentTokens: 0, // exercise the keepCount branch
		AutoCompact:      true,
	}
	c := NewCompactor(cfg, llm.Model{}, "", "", 0, "")
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("a"),
		agentctx.NewUserMessage("b"),
	}
	// Fewer messages than KeepRecent — no-op
	r, err := c.Compact(context.Background(), ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TokensBefore != r.TokensAfter {
		t.Errorf("expected no change, got before=%d after=%d", r.TokensBefore, r.TokensAfter)
	}
}

// --- GenerateSummary error paths ---

func TestGenerateSummary_NoMessages(t *testing.T) {
	c := NewCompactor(DefaultConfig(), llm.Model{}, "k", "sys", 0, "")
	if _, err := c.GenerateSummary(context.Background(), nil); err == nil {
		t.Error("expected error for no messages")
	}
}

func TestGenerateSummaryWithPrevious_NoVisibleMessages(t *testing.T) {
	c := NewCompactor(DefaultConfig(), llm.Model{}, "k", "sys", 0, "")
	// Agent-invisible messages only → no agent-visible
	invisible := agentctx.NewUserMessage("hidden").WithVisibility(false, true)
	// With empty previous summary → error
	if _, err := c.GenerateSummaryWithPrevious(context.Background(), []agentctx.AgentMessage{invisible}, "", "", nil, ""); err == nil {
		t.Error("expected error when no agent-visible messages and no previous summary")
	}
	// With non-empty previous summary → returned as-is
	got, err := c.GenerateSummaryWithPrevious(context.Background(), []agentctx.AgentMessage{invisible}, "", "", nil, "prior")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "prior" {
		t.Errorf("expected prior summary returned, got %q", got)
	}
}

// sseTextResponse returns a simple SSE stream with a single text delta.
func sseTextResponse(text string) string {
	chunks := []string{
		fmt.Sprintf(`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"%s"},"finish_reason":null}]}`, text),
		`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}
	return strings.Join(chunks, "\n\n") + "\n\n"
}

func TestGenerateSummaryWithPrevious_LLMSuccessAndFailures(t *testing.T) {
	// Success path: LLM returns text → summary returned.
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseTextResponse("the summary"))
	}))
	defer server.Close()

	model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai"}
	c := NewCompactor(DefaultConfig(), model, "k", "sys", 0, "")

	got, err := c.GenerateSummaryWithPrevious(context.Background(), []agentctx.AgentMessage{agentctx.NewUserMessage("hi")}, "", "", nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "the summary" {
		t.Errorf("expected 'the summary', got %q", got)
	}

	// Empty summary path → error
	emptyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// stream that produces only an empty content (no text delta)
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: [DONE]`+"\n\n")
	}))
	defer emptyServer.Close()
	c2 := NewCompactor(DefaultConfig(), llm.Model{ID: "m", ContextWindow: 200000, BaseURL: emptyServer.URL, API: "openai"}, "k", "sys", 0, "")
	_, err = c2.GenerateSummaryWithPrevious(context.Background(), []agentctx.AgentMessage{agentctx.NewUserMessage("hi")}, "", "", nil, "")
	if err == nil {
		t.Error("expected error for empty summary")
	}

	// Non-retryable 401 → no retries
	var authAttempts int32
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&authAttempts, 1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"Invalid API key"}}`)
	}))
	defer authServer.Close()
	c3 := NewCompactor(DefaultConfig(), llm.Model{ID: "m", ContextWindow: 200000, BaseURL: authServer.URL, API: "openai"}, "k", "sys", 0, "")
	_, err = c3.GenerateSummaryWithPrevious(context.Background(), []agentctx.AgentMessage{agentctx.NewUserMessage("hi")}, "", "", nil, "")
	if err == nil {
		t.Error("expected error from 401")
	}
	if atomic.LoadInt32(&authAttempts) != 1 {
		t.Errorf("expected 1 attempt (non-retryable), got %d", atomic.LoadInt32(&authAttempts))
	}
}
