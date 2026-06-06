package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

// TestResume_ReplaysPostCheckpointMessages verifies that resuming a session
// after a checkpoint correctly returns ALL messages including those written
// after the checkpoint was created.
//
// This test mirrors the production bug reported in session 8c0ef142:
// after restarting and resuming, 25 journal entries written between the last
// checkpoint and the resume were lost, because the resume path loaded only
// checkpoint.RecentMessages and ignored journal entries appended after the
// checkpoint (commit / push / PR creation flow was dropped).
//
// Scenario:
//  1. Session writes 3 pre-checkpoint messages
//  2. SaveCheckpoint (snapshot of current state)
//  3. Session writes 2 more messages (post-checkpoint, not yet checkpointed)
//  4. Resume: load state from sessionDir via LoadResumeState
//
// Expected: LoadResumeState returns 5 messages (3 pre + 2 post).
// Bug (pre-fix): only 3 messages returned (drops 2 post-checkpoint entries).
func TestResume_ReplaysPostCheckpointMessages(t *testing.T) {
	sessionDir := t.TempDir()

	// Use the same sessionDir layout as production.
	sess := newTestSession(t, sessionDir)

	// Step 1: write 3 pre-checkpoint user messages.
	for i := 0; i < 3; i++ {
		msg := agentctx.NewUserMessage(fmt.Sprintf("pre-checkpoint %d", i))
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("AppendMessage pre %d: %v", i, err)
		}
	}

		// Step 2: snapshot current state and save as a checkpoint.
	// Use the production SaveCheckpoint path: recent messages come from the
	// session, and MessageIndex must reflect the journal length AT THIS POINT
	// so that replay can skip entries already represented in the snapshot.
	preMessages := cloneMessages(sess.GetMessages())
	preAgentState := agentctx.NewAgentState("test-session", "/workspace")
	preAgentState.TotalTurns = 7 // simulate prior work in the session
	snapshot := &agentctx.ContextSnapshot{
		LLMContext:     "# Current Task\npre-checkpoint work",
		RecentMessages: preMessages,
		AgentState:     preAgentState,
	}
	checkpointInfo, err := saveCheckpointAtJournalHead(sessionDir, snapshot, 1)
	if err != nil {
		t.Fatalf("saveCheckpointAtJournalHead: %v", err)
	}
	t.Logf("checkpoint saved: path=%s msgIndex=%d messages=%d",
		checkpointInfo.Path, checkpointInfo.MessageIndex, len(preMessages))

	// Step 3: write 2 post-checkpoint messages — these are the entries that
	// were lost in the production bug.
	for i := 0; i < 2; i++ {
		msg := agentctx.NewUserMessage(fmt.Sprintf("post-checkpoint %d", i))
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("AppendMessage post %d: %v", i, err)
		}
	}

	// Step 4: simulate a fresh process resuming the session — load state
	// purely from disk (sessionDir + journal + checkpoint).
	gotMessages, gotLLMCtx, gotState, err := LoadResumeState(sessionDir, nil)
	if err != nil {
		t.Fatalf("LoadResumeState: %v", err)
	}

	// Expected: 3 pre + 2 post = 5 messages.
	if got := len(gotMessages); got != 5 {
		t.Errorf("message count = %d, want 5 (3 pre-checkpoint + 2 post-checkpoint)", got)
		for i, m := range gotMessages {
			t.Logf("  msg[%d] role=%s text=%q", i, m.Role, firstText(m))
		}
	}

	// Also check that both pre and post messages are present.
	if gotLLMCtx != "# Current Task\npre-checkpoint work" {
		t.Errorf("LLMContext = %q, want checkpoint's LLMContext", gotLLMCtx)
	}
		if gotState == nil {
		t.Error("AgentState = nil, want non-nil")
	} else if gotState.TotalTurns != 7 {
		t.Errorf("TotalTurns = %d, want 7 (preserved from checkpoint)", gotState.TotalTurns)
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
// LoadResumeState should return the fallback messages as-is.
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

// TestResume_FallsBackWhenSessionDirEmpty verifies the empty-sessionDir path:
// LoadResumeState("", fallback) should return fallback messages.
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

// newTestSession creates a Session rooted at sessionDir, mimicking the layout
// used in production (~/.ai/sessions/<id>/).
func newTestSession(t *testing.T, sessionDir string) *session.Session {
	t.Helper()
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", sessionDir, err)
	}
	sess := session.NewSession(sessionDir)
	// SessionInfo entry mirrors what production writes shortly after init.
	if _, err := sess.AppendSessionInfo("test", ""); err != nil {
		t.Fatalf("AppendSessionInfo: %v", err)
	}
	return sess
}

// saveCheckpointAtJournalHead saves a checkpoint with MessageIndex equal to
// the current journal length. This mirrors the CORRECT behavior that should
// happen in production (but currently doesn't — see TestCreateSnapshot_PersistsJournalLength).
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

// Ensure filepath import is used when sessionDir is constructed via filepath.Join
var _ = filepath.Join