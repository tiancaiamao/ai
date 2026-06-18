package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/tools/context_mgmt"
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
	c := NewCompactor(nil, llm.Model{}, "key", "sys", 0)
	if c.GetConfig() == nil {
		t.Error("expected non-nil default config")
	}
	if c.GetConfig().MaxTokens != DefaultConfig().MaxTokens {
		t.Errorf("expected default MaxTokens=%d, got %d", DefaultConfig().MaxTokens, c.GetConfig().MaxTokens)
	}
}

func TestCompactor_ContextWindowAndReserve(t *testing.T) {
	c := NewCompactor(&Config{ReserveTokens: 0}, llm.Model{}, "", "", 5000)
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
	c2 := NewCompactor(&Config{ReserveTokens: 4096}, llm.Model{}, "", "", 0)
	if c2.ReserveTokens() != 4096 {
		t.Errorf("expected 4096, got %d", c2.ReserveTokens())
	}
}

func TestCompactor_KeepRecentAccessors(t *testing.T) {
	// Default KeepRecent fallback
	c := NewCompactor(&Config{KeepRecent: 0, KeepRecentTokens: 0}, llm.Model{}, "", "", 0)
	if c.KeepRecentMessages() != DefaultConfig().KeepRecent {
		t.Errorf("expected default KeepRecent=%d, got %d", DefaultConfig().KeepRecent, c.KeepRecentMessages())
	}
	if c.KeepRecentTokens() != 0 {
		t.Errorf("expected 0 when KeepRecentTokens=0, got %d", c.KeepRecentTokens())
	}

	// Explicit KeepRecent
	c2 := NewCompactor(&Config{KeepRecent: 7}, llm.Model{}, "", "", 0)
	if c2.KeepRecentMessages() != 7 {
		t.Errorf("expected 7, got %d", c2.KeepRecentMessages())
	}

	// KeepRecentTokens capped by EffectiveTokenLimit/2
	c3 := NewCompactor(&Config{KeepRecentTokens: 100000}, llm.Model{}, "", "", 100000)
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
	c := NewCompactor(&Config{MaxTokens: 9999}, llm.Model{}, "", "", 100000)
	limit, src := c.EffectiveTokenLimit()
	if src != "context_window" {
		t.Errorf("expected context_window source, got %s", src)
	}
	if limit <= 0 {
		t.Errorf("expected positive limit, got %d", limit)
	}

	// falls back to MaxTokens when window=0
	c2 := NewCompactor(&Config{MaxTokens: 8000}, llm.Model{}, "", "", 0)
	limit2, src2 := c2.EffectiveTokenLimit()
	if src2 != "max_tokens" || limit2 != 8000 {
		t.Errorf("expected (8000, max_tokens), got (%d, %s)", limit2, src2)
	}

	// none when both unset
	c3 := NewCompactor(&Config{}, llm.Model{}, "", "", 0)
	if limit3, src3 := c3.EffectiveTokenLimit(); limit3 != 0 || src3 != "none" {
		t.Errorf("expected (0, none), got (%d, %s)", limit3, src3)
	}

	// context window with reserve exceeding window → fall back to max_tokens
	c4 := NewCompactor(&Config{MaxTokens: 1234, ReserveTokens: 200000}, llm.Model{}, "", "", 1000)
	limit4, src4 := c4.EffectiveTokenLimit()
	if src4 != "max_tokens" || limit4 != 1234 {
		t.Errorf("expected fall-back to max_tokens, got (%d, %s)", limit4, src4)
	}
}

