package agent

import (
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/session"
)

// ---------------------------------------------------------------------------
// NewCompactionController
// ---------------------------------------------------------------------------

func TestNewCompactionController(t *testing.T) {
	deps := CompactionDeps{}
	cc := NewCompactionController(deps)
	if cc == nil {
		t.Fatal("NewCompactionController should return non-nil")
	}
}

// ---------------------------------------------------------------------------
// MaybeCompact — nil compactor / nil session early returns
// ---------------------------------------------------------------------------

func TestMaybeCompact_NilCompactor(t *testing.T) {
	var captured []AgentEvent
	cc := NewCompactionController(CompactionDeps{
		Compactor: nil,
		Agent:     NewAgent(llm.Model{}, "key", "test"),
		EmitEvent: func(e AgentEvent) { captured = append(captured, e) },
		SetState:  func(bool) {},
	})

	// Should not panic or emit events
	cc.MaybeCompact("test", nil)
	if len(captured) != 0 {
		t.Errorf("expected 0 events with nil compactor, got %d", len(captured))
	}
}

func TestMaybeCompact_NilSession(t *testing.T) {
	var captured []AgentEvent
	cc := NewCompactionController(CompactionDeps{
		Compactor: nil,
		Agent:     NewAgent(llm.Model{}, "key", "test"),
		EmitEvent: func(e AgentEvent) { captured = append(captured, e) },
		SetState:  func(bool) {},
	})

	cc.MaybeCompact("test_trigger", nil)
	if len(captured) != 0 {
		t.Errorf("expected 0 events with nil session, got %d", len(captured))
	}
}

func TestMaybeCompact_BothNil(t *testing.T) {
	cc := NewCompactionController(CompactionDeps{})
	// Should not panic
	cc.MaybeCompact("test", nil)
}

// ---------------------------------------------------------------------------
// MaybeCompact — empty session with nil compactor
// ---------------------------------------------------------------------------

func TestMaybeCompact_EmptySessionNilCompactor(t *testing.T) {
	ag := NewAgent(llm.Model{}, "key", "test")
	var captured []AgentEvent
	var stateChanges []bool

	cc := NewCompactionController(CompactionDeps{
		Compactor: nil,
		Agent:     ag,
		EmitEvent: func(e AgentEvent) { captured = append(captured, e) },
		SetState:  func(b bool) { stateChanges = append(stateChanges, b) },
	})

	sess := createControllerTestSession(t)
	cc.MaybeCompact("pre_request", sess)
	if len(captured) != 0 {
		t.Errorf("expected 0 events, got %d", len(captured))
	}
	if len(stateChanges) != 0 {
		t.Errorf("expected 0 state changes, got %d", len(stateChanges))
	}
}

// ---------------------------------------------------------------------------
// MaybeCompact — nil agent is safe with nil compactor
// ---------------------------------------------------------------------------

func TestMaybeCompact_NilAgentSafeWithNilCompactor(t *testing.T) {
	cc := NewCompactionController(CompactionDeps{
		Agent: nil,
	})
	// nil compactor → early exit, nil Agent is never accessed
	cc.MaybeCompact("test", nil)
}

// ---------------------------------------------------------------------------
// RestoreContext
// ---------------------------------------------------------------------------

func TestRestoreContext_NoSummary(t *testing.T) {
	ag := NewAgent(llm.Model{}, "key", "test")
	cc := NewCompactionController(CompactionDeps{
		Agent: ag,
	})

	sess := createControllerTestSession(t)
	summary := sess.GetLastCompactionSummary()
	if summary != "" {
		t.Errorf("empty session summary = %q, want empty", summary)
	}
	// Should not panic
	cc.RestoreContext(sess)
}

func TestRestoreContext_EmptySessionDir(t *testing.T) {
	// Session with empty dir (in-memory) — RestoreContext should handle gracefully
	sess := session.NewSession("")
	ag := NewAgent(llm.Model{}, "key", "test")
	cc := NewCompactionController(CompactionDeps{
		Agent: ag,
	})
	// Should not panic — session has no compaction summary
	cc.RestoreContext(sess)
}

// ---------------------------------------------------------------------------
// CompactionInfo struct
// ---------------------------------------------------------------------------

func TestCompactionInfo_Fields(t *testing.T) {
	info := CompactionInfo{
		Auto:    true,
		Before:  10,
		After:   5,
		Trigger: "pre_request",
	}
	if !info.Auto {
		t.Error("Auto should be true")
	}
	if info.Before != 10 {
		t.Errorf("Before = %d, want 10", info.Before)
	}
	if info.Trigger != "pre_request" {
		t.Errorf("Trigger = %q, want %q", info.Trigger, "pre_request")
	}
}

// ---------------------------------------------------------------------------
// CompactionStartEvent / CompactionEndEvent
// ---------------------------------------------------------------------------

func TestCompactionStartEvent(t *testing.T) {
	info := CompactionInfo{Auto: true, Before: 8, Trigger: "test"}
	evt := NewCompactionStartEvent(info)
	if evt.Type != EventCompactionStart {
		t.Errorf("Type = %q, want %q", evt.Type, EventCompactionStart)
	}
	if evt.Compaction == nil {
		t.Fatal("Compaction should not be nil")
	}
	if !evt.Compaction.Auto {
		t.Error("Compaction.Auto should be true")
	}
}

func TestCompactionEndEvent(t *testing.T) {
	info := CompactionInfo{After: 4, Error: ""}
	evt := NewCompactionEndEvent(info)
	if evt.Type != EventCompactionEnd {
		t.Errorf("Type = %q, want %q", evt.Type, EventCompactionEnd)
	}
}

// ---------------------------------------------------------------------------
// Full flow: MaybeCompact with empty session triggers no compaction
// ---------------------------------------------------------------------------

func TestMaybeCompact_FullFlow_NoCompaction(t *testing.T) {
	ag := NewAgent(llm.Model{}, "key", "test")
	var events []AgentEvent
	var states []bool

	cc := NewCompactionController(CompactionDeps{
		Compactor: nil,
		Agent:     ag,
		EmitEvent: func(e AgentEvent) { events = append(events, e) },
		SetState:  func(b bool) { states = append(states, b) },
	})

	sess := createControllerTestSession(t)
	cc.MaybeCompact("pre_request_prompt", sess)

	// With nil compactor, ShouldCompact check is skipped entirely
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d: %v", len(events), eventTypesFromAgentEvents(events))
	}
	if len(states) != 0 {
		t.Errorf("expected 0 state changes, got %d", len(states))
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func createControllerTestSession(t *testing.T) *session.Session {
	t.Helper()
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "session")
	return session.NewSession(sessionDir)
}

func eventTypesFromAgentEvents(events []AgentEvent) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

// Verify interface at compile time
var _ agentctx.AgentMessage = agentctx.AgentMessage{}