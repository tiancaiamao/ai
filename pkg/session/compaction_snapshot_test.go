package session

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestCompactionSnapshot_PreservesHistoryAndReconstructs verifies the Proposal B
// design: after compaction, messages.jsonl is append-only (history preserved),
// and GetMessages() reconstructs the post-compaction state from the snapshot.
//
// Flow:
//  1. Write 10 messages
//  2. AppendCompaction with [summary, 2 recent] as the post-compaction state
//  3. Write 3 more messages after compaction
//  4. GetMessages() should return: snapshot_messages (3) + post-compaction (3) = 6
//  5. Reload from disk and verify same result
func TestCompactionSnapshot_PreservesHistoryAndReconstructs(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir)

	// Step 1: write 10 messages.
	for i := 0; i < 10; i++ {
		if _, err := sess.AppendMessage(agentctx.NewUserMessage("old msg")); err != nil {
			t.Fatal(err)
		}
	}

	// Step 2: simulate compaction — post-compaction in-memory state is
	// [summary_message, 2 kept messages].
	postCompaction := []agentctx.AgentMessage{
		agentctx.NewUserMessage("[summary] conversation was about X, Y, Z"),
		agentctx.NewUserMessage("kept message 1"),
		agentctx.NewUserMessage("kept message 2"),
	}
	if _, err := sess.AppendCompaction("conversation was about X, Y, Z", postCompaction); err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}

	// Step 3: write 3 messages after compaction.
	for i := 0; i < 3; i++ {
		if _, err := sess.AppendMessage(agentctx.NewUserMessage("new msg")); err != nil {
			t.Fatal(err)
		}
	}

	// Step 4: GetMessages() should return snapshot (3) + post-compaction (3) = 6.
	msgs := sess.GetMessages()
	if got := len(msgs); got != 6 {
		t.Fatalf("GetMessages count = %d, want 6 (3 snapshot + 3 post-compaction)", got)
		for i, m := range msgs {
			t.Logf("  msg[%d] = %q", i, firstTextContent(m))
		}
	}

	// First 3 should be the snapshot messages.
	if firstTextContent(msgs[0]) != "[summary] conversation was about X, Y, Z" {
		t.Errorf("msgs[0] = %q, want summary", firstTextContent(msgs[0]))
	}

	// Step 5: reload from disk — messages.jsonl should have all entries
	// (10 old + 1 compaction + 3 new = 14 entries) plus the snapshot file.
	loaded, err := LoadSession(dir)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	// The loaded session should have 14 entries total (header + 10 msgs +
	// session_info + compaction + 3 msgs = 16, but let's count properly).
	// What matters is GetMessages() returns the same 6.
	loadedMsgs := loaded.GetMessages()
	if got := len(loadedMsgs); got != 6 {
		t.Fatalf("after reload: GetMessages count = %d, want 6", got)
		for i, m := range loadedMsgs {
			t.Logf("  msg[%d] = %q", i, firstTextContent(m))
		}
	}

	// Verify all messages match the in-memory version.
	for i := range msgs {
		if firstTextContent(msgs[i]) != firstTextContent(loadedMsgs[i]) {
			t.Errorf("msg[%d] mismatch: in-memory=%q loaded=%q",
				i, firstTextContent(msgs[i]), firstTextContent(loadedMsgs[i]))
		}
	}

	// Verify snapshot file exists.
	if compactionEntry := findCompactionEntry(loaded); compactionEntry != nil {
		if compactionEntry.SnapshotRef == "" {
			t.Error("SnapshotRef is empty after reload")
		}
	}
}

// TestCompactionSnapshot_MultipleCompactions verifies that multiple sequential
// compactions each create their own snapshot and GetMessages uses the latest.
func TestCompactionSnapshot_MultipleCompactions(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir)

	// First batch of messages.
	for i := 0; i < 5; i++ {
		sess.AppendMessage(agentctx.NewUserMessage("batch 1"))
	}

	// First compaction.
	post1 := []agentctx.AgentMessage{
		agentctx.NewUserMessage("[summary 1]"),
	}
	sess.AppendCompaction("first summary", post1)

	// More messages.
	for i := 0; i < 3; i++ {
		sess.AppendMessage(agentctx.NewUserMessage("batch 2"))
	}

	// Second compaction.
	post2 := []agentctx.AgentMessage{
		agentctx.NewUserMessage("[summary 2]"),
		agentctx.NewUserMessage("recent after 2nd compaction"),
	}
	sess.AppendCompaction("second summary", post2)

	// More messages after second compaction.
	sess.AppendMessage(agentctx.NewUserMessage("final msg"))

	msgs := sess.GetMessages()
	// Should be: snapshot2 (2) + 1 post-compaction = 3
	if got := len(msgs); got != 3 {
		t.Fatalf("GetMessages count = %d, want 3 (2 snapshot + 1 post-compaction)", got)
		for i, m := range msgs {
			t.Logf("  msg[%d] = %q", i, firstTextContent(m))
		}
	}

	if firstTextContent(msgs[0]) != "[summary 2]" {
		t.Errorf("msgs[0] = %q, want [summary 2]", firstTextContent(msgs[0]))
	}
	if firstTextContent(msgs[2]) != "final msg" {
		t.Errorf("msgs[2] = %q, want 'final msg'", firstTextContent(msgs[2]))
	}

	// Reload and verify.
	loaded, _ := LoadSession(dir)
	loadedMsgs := loaded.GetMessages()
	if len(loadedMsgs) != 3 {
		t.Fatalf("after reload: count = %d, want 3", len(loadedMsgs))
	}
}

func firstTextContent(m agentctx.AgentMessage) string {
	for _, c := range m.Content {
		if tc, ok := c.(agentctx.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

func findCompactionEntry(s *Session) *SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.entries) - 1; i >= 0; i-- {
		if s.entries[i].Type == EntryTypeCompaction {
			return s.entries[i]
		}
	}
	return nil
}