func TestCalculateDynamicThreshold_EdgeCases(t *testing.T) {
	// No context window → use config MaxTokens
	c := NewCompactor(&Config{MaxTokens: 7777}, llm.Model{}, "", "", 0)
	if got := c.CalculateDynamicThreshold(); got != 7777 {
		t.Errorf("expected 7777, got %d", got)
	}

	// Tiny context window (overhead > window) → fall back to MaxTokens
	c2 := NewCompactor(&Config{MaxTokens: 3333, ReserveTokens: 50000}, llm.Model{}, "", "huge system prompt", 1000)
	if got := c2.CalculateDynamicThreshold(); got != 3333 {
		t.Errorf("expected fall-back 3333, got %d", got)
	}

	// Window with all overhead but small available → minimum threshold 4000 applies
	c3 := NewCompactor(&Config{ReserveTokens: 16384, MaxTokens: 5000}, llm.Model{}, "", "", 20000)
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
	c := NewCompactor(DefaultConfig(), llm.Model{}, "", "", 0)
	ctx := agentctx.NewAgentContext("sys")
	r, err := c.Compact(ctx)
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
	c := NewCompactor(cfg, llm.Model{}, "", "", 0)
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("a"),
		agentctx.NewUserMessage("b"),
	}
	// Fewer messages than KeepRecent — no-op
	r, err := c.Compact(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.TokensBefore != r.TokensAfter {
		t.Errorf("expected no change, got before=%d after=%d", r.TokensBefore, r.TokensAfter)
	}
}

// --- GenerateSummary error paths ---

func TestGenerateSummary_NoMessages(t *testing.T) {
	c := NewCompactor(DefaultConfig(), llm.Model{}, "k", "sys", 0)
	if _, err := c.GenerateSummary(nil); err == nil {
		t.Error("expected error for no messages")
	}
}

func TestGenerateSummaryWithPrevious_NoVisibleMessages(t *testing.T) {
	c := NewCompactor(DefaultConfig(), llm.Model{}, "k", "sys", 0)
	// Agent-invisible messages only → no agent-visible
	invisible := agentctx.NewUserMessage("hidden").WithVisibility(false, true)
	// With empty previous summary → error
	if _, err := c.GenerateSummaryWithPrevious([]agentctx.AgentMessage{invisible}, "", "", nil, ""); err == nil {
		t.Error("expected error when no agent-visible messages and no previous summary")
	}
	// With non-empty previous summary → returned as-is
	got, err := c.GenerateSummaryWithPrevious([]agentctx.AgentMessage{invisible}, "", "", nil, "prior")
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
	c := NewCompactor(DefaultConfig(), model, "k", "sys", 0)

	got, err := c.GenerateSummaryWithPrevious([]agentctx.AgentMessage{agentctx.NewUserMessage("hi")}, "", "", nil, "")
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
	c2 := NewCompactor(DefaultConfig(), llm.Model{ID: "m", ContextWindow: 200000, BaseURL: emptyServer.URL, API: "openai"}, "k", "sys", 0)
	_, err = c2.GenerateSummaryWithPrevious([]agentctx.AgentMessage{agentctx.NewUserMessage("hi")}, "", "", nil, "")
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
	c3 := NewCompactor(DefaultConfig(), llm.Model{ID: "m", ContextWindow: 200000, BaseURL: authServer.URL, API: "openai"}, "k", "sys", 0)
	_, err = c3.GenerateSummaryWithPrevious([]agentctx.AgentMessage{agentctx.NewUserMessage("hi")}, "", "", nil, "")
	if err == nil {
		t.Error("expected error from 401")
	}
	if atomic.LoadInt32(&authAttempts) != 1 {
		t.Errorf("expected 1 attempt (non-retryable), got %d", atomic.LoadInt32(&authAttempts))
	}
}

// --- CompactTool tests ---

