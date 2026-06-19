package testutil

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// HarnessOption configures an AgentHarness.
type HarnessOption func(*harnessConfig)

type harnessConfig struct {
	model        llm.Model
	maxTurns     int
	tools        []agentctx.Tool
	compactors   []agentctx.Compactor
	systemPrompt string
	apiKey       string
	// Advanced options
	maxConsecutiveToolCalls int
}

// WithMaxTurns limits the number of agent loop turns.
func WithMaxTurns(n int) HarnessOption {
	return func(c *harnessConfig) { c.maxTurns = n }
}

// WithTools adds tools to the agent context.
func WithTools(tools ...agentctx.Tool) HarnessOption {
	return func(c *harnessConfig) { c.tools = append(c.tools, tools...) }
}

// WithCompactors sets compaction strategies on the agent.
func WithCompactors(c ...agentctx.Compactor) HarnessOption {
	return func(cfg *harnessConfig) { cfg.compactors = append(cfg.compactors, c...) }
}

// AgentHarness is a fully-wired test harness for agent.Agent.
//
// It creates an Agent with a mock LLM server (via httptest) and provides
// convenience methods for sending prompts, collecting events, and asserting
// on behavior.
//
// Usage:
//
//	h := NewAgentHarness(t,
//	    TextResponse("hello"),
//	    WithTools(EchoTool("echo")),
//	    WithMaxTurns(3),
//	)
//	defer h.Close()
//
//	h.PromptAndWait("say hello", 10*time.Second)
//	assert.True(t, h.Events.HasEvent(agent.EventAgentEnd))
type AgentHarness struct {
	Agent     *agent.Agent
	AgentCtx  *agentctx.AgentContext
	Server    *httptest.Server // mock LLM server
	Events    *EventCollector
	CallCount int // number of LLM calls made

	t       *testing.T
	stopSub func()
}

// NewAgentHarness creates a fully-wired agent with a mock LLM server.
//
// Each response string is served in order per LLM call. The server is created
// via LLMServer, so the responses are SSE-formatted strings (use SSEBuilder
// or the TextResponse/ToolCallResponse helpers).
//
// Options override defaults: model=test-model, apiKey=test-key,
// systemPrompt="You are a test assistant.", maxTurns=0 (unlimited).
func NewAgentHarness(t *testing.T, responses []string, opts ...HarnessOption) *AgentHarness {
	t.Helper()

	cfg := &harnessConfig{
		model: llm.Model{
			ID:       "test-model",
			Provider: "test",
			API:      "openai-completions",
		},
		apiKey:       "test-key",
		systemPrompt: "You are a test assistant.",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Create mock LLM server
	callCount := 0
	srv := LLMServerFactory(func(callIndex int, _ *http.Request) string {
		callCount++
		if callIndex >= len(responses) {
			return TextResponse(fmt.Sprintf("mock response %d", callIndex))
		}
		return responses[callIndex]
	})

	// Update model BaseURL to point at mock server
	cfg.model.BaseURL = srv.URL

	// Create agent context and add tools
	agentCtx := agentctx.NewAgentContext(cfg.systemPrompt)
	for _, tool := range cfg.tools {
		agentCtx.Tools = append(agentCtx.Tools, tool)
	}

	// Create loop config
	loopCfg := agent.DefaultLoopConfig()
	if cfg.maxTurns > 0 {
		loopCfg.MaxTurns = cfg.maxTurns
	}
	if cfg.maxConsecutiveToolCalls > 0 {
		loopCfg.MaxConsecutiveToolCalls = cfg.maxConsecutiveToolCalls
	}

	loopCfg.Compactors = cfg.compactors

	// Create agent
	a := agent.NewAgentFromConfigWithContext(cfg.model, cfg.apiKey, agentCtx, loopCfg)

	// Wire up event collection
	collector := NewEventCollector()
	unsub := collector.Subscribe(a.Events())

	return &AgentHarness{
		Agent:    a,
		AgentCtx: agentCtx,
		Server:   srv,
		Events:   collector,
		t:        t,
		stopSub:  unsub,
	}
}

// Prompt sends a user message to the agent. Does not wait for completion.
func (h *AgentHarness) Prompt(msg string) {
	h.t.Helper()
	if err := h.Agent.Prompt(msg); err != nil {
		h.t.Fatalf("Prompt failed: %v", err)
	}
}

// PromptAndWait sends a user message and waits for the agent to finish processing.
func (h *AgentHarness) PromptAndWait(msg string, timeout time.Duration) {
	h.t.Helper()
	h.Prompt(msg)
	h.Wait(timeout)
}

// Wait blocks until the agent finishes or the timeout expires.
// After the agent completes, it briefly yields to let the event subscriber
// drain remaining events from the channel.
func (h *AgentHarness) Wait(timeout time.Duration) {
	h.t.Helper()
	done := make(chan struct{})
	go func() {
		h.Agent.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Give the subscriber goroutine time to drain remaining events.
		time.Sleep(10 * time.Millisecond)
	case <-time.After(timeout):
		h.t.Fatalf("Agent did not finish within %v", timeout)
	}
}

// FollowUp sends a follow-up message to the running agent.
func (h *AgentHarness) FollowUp(msg string) {
	h.t.Helper()
	if err := h.Agent.FollowUp(msg); err != nil {
		h.t.Fatalf("FollowUp failed: %v", err)
	}
}

// Close tears down the harness: stops event subscription, closes mock server.
func (h *AgentHarness) Close() {
	h.stopSub()
	h.Server.Close()
}
