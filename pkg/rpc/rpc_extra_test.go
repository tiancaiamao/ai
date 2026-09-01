package rpc

import (
	"context"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/skill"
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
	app := &rpcApp{}
	got := app.expandSkillCommands("/skill:foo bar")
	if got != "/skill:foo bar" {
		t.Errorf("nil skillResult should return text unchanged, got %q", got)
	}
}

func TestExpandSkillCommands(t *testing.T) {
	app := &rpcApp{
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

// --- slash handler error branches (no agent required) ---

func TestHandleSteerSlashUsage(t *testing.T) {
	app := &rpcApp{}
	if _, err := app.handleSteerSlash("   "); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("empty steer should fail with usage error, got %v", err)
	}
}

func TestHandleSteerSlashPending(t *testing.T) {
	app := &rpcApp{steeringMode: "one-at-a-time", pendingSteer: true}
	_, err := app.handleSteerSlash("hello")
	if err == nil || !strings.Contains(err.Error(), "pending") {
		t.Errorf("expected 'steer already pending' error, got %v", err)
	}
}

func TestHandleFollowUpSlashErrors(t *testing.T) {
	app := &rpcApp{}

	if _, err := app.handleFollowUpSlash(""); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("empty follow-up should fail with usage error, got %v", err)
	}

	// Agent idle.
	if _, err := app.handleFollowUpSlash("hi"); err == nil || !strings.Contains(err.Error(), "not busy") {
		t.Errorf("idle agent should fail with 'not busy', got %v", err)
	}

	// Agent busy but follow-up mode disabled.
	app.isStreaming = true
	app.followUpMode = "off"
	if _, err := app.handleFollowUpSlash("hi"); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("disabled mode should fail with 'not enabled', got %v", err)
	}
}

func TestHandleAbortSlashNotStreaming(t *testing.T) {
	app := &rpcApp{}
	if _, err := app.handleAbortSlash(""); err == nil || !strings.Contains(err.Error(), "not streaming") {
		t.Errorf("expected 'not streaming' error, got %v", err)
	}
}

func TestHandleRewindUsage(t *testing.T) {
	app := &rpcApp{}
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