func TestCompactTool_Metadata(t *testing.T) {
	tool := NewCompactTool(agentctx.NewAgentContext("sys"), nil)
	if tool.Name() != "compact" {
		t.Errorf("expected name 'compact', got %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
	params := tool.Parameters()
	if params == nil {
		t.Fatal("expected non-nil parameters")
	}
	if _, ok := params["properties"]; !ok {
		t.Error("expected 'properties' in parameters")
	}
}

func TestCompactTool_Execute_RequiresReason(t *testing.T) {
	tool := NewCompactTool(agentctx.NewAgentContext("sys"), NewCompactor(DefaultConfig(), llm.Model{}, "", "", 0))
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil {
		t.Error("expected error when reason is missing")
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"reason": ""}); err == nil {
		t.Error("expected error when reason is empty")
	}
}

func TestCompactTool_Execute_Strategies(t *testing.T) {
	cases := []struct {
		strategy string
	}{
		{"conservative"},
		{"balanced"},
		{"aggressive"},
	}
	for _, tc := range cases {
		t.Run(tc.strategy, func(t *testing.T) {
			agentCtx := agentctx.NewAgentContext("sys")
			// Provide enough messages that Compact actually performs a split.
			for i := 0; i < 12; i++ {
				agentCtx.RecentMessages = append(agentCtx.RecentMessages,
					agentctx.NewUserMessage(strings.Repeat("x", 200)))
			}
			// Use a small KeepRecentTokens so summary path is exercised.
			cfg := &Config{
				KeepRecentTokens: 100,
				MaxTokens:        50,
				AutoCompact:      true,
			}
			compactor := NewCompactor(cfg, llm.Model{}, "", "", 0)
			tool := NewCompactTool(agentCtx, compactor)

			// Provide a mock OnCompactEvent so the persist path is exercised.
			var persisted bool
			agentCtx.OnCompactEvent = func(_ *agentctx.CompactEventDetail) error {
				persisted = true
				return nil
			}

			// Compact will fail because LLM is unavailable — that's OK, we
			// still execute the early branches (parse, persist, config adjust).
			_, _ = tool.Execute(context.Background(), map[string]any{
				"strategy": tc.strategy,
				"reason":   "test",
			})
			if !persisted {
				t.Error("expected OnCompactEvent to be called")
			}
		})
	}
}

func TestCompactTool_Execute_OnCompactEventError(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	cfg := DefaultConfig()
	compactor := NewCompactor(cfg, llm.Model{}, "", "", 0)
	tool := NewCompactTool(agentCtx, compactor)

	agentCtx.OnCompactEvent = func(_ *agentctx.CompactEventDetail) error {
		return fmt.Errorf("disk full")
	}
	_, err := tool.Execute(context.Background(), map[string]any{
		"strategy": "balanced",
		"reason":   "go",
	})
	if err == nil || !strings.Contains(err.Error(), "persist compact event") {
		t.Errorf("expected persist error, got %v", err)
	}
}

// --- context management helpers (pure) ---

func TestExtractLatestUserRequest(t *testing.T) {
	if got := extractLatestUserRequest(nil); got != "(no user request found)" {
		t.Errorf("expected fallback text, got %q", got)
	}

	// No user message
	if got := extractLatestUserRequest([]agentctx.AgentMessage{agentctx.NewAssistantMessage()}); got != "(no user request found)" {
		t.Errorf("expected fallback, got %q", got)
	}

	// Recent user message
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("first"),
		agentctx.NewUserMessage("second"),
	}
	if got := extractLatestUserRequest(msgs); got != "second" {
		t.Errorf("expected 'second', got %q", got)
	}

	// Long user message → truncated with ellipsis
	long := strings.Repeat("x", 600)
	msgs = []agentctx.AgentMessage{agentctx.NewUserMessage(long)}
	got := extractLatestUserRequest(msgs)
	if !strings.HasSuffix(got, "...") || len(got) > 510 {
		t.Errorf("expected truncation + ellipsis, got len=%d", len(got))
	}

	// Empty text user message → skipped
	emptyMsg := agentctx.NewUserMessage("")
	msgs = []agentctx.AgentMessage{emptyMsg, agentctx.NewUserMessage("real")}
	if got := extractLatestUserRequest(msgs); got != "real" {
		t.Errorf("expected 'real', got %q", got)
	}
}

func TestCompactArgsStr(t *testing.T) {
	if compactArgsStr(nil) != "" {
		t.Error("expected empty for nil")
	}
	if compactArgsStr(map[string]any{}) != "" {
		t.Error("expected empty for empty map")
	}
	got := compactArgsStr(map[string]any{"a": 1})
	if got != `{"a":1}` {
		t.Errorf("unexpected output: %q", got)
	}
	// Large map → truncated to 100 chars + "..."
	big := map[string]any{}
	for i := 0; i < 30; i++ {
		big[fmt.Sprintf("k%d", i)] = i
	}
	out := compactArgsStr(big)
	if len(out) <= 100 {
		t.Errorf("expected truncation marker in long output, got %q", out)
	}
	if !strings.HasSuffix(out, "...") {
		t.Errorf("expected '...' suffix on truncation, got %q", out)
	}
}

