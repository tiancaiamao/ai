package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

// TestResume_LoadsAgentStateFromCheckpoint verifies that resuming a session
// correctly restores AgentState from the latest checkpoint.
//
// In the Proposal B design:
//   - Messages are the sole responsibility of sess.GetMessages() (which
//     handles compaction snapshot refs internally).
//   - LoadResumeState returns fallbackMessages unchanged and only restores
//     AgentState from the latest checkpoint.
//
// Scenario:
//  1. Session writes 3 messages
//  2. SaveCheckpoint (snapshot of current AgentState)
//  3. Session writes 2 more messages
//  4. Resume: LoadResumeState with fallback = sess.GetMessages()
//
// Expected: LoadResumeState returns all 5 messages (from fallback) and
// AgentState with TotalTurns=7 from checkpoint.
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

	// Step 2: save checkpoint with AgentState.
	preAgentState := agentctx.NewAgentState("test-session", "/workspace")
	preAgentState.TotalTurns = 7
	snapshot := &agentctx.ContextSnapshot{
		RecentMessages: cloneMessages(sess.GetMessages()),
		AgentState:     preAgentState,
	}
	checkpointInfo, err := saveCheckpointAtJournalHead(sessionDir, snapshot, 1)
	if err != nil {
		t.Fatalf("saveCheckpointAtJournalHead: %v", err)
	}
	t.Logf("checkpoint saved: path=%s msgIndex=%d messages=%d",
		checkpointInfo.Path, checkpointInfo.MessageIndex, len(snapshot.RecentMessages))

	// Step 3: write 2 post-checkpoint messages.
	for i := 0; i < 2; i++ {
		msg := agentctx.NewUserMessage(fmt.Sprintf("post-checkpoint %d", i))
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("AppendMessage post %d: %v", i, err)
		}
	}

	// Step 4: simulate a fresh process resuming the session.
	fallback := sess.GetMessages()
	gotMessages, gotLLMCtx, gotState, err := LoadResumeState(sessionDir, fallback)
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

	// LLMContext: no longer restored from checkpoint.
	if gotLLMCtx != "" {
		t.Errorf("LLMContext = %q, want empty", gotLLMCtx)
	}

	// AgentState: restored from checkpoint.
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

	gotMessages, gotLLMCtx, gotState, err := LoadResumeState(sessionDir, fallback)
	if err != nil {
		t.Fatalf("LoadResumeState: %v", err)
	}

	if len(gotMessages) != len(fallback) {
		t.Errorf("message count = %d, want %d (fallback)", len(gotMessages), len(fallback))
	}
	if gotLLMCtx != "" {
		t.Errorf("LLMContext = %q, want empty (no checkpoint)", gotLLMCtx)
	}
	if gotState != nil {
		t.Errorf("AgentState = %v, want nil (no checkpoint)", gotState)
	}
}

// TestResume_FallsBackWhenSessionDirEmpty verifies the empty-sessionDir path.
func TestResume_FallsBackWhenSessionDirEmpty(t *testing.T) {
	fallback := []agentctx.AgentMessage{agentctx.NewUserMessage("hello")}

	gotMessages, _, _, err := LoadResumeState("", fallback)
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
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", sessionDir, err)
	}
	sess := session.NewSession(sessionDir)
	if _, err := sess.AppendSessionInfo("test", ""); err != nil {
		t.Fatalf("AppendSessionInfo: %v", err)
	}
	return sess
}

func saveCheckpointAtJournalHead(sessionDir string, snapshot *agentctx.ContextSnapshot, turn int) (*agentctx.CheckpointInfo, error) {
	journal, err := agentctx.OpenJournal(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	defer journal.Close()
	msgIdx := journal.GetLength()
	return agentctx.SaveCheckpoint(sessionDir, snapshot, turn, msgIdx)
}

func cloneMessages(in []agentctx.AgentMessage) []agentctx.AgentMessage {
	out := make([]agentctx.AgentMessage, len(in))
	copy(out, in)
	return out
}

func firstText(m agentctx.AgentMessage) string {
	for _, c := range m.Content {
		if tc, ok := c.(agentctx.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

var _ = filepath.Join
