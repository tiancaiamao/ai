package session

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestCanCompactTrueThenFalseAfterCompaction(t *testing.T) {
	sess := NewSession("", nil)
	comp := compact.NewCompactor(&compact.Config{
		AutoCompact:      true,
		KeepRecent:       2,
		KeepRecentTokens: 0,
		MaxMessages:      1,
		MaxTokens:        0,
	}, llm.Model{}, "", "sys", 0)

	for i := 0; i < 8; i++ {
		if i%2 == 0 {
			if _, err := sess.AppendMessage(agentctx.NewUserMessage("user")); err != nil {
				t.Fatalf("append user message: %v", err)
			}
		} else {
			msg := agentctx.NewAssistantMessage()
			msg.Content = []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "assistant"},
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
	sess := NewSession("", nil)
	comp := compact.NewCompactor(&compact.Config{
		AutoCompact:      true,
		KeepRecent:       2,
		KeepRecentTokens: 0,
		MaxMessages:      1,
		MaxTokens:        0,
	}, llm.Model{}, "", "sys", 0)

	for i := 0; i < 4; i++ {
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "assistant only"},
		}
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("append assistant message: %v", err)
		}
	}

	if sess.CanCompact(comp) {
		t.Fatal("expected session to be non-compactable without cuttable user boundary")
	}
}

func TestCanCompactWithCuttableAheadOfBoundary(t *testing.T) {
	sess := NewSession("", nil)
	comp := compact.NewCompactor(&compact.Config{
		AutoCompact:      true,
		KeepRecent:       5,
		KeepRecentTokens: 0,
		MaxMessages:      1,
		MaxTokens:        0,
	}, llm.Model{}, "", "sys", 0)

	appendAssistant := func(text string) {
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: text},
		}
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatalf("append assistant message: %v", err)
		}
	}

	for i := 0; i < 10; i++ {
		appendAssistant("assistant prefix")
	}
	if _, err := sess.AppendMessage(agentctx.NewUserMessage("latest user boundary")); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	for i := 0; i < 3; i++ {
		appendAssistant("assistant suffix")
	}

	if !sess.CanCompact(comp) {
		t.Fatal("expected session to be compactable with forward cuttable boundary")
	}

	refs := buildMessageRefs("", sess.GetBranch(""))
	cutIdx := findFirstKeptIndex(refs, comp)
	if cutIdx <= 0 {
		t.Fatalf("expected forward cuttable boundary, got index=%d", cutIdx)
	}
	if refs[cutIdx].Message.Role != "user" {
		t.Fatalf("expected cut index to land on user message, got role=%q", refs[cutIdx].Message.Role)
	}
}