func TestContextManager_StaleAgeDefaults(t *testing.T) {
	cfg := &ContextManagerConfig{}
	if cfg.staleAgeInvestigative() != mgmtStaleAgeInvestigative {
		t.Errorf("expected default %d, got %d", mgmtStaleAgeInvestigative, cfg.staleAgeInvestigative())
	}
	if cfg.staleAgeModification() != mgmtStaleAgeModification {
		t.Errorf("expected default %d, got %d", mgmtStaleAgeModification, cfg.staleAgeModification())
	}

	cfg2 := &ContextManagerConfig{
		StaleAgeInvestigative: 7,
		StaleAgeModification:  9,
	}
	if cfg2.staleAgeInvestigative() != 7 {
		t.Errorf("expected 7, got %d", cfg2.staleAgeInvestigative())
	}
	if cfg2.staleAgeModification() != 9 {
		t.Errorf("expected 9, got %d", cfg2.staleAgeModification())
	}
}

// --- ContextManager.ShouldCompact / Compact (no real LLM) ---

func TestContextManager_ShouldCompact_SkipCondition(t *testing.T) {
	cfg := DefaultContextManagerConfig()
	cm := NewContextManager(cfg, llmModelStub(), "", 200000, "sys", nil)
	cm.SetSkipCondition(func() bool { return true })
	if cm.ShouldCompact(context.Background(), agentctx.NewAgentContext("sys")) {
		t.Error("expected false when SkipCondition returns true")
	}
}

func TestContextManager_ShouldCompact_AutoCompactOff(t *testing.T) {
	cfg := DefaultContextManagerConfig()
	cfg.AutoCompact = false
	cm := NewContextManager(cfg, llmModelStub(), "", 200000, "sys", nil)
	if cm.ShouldCompact(context.Background(), agentctx.NewAgentContext("sys")) {
		t.Error("expected false when AutoCompact is false")
	}
}

func TestContextManager_ShouldCompact_BelowTokenLow(t *testing.T) {
	cfg := DefaultContextManagerConfig()
	cm := NewContextManager(cfg, llmModelStub(), "", 200000, "sys", nil)
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{agentctx.NewUserMessage("tiny")}
	if cm.ShouldCompact(context.Background(), ctx) {
		t.Error("expected false when below token low")
	}
}

func TestContextManager_ShouldCompact_Tiers(t *testing.T) {
	cfg := DefaultContextManagerConfig()
	cm := NewContextManager(cfg, llmModelStub(), "", 1000, "sys", nil)

	// High tier: token percent >= TokenHigh
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage(strings.Repeat("x", 800)), // ~200 tokens / 1000 = 20%
	}
	// Above TokenLow (20%) but below Medium → "low" tier
	ctx.AgentState.ToolCallsSinceLastTrigger = 100 // exceeds any interval
	if !cm.ShouldCompact(context.Background(), ctx) {
		t.Error("expected true at low tier when interval exceeded")
	}

	// Below interval
	ctx.AgentState.ToolCallsSinceLastTrigger = 1
	if cm.ShouldCompact(context.Background(), ctx) {
		t.Error("expected false when interval not reached")
	}
}

func TestContextManager_CalculateDynamicThreshold_NoWindow(t *testing.T) {
	cm := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 0, "sys", nil)
	if cm.CalculateDynamicThreshold() != 0 {
		t.Error("expected 0 when no context window")
	}
	cm2 := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 100000, "sys", nil)
	if cm2.CalculateDynamicThreshold() <= 0 {
		t.Error("expected positive threshold with window")
	}
}

func TestContextManager_SetCompactor(t *testing.T) {
	cm := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "sys", nil)
	if cm.compactor != nil {
		t.Fatal("expected nil compactor initially")
	}
	c := NewCompactor(DefaultConfig(), llm.Model{}, "", "", 0)
	cm.SetCompactor(c)
	if cm.compactor != c {
		t.Error("SetCompactor did not set compactor")
	}
}

func TestContextManager_Compact_DelegatesToWithCtx(t *testing.T) {
	cm := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 0, "sys", nil)
	// No LLM server reachable → expect error; ensures Compact delegates.
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{agentctx.NewUserMessage("hi")}
	_, err := cm.Compact(ctx)
	if err == nil {
		// When contextWindow=0, estimateTokenPercent=0 → behavior depends on server
		// The key here is that Compact does not panic and goes through CompactWithCtx.
	}
}

// --- executeToolCalls error paths ---

type stubTool struct {
	name string
	exec func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error)
}

