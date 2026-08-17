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

// sseTwoLineResponse streams an SSE completion with a two-line answer.
func sseTwoLineResponse(line1, line2 string) string {
	content := line1
	if line2 != "" {
		content += "\n" + line2
	}
	var sb strings.Builder
	sb.WriteString(`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":` + jsonString(content) + `},"finish_reason":null}]}` + "\n\n")
	sb.WriteString(`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
	sb.WriteString(`data: [DONE]` + "\n\n")
	return sb.String()
}

// jsonString escapes a Go string into a JSON string literal (sufficient for tests).
func jsonString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func newAskTestCtx() *agentctx.AgentContext {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = append(ctx.RecentMessages, agentctx.NewUserMessage("hello"))
	return ctx
}

// askTestConfig returns a config with LLMDecide enabled (askLLM requires it).
func askTestConfig() *Config {
	cfg := DefaultConfig()
	cfg.LLMDecide = &LLMDecideConfig{
		SoftThreshold: 10,
		HardLimit:     100000,
	}
	return cfg
}

func TestAskLLM_ConfirmAndReject(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     bool
	}{
		{"confirm", "confirm", true},
		{"reject", "reject", false},
		{"confirm with text", "Yes, confirm please", true},
		{"garbage means no compact", "banana", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, sseTwoLineResponse(tt.response, ""))
			}))
			defer server.Close()

			model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai"}
			c := NewCompactor(askTestConfig(), model, "k", "sys", 0, "")
			got, err := c.askLLM(context.Background(), newAskTestCtx(), 1000)
			if err != nil {
				t.Fatalf("askLLM error: %v", err)
			}
			if got != tt.want {
				t.Errorf("askLLM(%q response) = %v, want %v", tt.response, got, tt.want)
			}
		})
	}
}

func TestAskLLM_CanaryCheck(t *testing.T) {
	tests := []struct {
		name   string
		canary string
		line1  string
		line2  string
		want   bool
	}{
		{"canary ok + confirm", "secret-canary-123", "secret-canary-123", "confirm", true},
		{"canary ok + reject", "secret-canary-123", "secret-canary-123", "reject", false},
		{"canary mismatch forces compact", "secret-canary-123", "wrong-value", "reject", true},
		{"canary with backticks", "secret-canary-123", "`secret-canary-123`", "confirm", true},
		{"canary substring accepted", "secret-canary-123", "value: secret-canary-123 here", "confirm", true},
		{"case insensitive canary", "ABC-def", "abc-DEF", "confirm", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, sseTwoLineResponse(tt.line1, tt.line2))
			}))
			defer server.Close()

			model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai"}
			c := NewCompactor(askTestConfig(), model, "k", "sys", 0, "")
			c.SetCanaryValue(tt.canary)

			got, err := c.askLLM(context.Background(), newAskTestCtx(), 1000)
			if err != nil {
				t.Fatalf("askLLM error: %v", err)
			}
			if got != tt.want {
				t.Errorf("canary case %q: askLLM = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestAskLLM_LLMError(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"bad key"}}`)
	}))
	defer server.Close()

	model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai"}
	c := NewCompactor(askTestConfig(), model, "k", "sys", 0, "")
	_, err := c.askLLM(context.Background(), newAskTestCtx(), 1000)
	if err == nil {
		t.Fatal("expected error from failing LLM")
	}
}

func TestAskLLM_ThinkingFallback(t *testing.T) {
	// Model responds with reasoning content only; the text answer is empty.
	// askLLM should fall back to the thinking text for decision parsing.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"confirm"},"finish_reason":null}]}`+"\n\n")
		fmt.Fprint(w, `data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n")
		fmt.Fprint(w, `data: [DONE]`+"\n\n")
	}))
	defer server.Close()

	model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai"}
	c := NewCompactor(askTestConfig(), model, "k", "sys", 0, "")
	got, err := c.askLLM(context.Background(), newAskTestCtx(), 1000)
	if err != nil {
		t.Fatalf("askLLM error: %v", err)
	}
	if !got {
		t.Error("thinking fallback 'confirm' should compact")
	}
}

func TestCompactorSetters(t *testing.T) {
	c := NewCompactor(nil, llm.Model{}, "k", "sys", 0, "")
	c.SetCanaryValue("v")
	if c.canaryValue != "v" {
		t.Errorf("canaryValue = %q, want 'v'", c.canaryValue)
	}
	c.SetAgentContextPrefix("prefix")
	if c.agentContextPrefix != "prefix" {
		t.Errorf("agentContextPrefix = %q, want 'prefix'", c.agentContextPrefix)
	}
	c.SetThinkingLevel("high")
	if c.thinkingLevel != "high" {
		t.Errorf("thinkingLevel = %q, want 'high'", c.thinkingLevel)
	}
}

func TestShouldCompactLLMDecide_HardLimitAndInterval(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LLMDecide = &LLMDecideConfig{
		SoftThreshold: 10,
		HardLimit:     100,
		TierMedium:    40,
		TierHigh:      70,
		IntervalLow:   1,
	}

	askCalls := 0
	c := NewCompactor(cfg, llm.Model{}, "k", "sys", 0, "")
	c.askFunc = func(ctx context.Context, actx *agentctx.AgentContext, tokens int) (bool, error) {
		askCalls++
		return false, nil // LLM says no
	}

	// Below soft threshold → no ask.
	smallCtx := agentctx.NewAgentContext("sys")
	smallCtx.RecentMessages = append(smallCtx.RecentMessages, agentctx.NewUserMessage("hi"))
	c.ShouldCompact(context.Background(), smallCtx)
	if askCalls != 0 {
		t.Errorf("below soft threshold: askCalls = %d, want 0", askCalls)
	}

	// Above hard limit → compact without asking.
	bigCtx := agentctx.NewAgentContext("sys")
	for i := 0; i < 20; i++ {
		bigCtx.RecentMessages = append(bigCtx.RecentMessages,
			agentctx.NewUserMessage(strings.Repeat("word ", 100)))
	}
	c.ShouldCompact(context.Background(), bigCtx)
	if askCalls != 0 {
		t.Errorf("above hard limit: askCalls = %d, want 0 (no ask)", askCalls)
	}

	// Between soft and hard → ask (interval reached after 1 tool call).
	midCtx := agentctx.NewAgentContext("sys")
	midCtx.RecentMessages = append(midCtx.RecentMessages,
		agentctx.NewUserMessage(strings.Repeat("word ", 15)))
	midCtx.AgentState.ToolCallsSinceLastTrigger = 1
	c.ShouldCompact(context.Background(), midCtx)
	if askCalls != 1 {
		t.Errorf("between thresholds: askCalls = %d, want 1", askCalls)
	}

	// Immediately re-check — interval not elapsed → no re-ask.
	c.ShouldCompact(context.Background(), midCtx)
	if askCalls != 1 {
		t.Errorf("interval not elapsed: askCalls = %d, want 1", askCalls)
	}
}
