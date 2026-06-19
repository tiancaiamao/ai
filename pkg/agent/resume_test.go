package agent

import (
	"fmt"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

// TestResume_LoadsAgentStateFromCheckpoint verifies that resuming a session
// correctly restores AgentState from agent_state.json.
//
// Messages are the sole responsibility of sess.GetMessages().
// LoadResumeState returns fallbackMessages unchanged and only restores
// AgentState from agent_state.json.
func TestResume_LoadsAgentStateFromCheckpoint(t *testing.T) {
	sessionDir := t.TempDir()

	sess := newTestSession(t, sessionDir)

	// Step 1: write 3 messages.
	for i := 0; i < 3; i++ {
		msg := agentctx.NewUserMessage(fmt.Sprintf("pre-checkpoint %d", i))
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("AppendMessage pre %d: %v", i, err)
		}
	}

	// Step 2: save AgentState directly.
	preAgentState := agentctx.NewAgentState("test-session", "/workspace")
	preAgentState.TotalTurns = 7
	if err := agentctx.SaveAgentState(sessionDir, preAgentState); err != nil {
		t.Fatalf("SaveAgentState: %v", err)
	}

	// Step 3: write 2 post-checkpoint messages.
	for i := 0; i < 2; i++ {
		msg := agentctx.NewUserMessage(fmt.Sprintf("post-checkpoint %d", i))
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("AppendMessage post %d: %v", i, err)
		}
	}

	// Step 4: simulate a fresh process resuming the session.
	fallback := sess.GetMessages()
	gotMessages, gotState, err := LoadResumeState(sessionDir, fallback)
	if err != nil {
		t.Fatalf("LoadResumeState: %v", err)
	}

	// Messages: returned unchanged from fallback (session is source of truth).
	if got := len(gotMessages); got != 5 {
		t.Errorf("message count = %d, want 5", got)
		for i, m := range gotMessages {
			t.Logf("  msg[%d] role=%s text=%q", i, m.Role, firstText(m))
		}
	}

	// AgentState: restored from agent_state.json.
	if gotState == nil {
		t.Error("AgentState = nil, want non-nil")
	} else if gotState.TotalTurns != 7 {
		t.Errorf("TotalTurns = %d, want 7", gotState.TotalTurns)
	}

	// Ensure post-checkpoint messages survived.
	if len(gotMessages) >= 5 {
		lastMsg := firstText(gotMessages[4])
		if lastMsg != "post-checkpoint 1" {
			t.Errorf("last message text = %q, want %q", lastMsg, "post-checkpoint 1")
		}
	}
}

// TestResume_FallsBackWhenNoCheckpoint verifies the no-checkpoint path:
// LoadResumeState should return the fallback messages as-is with nil AgentState.
func TestResume_FallsBackWhenNoCheckpoint(t *testing.T) {
	sessionDir := t.TempDir()

	fallback := []agentctx.AgentMessage{
		agentctx.NewUserMessage("hello"),
		agentctx.NewAssistantMessage(),
	}

	gotMessages, gotState, err := LoadResumeState(sessionDir, fallback)
	if err != nil {
		t.Fatalf("LoadResumeState: %v", err)
	}

	if len(gotMessages) != len(fallback) {
		t.Errorf("message count = %d, want %d (fallback)", len(gotMessages), len(fallback))
	}
	if gotState != nil {
		t.Errorf("AgentState = %v, want nil (no checkpoint)", gotState)
	}
}

// TestResume_FallsBackWhenSessionDirEmpty verifies the empty-sessionDir path.
func TestResume_FallsBackWhenSessionDirEmpty(t *testing.T) {
	fallback := []agentctx.AgentMessage{agentctx.NewUserMessage("hello")}

	gotMessages, _, err := LoadResumeState("", fallback)
	if err != nil {
		t.Fatalf("LoadResumeState: %v", err)
	}
	if len(gotMessages) != 1 {
		t.Errorf("message count = %d, want 1", len(gotMessages))
	}
}

// --- helpers ---

func newTestSession(t *testing.T, sessionDir string) *session.Session {
	t.Helper()
	sess := session.NewSession(sessionDir)
	if _, err := sess.AppendSessionInfo("test", ""); err != nil {
		t.Fatalf("AppendSessionInfo: %v", err)
	}
	return sess
}

func firstText(m agentctx.AgentMessage) string {
	for _, c := range m.Content {
		if tc, ok := c.(agentctx.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