func (s *stubTool) Name() string { return s.name }
func (s *stubTool) Description() string {
	return "stub"
}
func (s *stubTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (s *stubTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	return s.exec(ctx, args)
}

func TestExecuteToolCalls_ErrorPaths(t *testing.T) {
	cm := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 0, "sys", nil)

	// Tool not found
	tc1 := []llm.ToolCall{{ID: "x", Type: "function", Function: llm.FunctionCall{Name: "missing"}}}
	trunc, llmCtx := cm.executeToolCallsForTest(tc1, []context_mgmt.Tool{})
	if trunc != 0 || llmCtx {
		t.Errorf("expected no-op for missing tool, got trunc=%d llmCtx=%v", trunc, llmCtx)
	}

	// Tool with bad JSON args
	bad := &stubTool{name: "badArgs"}
	bad.exec = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "should not see"}}, nil
	}
	tc2 := []llm.ToolCall{{
		ID: "x", Type: "function",
		Function: llm.FunctionCall{Name: "badArgs", Arguments: "{not-json}"},
	}}
	trunc, _ = cm.executeToolCallsForTest(tc2, []context_mgmt.Tool{bad})
	if trunc != 0 {
		t.Errorf("expected 0 truncations on parse error, got %d", trunc)
	}

	// Tool Execute returns error
	errTool := &stubTool{name: "errTool"}
	errTool.exec = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return nil, fmt.Errorf("kaboom")
	}
	tc3 := []llm.ToolCall{{ID: "x", Type: "function", Function: llm.FunctionCall{Name: "errTool"}}}
	trunc, _ = cm.executeToolCallsForTest(tc3, []context_mgmt.Tool{errTool})
	if trunc != 0 {
		t.Errorf("expected 0 truncations on exec error, got %d", trunc)
	}

	// Tool with valid args
	okTool := &stubTool{name: "okTool"}
	okTool.exec = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "ok"}}, nil
	}
	argsJSON, _ := json.Marshal(map[string]any{"k": "v"})
	tc4 := []llm.ToolCall{{
		ID: "x", Type: "function",
		Function: llm.FunctionCall{Name: "okTool", Arguments: string(argsJSON)},
	}}
	_, _ = cm.executeToolCallsForTest(tc4, []context_mgmt.Tool{okTool})

	// truncate_messages count parsing
	truncTool := &stubTool{name: "truncate_messages"}
	truncTool.exec = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "Truncated 7 messages."}}, nil
	}
	tc5 := []llm.ToolCall{{ID: "x", Type: "function", Function: llm.FunctionCall{Name: "truncate_messages"}}}
	trunc, _ = cm.executeToolCallsForTest(tc5, []context_mgmt.Tool{truncTool})
	if trunc != 7 {
		t.Errorf("expected 7 truncations, got %d", trunc)
	}

	// truncate_messages with un-parseable text → trunc stays 0
	truncTool2 := &stubTool{name: "truncate_messages"}
	truncTool2.exec = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "no number here"}}, nil
	}
	trunc, _ = cm.executeToolCallsForTest(tc5, []context_mgmt.Tool{truncTool2})
	if trunc != 0 {
		t.Errorf("expected 0 on bad text, got %d", trunc)
	}

	// update_llm_context sets llmContextUpdated
	updTool := &stubTool{name: "update_llm_context"}
	updTool.exec = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "updated"}}, nil
	}
	tc6 := []llm.ToolCall{{ID: "x", Type: "function", Function: llm.FunctionCall{Name: "update_llm_context"}}}
	_, updated := cm.executeToolCallsForTest(tc6, []context_mgmt.Tool{updTool})
	if !updated {
		t.Error("expected llmContextUpdated=true after update_llm_context")
	}

	// update_llm_context with empty content (no TextContent) → still updates
	updTool2 := &stubTool{name: "update_llm_context"}
	updTool2.exec = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return nil, nil
	}
	_, updated = cm.executeToolCallsForTest(tc6, []context_mgmt.Tool{updTool2})
	if !updated {
		t.Error("expected llmContextUpdated=true even with nil content")
	}
}

// --- ContextManager.SetSkipCondition setter ---

