package session

import (
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestCanCompactTrueThenFalseAfterCompaction(t *testing.T) {
	sess := NewSession("")
	comp := compact.NewCompactor(&compact.Config{
		AutoCompact:      true,
		KeepRecent:       2,
		KeepRecentTokens: 0,
		MaxMessages:      1,
		MaxTokens:        0,
	}, llm.Model{}, "", "sys", 0)

	for i := 0; i < 8; i++ {
		if i%2 == 0 {
			if _, err := sess.AppendMessage(agent.NewUserMessage("user")); err != nil {
				t.Fatalf("append user message: %v", err)
			}
		} else {
			msg := agent.NewAssistantMessage()
			msg.Content = []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: "assistant"},
			}
			if _, err := sess.AppendMessage(msg); err != nil {
				t.Fatalf("append assistant message: %v", err)
			}
		}
	}

	if !sess.CanCompact(comp) {
		t.Fatal("expected session to be compactable")
	}

	sess.mu.Lock()
	firstKeptID := ""
	if len(sess.entries) > 0 {
		firstKeptID = sess.entries[0].ID
	}
	entry := &SessionEntry{
		Type:             EntryTypeCompaction,
		ID:               generateEntryID(sess.byID),
		ParentID:         sess.leafID,
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		Summary:          "summary",
		FirstKeptEntryID: firstKeptID,
	}
	sess.addEntry(entry)
	sess.mu.Unlock()

	if sess.CanCompact(comp) {
		t.Fatal("expected session to be non-compactable immediately after compaction")
	}
}

func TestCanCompactFalseWithoutCuttableBoundary(t *testing.T) {
	sess := NewSession("")
	comp := compact.NewCompactor(&compact.Config{
		AutoCompact:      true,
		KeepRecent:       2,
		KeepRecentTokens: 0,
		MaxMessages:      1,
		MaxTokens:        0,
	}, llm.Model{}, "", "sys", 0)

	for i := 0; i < 4; i++ {
		msg := agent.NewAssistantMessage()
		msg.Content = []agent.ContentBlock{
			agent.TextContent{Type: "text", Text: "assistant only"},
		}
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("append assistant message: %v", err)
		}
	}

	if sess.CanCompact(comp) {
		t.Fatal("expected session to be non-compactable without cuttable user boundary")
	}
}
