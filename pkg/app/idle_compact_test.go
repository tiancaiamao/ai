package app

import (
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/testutil"
)

// newIdleCompactApp builds an App around a harness agent whose context is small
// enough that softThreshold decides whether the idle path runs.
func newIdleCompactApp(t *testing.T, softThreshold int) (*App, *testutil.AgentHarness) {
	t.Helper()
	h := testutil.NewAgentHarness(t, nil)
	t.Cleanup(h.Close)

	compactor := compact.NewCompactor(
		&compact.Config{
			AutoCompact: true,
			LLMDecide:   &compact.LLMDecideConfig{SoftThreshold: softThreshold},
		},
		llm.Model{ID: "test-model", Provider: "anthropic"},
		"test-key", "system", 200_000, "",
	)

	app := &App{
		ag:                    h.Agent,
		agentCtx:              h.AgentCtx,
		compactor:             compactor,
		autoCompactionEnabled: true,
		model:                 llm.Model{ID: "test-model", Provider: "anthropic"},
	}
	return app, h
}

// withIdleRunner replaces the compaction body for the duration of a test.
func withIdleRunner(t *testing.T, fn func(*App, string) (*CompactResult, error)) {
	t.Helper()
	orig := idleCompactionRun
	idleCompactionRun = fn
	t.Cleanup(func() { idleCompactionRun = orig })
}

func (app *App) testIdleEpoch() uint64 {
	app.idleMu.Lock()
	defer app.idleMu.Unlock()
	return app.idleEpoch
}

// Idle compaction runs when the context has reached the "ask for compact"
// threshold and the agent is idle.
func TestIdleCompactionRunsAboveThreshold(t *testing.T) {
	app, h := newIdleCompactApp(t, 1)

	var triggers []string
	withIdleRunner(t, func(_ *App, trigger string) (*CompactResult, error) {
		triggers = append(triggers, trigger)
		return &CompactResult{}, nil
	})

	app.scheduleIdleCompaction()
	tokens := h.AgentCtx.EstimateTokens()
	if tokens < 1 {
		t.Fatalf("harness context estimated at %d tokens, expected non-zero", tokens)
	}
	app.runIdleCompaction(app.testIdleEpoch())

	if len(triggers) != 1 || triggers[0] != "idle" {
		t.Fatalf("triggers = %v, want exactly one \"idle\"", triggers)
	}
}

// Below the soft threshold an idle compaction saves nothing: skip it.
func TestIdleCompactionSkippedBelowThreshold(t *testing.T) {
	app, _ := newIdleCompactApp(t, 10_000_000)

	called := false
	withIdleRunner(t, func(*App, string) (*CompactResult, error) {
		called = true
		return &CompactResult{}, nil
	})

	app.runIdleCompaction(app.testIdleEpoch())
	if called {
		t.Fatal("idle compaction ran below the soft threshold")
	}
}

// A turn in flight must never be compacted underneath.
func TestIdleCompactionSkippedWhileStreaming(t *testing.T) {
	app, _ := newIdleCompactApp(t, 1)

	called := false
	withIdleRunner(t, func(*App, string) (*CompactResult, error) {
		called = true
		return &CompactResult{}, nil
	})

	app.stateMu.Lock()
	app.isStreaming = true
	app.stateMu.Unlock()

	app.runIdleCompaction(app.testIdleEpoch())
	if called {
		t.Fatal("idle compaction ran while the agent was streaming")
	}
}

// Disabling auto-compaction is an explicit user opt-out: the idle trigger must
// not rewrite the conversation behind it.
func TestIdleCompactionSkippedWhenAutoCompactionDisabled(t *testing.T) {
	app, _ := newIdleCompactApp(t, 1)
	app.autoCompactionEnabled = false

	called := false
	withIdleRunner(t, func(*App, string) (*CompactResult, error) {
		called = true
		return &CompactResult{}, nil
	})

	app.runIdleCompaction(app.testIdleEpoch())
	if called {
		t.Fatal("idle compaction ran while auto-compaction was disabled")
	}
}

// A timer that fires after the user came back is stale: the epoch changed, so
// it must not compact.
func TestIdleCompactionAbortsWhenSuperseded(t *testing.T) {
	app, _ := newIdleCompactApp(t, 1)

	called := false
	withIdleRunner(t, func(*App, string) (*CompactResult, error) {
		called = true
		return &CompactResult{}, nil
	})

	app.scheduleIdleCompaction()
	staleEpoch := app.testIdleEpoch()
	app.cancelIdleCompaction() // simulates user input arriving

	app.runIdleCompaction(staleEpoch)
	if called {
		t.Fatal("stale idle timer compacted after user input")
	}
}