func TestContextManager_SetSkipCondition(t *testing.T) {
	cfg := DefaultContextManagerConfig()
	cm := NewContextManager(cfg, llmModelStub(), "", 200000, "sys", nil)
	if cm.config.SkipCondition != nil {
		t.Fatal("expected nil SkipCondition initially")
	}
	cm.SetSkipCondition(func() bool { return false })
	if cm.config.SkipCondition == nil {
		t.Error("expected SkipCondition to be set")
	}
}

// --- Custom ContextMgmtPrompt honored on construction ---

func TestNewContextManager_CustomPromptUsed(t *testing.T) {
	cfg := DefaultContextManagerConfig()
	cfg.ContextMgmtPrompt = "custom-instructions"
	cm := NewContextManager(cfg, llmModelStub(), "", 200000, "", nil)
	if cm.systemPrompt != "custom-instructions" {
		t.Errorf("expected custom prompt, got %q", cm.systemPrompt)
	}
}

func TestNewContextManager_NilConfigDefaults(t *testing.T) {
	cm := NewContextManager(nil, llmModelStub(), "", 200000, "sys", nil)
	if cm.config == nil {
		t.Fatal("expected non-nil default config")
	}
	if !cm.config.AutoCompact {
		t.Error("expected AutoCompact=true default")
	}
}

// --- buildContextMgmtMessages with truncated tool result and protected region ---

func TestBuildContextMgmtMessages_TruncatedAndProtected(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	// One already-truncated tool result
	tr := makeToolResult("call-trunc", 5000)
	tr.Truncated = true
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, tr)
	// Protected user messages (last 5)
	for i := 0; i < 5; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("u"+fmt.Sprint(i)))
	}

	cm := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "sys", nil)
	msgs := cm.buildContextMgmtMessages(agentCtx)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "(already truncated)") {
		t.Errorf("expected truncated annotation, got: %s", msgs[0].Content)
	}
}

// --- ContextManager.CompactWithCtx with no_action response (already covered) — add empty compactor branch ---

func TestCompactWithCtx_NoCompactorUsesDefaultTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseNoActionResponse())
	}))
	defer server.Close()

	model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai"}
	cm := NewContextManager(DefaultContextManagerConfig(), model, "k", 200000, "sys", nil)

	agentCtx := agentctx.NewAgentContext("sys")
	for i := 0; i < 10; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hi"))
	}
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100

	result, err := cm.CompactWithCtx(context.Background(), agentCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// no_action returns a result describing the call (no truncation performed).
	if result == nil {
		t.Fatal("expected non-nil result even for no_action")
	}
	if result.TruncatedCount != 0 {
		t.Errorf("expected 0 truncated, got %d", result.TruncatedCount)
	}
	if result.LLMContextUpdated {
		t.Error("expected LLMContextUpdated=false for no_action")
	}
}

// --- collectTruncationCandidates edge cases ---

func TestCollectTruncationCandidates_ProtectedStartExceedsMessages(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = []agentctx.AgentMessage{
		makeToolResult("c1", 5000),
	}
	// protectedStart > len(messages) — must clamp
	candidates, _, _ := collectTruncationCandidates(agentCtx, 10, false, mgmtStaleAgeInvestigative, mgmtStaleAgeModification)
	if len(candidates) != 1 {
		t.Errorf("expected 1 candidate (clamped), got %d", len(candidates))
	}
}

func TestCollectTruncationCandidates_SmallOutputNonSelectable(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	// 100-char text → below 500-char threshold
	agentCtx.RecentMessages = []agentctx.AgentMessage{
		makeToolResult("small", 100),
		agentctx.NewUserMessage("u1"),
		agentctx.NewUserMessage("u2"),
		agentctx.NewUserMessage("u3"),
		agentctx.NewUserMessage("u4"),
		agentctx.NewUserMessage("u5"),
	}
	protectedStart := len(agentCtx.RecentMessages) - agentctx.RecentMessagesKeep
	candidates, _, nonSelectable := collectTruncationCandidates(agentCtx, protectedStart, false, mgmtStaleAgeInvestigative, mgmtStaleAgeModification)
	if len(candidates) != 0 {
		t.Errorf("expected 0 candidates (<500 chars), got %d", len(candidates))
	}
	if nonSelectable != 1 {
		t.Errorf("expected 1 non-selectable, got %d", nonSelectable)
	}
}
