package session

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestGetMessages_EntryID tests that buildSessionContext propagates
// session entry IDs into the AgentMessage.EntryID field.
func TestGetMessages_EntryID(t *testing.T) {
	sess := NewSession("")

	id1, err := sess.AppendMessage(agentctx.NewUserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	id2, err := sess.AppendMessage(agentctx.AgentMessage{
		Role:    "assistant",
		Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id3, err := sess.AppendMessage(agentctx.NewUserMessage("how are you"))
	if err != nil {
		t.Fatal(err)
	}

	msgs := sess.GetMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Each message should have its entry ID set
	if msgs[0].EntryID != id1 {
		t.Errorf("msgs[0].EntryID = %q, want %q", msgs[0].EntryID, id1)
	}
	if msgs[1].EntryID != id2 {
		t.Errorf("msgs[1].EntryID = %q, want %q", msgs[1].EntryID, id2)
	}
	if msgs[2].EntryID != id3 {
		t.Errorf("msgs[2].EntryID = %q, want %q", msgs[2].EntryID, id3)
	}

	// Roles should be correct
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" || msgs[2].Role != "user" {
		t.Errorf("roles: %q, %q, %q", msgs[0].Role, msgs[1].Role, msgs[2].Role)
	}
}

// TestGetMessages_EntryID_AfterRoundTrip tests that EntryID survives save/load.
func TestGetMessages_EntryID_AfterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir)

	id1, _ := sess.AppendMessage(agentctx.NewUserMessage("first"))
	sess.AppendMessage(agentctx.AgentMessage{
		Role:    "assistant",
		Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "reply"}},
	})
	id3, _ := sess.AppendMessage(agentctx.NewUserMessage("second"))

	// Reload from disk
	loaded, err := LoadSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	msgs := loaded.GetMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after reload, got %d", len(msgs))
	}

	// Entry IDs should still match
	if msgs[0].EntryID != id1 {
		t.Errorf("after reload: msgs[0].EntryID = %q, want %q", msgs[0].EntryID, id1)
	}
	if msgs[2].EntryID != id3 {
		t.Errorf("after reload: msgs[2].EntryID = %q, want %q", msgs[2].EntryID, id3)
	}
}
