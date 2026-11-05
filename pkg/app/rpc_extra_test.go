package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/testutil"
)

// --- sessionCompactor ---

type stubCompactor struct {
	should bool
	result *agentctx.CompactionResult
	err    error
	calls  int
}

func (s *stubCompactor) ShouldCompact(_ context.Context, _ *agentctx.AgentContext) bool {
	return s.should
}

func (s *stubCompactor) Compact(_ context.Context, _ *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	s.calls++
	return s.result, s.err
}

func TestSessionCompactorNil(t *testing.T) {
	sc := &sessionCompactor{}
	if sc.ShouldCompact(context.Background(), &agentctx.AgentContext{}) {
		t.Error("nil compactor should not compact")
	}
	res, err := sc.Compact(context.Background(), &agentctx.AgentContext{})
	if res != nil || err != nil {
		t.Errorf("nil compactor Compact = %v, %v; want nil, nil", res, err)
	}
}

func TestSessionCompactorDelegation(t *testing.T) {
	stub := &stubCompactor{should: true, result: &agentctx.CompactionResult{}}
	sc := &sessionCompactor{compactor: stub}

	agentCtx := &agentctx.AgentContext{}
	if !sc.ShouldCompact(context.Background(), agentCtx) {
		t.Error("expected ShouldCompact to delegate true")
	}
	res, err := sc.Compact(context.Background(), agentCtx)
	if err != nil || res != stub.result {
		t.Errorf("Compact = %v, %v; want stub result, nil", res, err)
	}
	if stub.calls != 1 {
		t.Errorf("stub.calls = %d; want 1", stub.calls)
	}

	// Update swaps the compactor.
	stub2 := &stubCompactor{}
	sc.Update(stub2)
	if sc.ShouldCompact(context.Background(), agentCtx) {
		t.Error("expected new stub to return false")
	}
}

// --- expandSkillCommands ---

func TestExpandSkillCommandsNilResult(t *testing.T) {
	app := &App{}
	got := app.expandSkillCommands("/skill:foo bar")
	if got != "/skill:foo bar" {
		t.Errorf("nil skillResult should return text unchanged, got %q", got)
	}
}

func TestExpandSkillCommands(t *testing.T) {
	app := &App{
		skillResult: &skill.LoadResult{
			Skills: []skill.Skill{
				{Name: "demo", Content: "do the thing"},
			},
		},
		skillStats: skill.LoadStats(t.TempDir()), // stats file in temp dir
	}

	got := app.expandSkillCommands("/skill:demo extra args")
	if !strings.Contains(got, `<skill name="demo"`) {
		t.Errorf("expected expanded skill block, got %q", got)
	}
	if !strings.Contains(got, "extra args") {
		t.Errorf("expected args appended, got %q", got)
	}

	// Non-skill text passes through unchanged.
	if got := app.expandSkillCommands("plain text"); got != "plain text" {
		t.Errorf("plain text changed: %q", got)
	}

	// Unknown skill stays unchanged.
	if got := app.expandSkillCommands("/skill:missing"); got != "/skill:missing" {
		t.Errorf("unknown skill should stay unchanged, got %q", got)
	}

	// Usage was recorded for "demo".
	if e := app.skillStats.Entries["demo"]; e == nil || e.Count != 1 {
		t.Errorf("expected usage count 1 for demo, got %v", e)
	}
}

func TestHandlePromptUsesBusyMode(t *testing.T) {
	app := &App{isStreaming: true, busyMode: "reject"}
	_, err := app.handlePrompt("hello", true, "", false)
	if err == nil || !strings.Contains(err.Error(), "rejected by busy-mode policy") {
		t.Fatalf("busy mode reject was not applied: %v", err)
	}
}

// --- slash handler error branches (no agent required) ---

func TestHandleSteerSlashUsage(t *testing.T) {
	app := &App{}
	if _, err := app.handleSteerSlash("   "); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("empty steer should fail with usage error, got %v", err)
	}
}

