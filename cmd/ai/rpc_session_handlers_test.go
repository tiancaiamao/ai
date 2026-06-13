package main

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

// mockAgent implements messageIndexResolver for testing.
type mockAgent struct {
	messages []agentctx.AgentMessage
}

func (m *mockAgent) GetMessages() []agentctx.AgentMessage {
	return m.messages
}

// TestResolveMessageIndex_EntryIDFromMessages tests that resolveMessageIndex
// correctly maps a /messages index to a session entry ID when the agent's
// messages carry EntryID (set by buildSessionContext).
func TestResolveMessageIndex_EntryIDFromMessages(t *testing.T) {
	sess := session.NewSession("")
	// Build agent messages with EntryID set (simulating buildSessionContext output)
	messages := []agentctx.AgentMessage{
		{Role: "user", EntryID: "aaa", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hello"}}},
		{Role: "assistant", EntryID: "bbb", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hi"}}},
		{Role: "user", EntryID: "ccc", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "how are you"}}},
	}

	ag := &mockAgent{messages: messages}

	// Resolve index 0 → "aaa"
	if id, ok := resolveMessageIndex(ag, sess, "0"); !ok || id != "aaa" {
		t.Errorf("expected (aaa, true), got (%q, %v)", id, ok)
	}
	// Resolve index 2 → "ccc"
	if id, ok := resolveMessageIndex(ag, sess, "2"); !ok || id != "ccc" {
		t.Errorf("expected (ccc, true), got (%q, %v)", id, ok)
	}
	// Out of range
	if _, ok := resolveMessageIndex(ag, sess, "5"); ok {
		t.Error("expected false for out-of-range index")
	}
	// Non-numeric passthrough
	if _, ok := resolveMessageIndex(ag, sess, "someEntryId"); ok {
		t.Error("expected false for non-numeric arg")
	}
}

// TestResolveMessageIndex_TimestampFallback tests that resolveMessageIndex
// falls back to timestamp+role matching when agent messages don't have EntryID.
// This is the scenario that was previously broken: agent loads messages from
// checkpoint (no EntryID), but /messages shows indices based on those messages.
func TestResolveMessageIndex_TimestampFallback(t *testing.T) {
	sess := session.NewSession("")

	// Manually set unique timestamps to avoid same-millisecond collisions
	msg1 := agentctx.NewUserMessage("hello")
	msg1.Timestamp = 1000
	id1, _ := sess.AppendMessage(msg1)

	msg2 := agentctx.AgentMessage{
		Role:      "assistant",
		Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hi"}},
		Timestamp: 2000,
	}
	sess.AppendMessage(msg2)

	msg3 := agentctx.NewUserMessage("how are you")
	msg3.Timestamp = 3000
	id3, _ := sess.AppendMessage(msg3)

	// Get session messages — these have EntryID set
	sessMsgs := sess.GetMessages()

	// Build agent messages WITHOUT EntryID (simulating checkpoint resume)
	agentMsgs := make([]agentctx.AgentMessage, len(sessMsgs))
	for i, m := range sessMsgs {
		cp := m
		cp.EntryID = "" // Simulate checkpoint that doesn't preserve EntryID
		agentMsgs[i] = cp
	}

	ag := &mockAgent{messages: agentMsgs}

	// Resolve index 0 → should find id1 via timestamp fallback
	if id, ok := resolveMessageIndex(ag, sess, "0"); !ok || id != id1 {
		t.Errorf("expected (%q, true), got (%q, %v)", id1, id, ok)
	}
	// Resolve index 2 → should find id3 via timestamp fallback
	if id, ok := resolveMessageIndex(ag, sess, "2"); !ok || id != id3 {
		t.Errorf("expected (%q, true), got (%q, %v)", id3, id, ok)
	}
}

// TestResolveMessageIndex_CompactionIndexMismatch demonstrates the old bug:
// the old resolveMessageIndex used sess.GetBranch("") and counted entries,
// but /messages uses ag.GetMessages() — after compaction/resume these differ.
// With the fix, the function uses ag.GetMessages() so indices match.
func TestResolveMessageIndex_CompactionIndexMismatch(t *testing.T) {
	sess := session.NewSession("")

	// Add 10 messages (5 user + 5 assistant pairs) with unique timestamps
	ts := int64(1000)
	for i := 0; i < 5; i++ {
		msg := agentctx.NewUserMessage("msg")
		msg.Timestamp = ts
		ts += 100
		sess.AppendMessage(msg)

		reply := agentctx.AgentMessage{
			Role:      "assistant",
			Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "reply"}},
			Timestamp: ts,
		}
		ts += 100
		sess.AppendMessage(reply)
	}
	// Session branch has 10 messages
	branch := sess.GetBranch("")
	branchMsgCount := 0
	for _, e := range branch {
		if e.Type == session.EntryTypeMessage {
			branchMsgCount++
		}
	}
	if branchMsgCount != 10 {
		t.Fatalf("expected 10 branch messages, got %d", branchMsgCount)
	}

	// Simulate agent having only 4 messages (e.g. after resume from checkpoint)
	// without EntryID — same timestamps as the last 4 session messages
	allMsgs := sess.GetMessages()
	agentMsgs := make([]agentctx.AgentMessage, 4)
	copy(agentMsgs, allMsgs[6:]) // Last 4 messages
	for i := range agentMsgs {
		agentMsgs[i].EntryID = "" // No EntryID from checkpoint
	}

	ag := &mockAgent{messages: agentMsgs}

	// Index 3 in agent (4 messages: [0,1,2,3]) should resolve to the LAST
	// session entry via timestamp match — NOT branch entry #3.
	if id, ok := resolveMessageIndex(ag, sess, "3"); !ok {
		t.Error("expected to resolve index 3")
	} else if id == "" {
		t.Error("expected non-empty entry ID")
	}

	// Verify: the resolved entry should be branch[9] (last one), not branch[3]
	lastEntry := branch[len(branch)-1]
	if lastEntry.Type != session.EntryTypeMessage || lastEntry.Message == nil {
		t.Fatal("expected last branch entry to be a message")
	}
	if agentMsgs[3].Timestamp != lastEntry.Message.Timestamp {
		t.Errorf("timestamp mismatch: agent=%d, branch_last=%d",
			agentMsgs[3].Timestamp, lastEntry.Message.Timestamp)
	}
	// The old code would have returned branch[3] for index 3,
	// which has a different timestamp.
	wrongEntry := branch[3]
	if wrongEntry.Message != nil && agentMsgs[3].Timestamp == wrongEntry.Message.Timestamp {
		t.Error("sanity check failed: branch[3] has same timestamp as agent[3] — test setup issue")
	}
}