// User prompts queue behind an in-flight idle compaction instead of racing it.
func TestIdleCompactionQueuesPrompts(t *testing.T) {
	app, _ := newIdleCompactApp(t, 1)

	started := make(chan struct{})
	release := make(chan struct{})
	withIdleRunner(t, func(*App, string) (*CompactResult, error) {
		close(started)
		<-release
		return &CompactResult{}, nil
	})

	go app.runIdleCompaction(app.testIdleEpoch())
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("idle compaction never started")
	}

	waited := make(chan struct{})
	go func() {
		app.waitForIdleCompaction()
		close(waited)
	}()

	select {
	case <-waited:
		t.Fatal("waitForIdleCompaction returned while compaction was still running")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("waitForIdleCompaction did not return after compaction finished")
	}
}

// The timer is armed only for providers whose cache TTL is known, and user
// input cancels it.
func TestScheduleIdleCompactionArmsAndCancelsTimer(t *testing.T) {
	tests := []struct {
		provider string
		armed    bool
	}{
		{"anthropic", true},
		{"deepseek", false},
		{"", false},
	}
	for _, c := range tests {
		t.Run(c.provider, func(t *testing.T) {
			app := &App{model: llm.Model{Provider: c.provider}}
			app.scheduleIdleCompaction()
			t.Cleanup(app.cancelIdleCompaction)

			armed := app.idleTimer != nil
			if armed != c.armed {
				t.Fatalf("provider %q: timer armed = %v, want %v", c.provider, armed, c.armed)
			}

			app.cancelIdleCompaction()
			if app.idleTimer != nil {
				t.Fatalf("provider %q: cancelIdleCompaction left a timer armed", c.provider)
			}
		})
	}
}

// handlePrompt is the user-input hook: it cancels the pending idle compaction.
func TestHandlePromptCancelsIdleTimer(t *testing.T) {
	app, h := newIdleCompactApp(t, 1)

	app.scheduleIdleCompaction()
	epochBefore := app.testIdleEpoch()

	if _, err := app.handlePrompt("hello", false, ""); err != nil {
		t.Fatalf("handlePrompt failed: %v", err)
	}
	h.Wait(5 * time.Second)

	if app.idleTimer != nil {
		t.Fatal("handlePrompt did not cancel the pending idle compaction")
	}
	if app.testIdleEpoch() == epochBefore {
		t.Fatal("handlePrompt did not invalidate the scheduled idle epoch")
	}
}

// End-to-end shape of the idle path: a real compaction runs (against the mock
// LLM), the context shrinks, and the emitted events identify the trigger.
func TestIdleCompactionShrinksContext(t *testing.T) {
	h := testutil.NewAgentHarness(t, nil)
	t.Cleanup(h.Close)

	compactor := compact.NewCompactor(
		&compact.Config{AutoCompact: true, LLMDecide: &compact.LLMDecideConfig{SoftThreshold: 1}},
		llm.Model{
			ID:       "test-model",
			Provider: "anthropic",
			API:      "openai-completions",
			BaseURL:  h.Server.URL,
		},
		"test-key", "You are a test assistant.", 200_000, "",
	)
	app := &App{
		ag:                    h.Agent,
		agentCtx:              h.AgentCtx,
		compactor:             compactor,
		autoCompactionEnabled: true,
		model:                 llm.Model{ID: "test-model", Provider: "anthropic"},
	}
	var events []agent.AgentEvent
	app.emitEvent = func(ev agent.AgentEvent) { events = append(events, ev) }

	// Enough messages that compaction has something to split off.
	for i := 0; i < 60; i++ {
		h.AgentCtx.RecentMessages = append(h.AgentCtx.RecentMessages, agentctx.NewUserMessage("message"))
	}
	before := len(h.AgentCtx.RecentMessages)

	app.runIdleCompaction(app.testIdleEpoch())

	after := len(h.AgentCtx.RecentMessages)
	if after >= before {
		t.Fatalf("context not compacted: %d → %d messages", before, after)
	}

	var start, end *agent.CompactionInfo
	for i := range events {
		switch events[i].Type {
		case agent.EventCompactionStart:
			start = events[i].Compaction
		case agent.EventCompactionEnd:
			end = events[i].Compaction
		}
	}
	if start == nil || end == nil {
		t.Fatalf("missing compaction events, got %d events", len(events))
	}
	for _, info := range []*agent.CompactionInfo{start, end} {
		if info.Trigger != "idle" {
			t.Errorf("trigger = %q, want \"idle\"", info.Trigger)
		}
		if !info.Auto {
			t.Error("idle compaction should be marked auto")
		}
		if info.Error != "" {
			t.Errorf("unexpected compaction error: %s", info.Error)
		}
	}
}