func TestHandleSteerSlashQueueFull(t *testing.T) {
	// First response is slow so the agent stays busy while we steer.
	srv := testutil.LLMServerFactory(func(i int, r *http.Request) string {
		if i == 0 {
			time.Sleep(300 * time.Millisecond)
			return testutil.TextResponse("first")
		}
		return testutil.TextResponse("steered")
	})
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}
	app := &App{steeringMode: "one-at-a-time", ag: agent.NewAgent(model, "test-key", "You are helpful.")}
	app.ag.SetSteerQueueDepth(1)

	if err := app.ag.Prompt("initial"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	// Wait until the first LLM call is in flight.
	time.Sleep(50 * time.Millisecond)

	// One-at-a-time: the first steer queues, the second is rejected with the
	// queue-full error instead of being silently dropped.
	if _, err := app.handleSteerSlash("first steer"); err != nil {
		t.Fatalf("first steer should queue while busy, got %v", err)
	}
	if _, err := app.handleSteerSlash("second steer"); err == nil || !strings.Contains(err.Error(), "steer queue full") {
		t.Errorf("expected queue full error, got %v", err)
	}

	app.ag.Wait()
}

func TestHandleFollowUpSlashUsage(t *testing.T) {
	app := &App{}
	if _, err := app.handleFollowUpSlash("   "); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("empty follow-up should fail with usage error, got %v", err)
	}
}

// TestHandleFollowUpSlashIdleRunsPrompt locks in that /follow-up on an idle
// agent runs the message as a normal prompt instead of failing: nothing is
// running to follow up on, and the follow-up channel is only drained by a
// running turn, so queuing here would strand the message.
func TestHandleFollowUpSlashIdleRunsPrompt(t *testing.T) {
	var body string
	srv := testutil.LLMServerFactory(func(i int, r *http.Request) string {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		return testutil.TextResponse("ok")
	})
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}
	app := &App{followUpMode: "one-at-a-time", ag: agent.NewAgent(model, "test-key", "You are helpful.")}

	if _, err := app.handleFollowUpSlash("FOLLOW-IDLE"); err != nil {
		t.Fatalf("idle follow-up should run as a prompt, got %v", err)
	}
	app.ag.Wait()

	if !strings.Contains(body, "FOLLOW-IDLE") {
		t.Errorf("follow-up message never reached the model:\n%s", body)
	}
	if n := app.ag.GetPendingFollowUps(); n != 0 {
		t.Errorf("follow-up channel should be empty, has %d pending", n)
	}
}

// TestHandleFollowUpSlashBusyQueues covers the busy path: every valid
// follow-up mode queues, and one-at-a-time rejects a second pending message.
func TestHandleFollowUpSlashBusyQueues(t *testing.T) {
	srv := testutil.LLMServerFactory(func(i int, r *http.Request) string {
		if i == 0 {
			time.Sleep(300 * time.Millisecond)
		}
		return testutil.TextResponse("ok")
	})
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}

	for _, mode := range []string{"all", "immediate", "one-at-a-time"} {
		t.Run(mode, func(t *testing.T) {
			app := &App{followUpMode: mode, isStreaming: true, ag: agent.NewAgent(model, "test-key", "You are helpful.")}
			if err := app.ag.Prompt("initial"); err != nil {
				t.Fatalf("Prompt failed: %v", err)
			}
			time.Sleep(50 * time.Millisecond)

			wantPending := 0
			for i, msg := range []string{"first follow-up", "second follow-up", "third follow-up"} {
				_, err := app.handleFollowUpSlash(msg)
				if mode == "one-at-a-time" && i > 0 {
					if err == nil || !strings.Contains(err.Error(), "already has a pending message") {
						t.Errorf("mode %q: message %d should be rejected when queue is full, got %v", mode, i+1, err)
					}
					continue
				}
				if err != nil {
					t.Errorf("mode %q: message %d should queue while busy, got %v", mode, i+1, err)
					continue
				}
				wantPending++
			}
			if n := app.ag.GetPendingFollowUps(); n != wantPending {
				t.Errorf("mode %q: pending follow-ups = %d, want %d", mode, n, wantPending)
			}

			app.ag.Wait()
		})
	}
}

func TestHandleAbortSlashNotStreaming(t *testing.T) {
	app := &App{}
	if _, err := app.handleAbortSlash(""); err == nil || !strings.Contains(err.Error(), "not streaming") {
		t.Errorf("expected 'not streaming' error, got %v", err)
	}
}

func TestHandleRewindUsage(t *testing.T) {
	app := &App{}
	if _, err := app.handleRewind(""); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("empty rewind should fail with usage error, got %v", err)
	}
}

// --- /set handler ---

func TestSetUsage(t *testing.T) {
	usage := SetUsage()
	if usage["usage"] != "/set <key> [value]" {
		t.Errorf("usage field = %v", usage["usage"])
	}
	settings, ok := usage["settings"].([]string)
	if !ok || len(settings) < 10 {
		t.Errorf("expected settings list, got %v", usage["settings"])
	}
}
