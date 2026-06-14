package session

import (
	"strings"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// addDeltaCompactLocked appends a delta_compact entry to the session branch.
// The caller must NOT hold the session lock; this helper acquires it.
// fromID/toID are the entry IDs of the first/last messages in the compressed
// interval; summary is the LLM-generated summary text.
func addDeltaCompact(t *testing.T, sess *Session, fromID, toID, summary string) string {
	t.Helper()
	sess.mu.Lock()
	defer sess.mu.Unlock()
	entry := &SessionEntry{
		Type:      EntryTypeDeltaCompact,
		ID:        generateEntryID(sess.byID),
		ParentID:  sess.leafID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		FromID:    fromID,
		ToEntryID: toID,
		Summary:   summary,
	}
	sess.addEntry(entry)
	return entry.ID
}

func extractText(m agentctx.AgentMessage) string {
	return m.ExtractText()
}

// TestBuildSessionContext_DeltaCompact verifies a single delta_compact entry:
// the messages in [FromID, ToEntryID] are replaced by exactly one
// delta_summary message, while messages outside the interval are preserved.
func TestBuildSessionContext_DeltaCompact(t *testing.T) {
	sess := NewSession("")

	sess.AppendMessage(agentctx.NewUserMessage("m1"))
	id2, _ := sess.AppendMessage(agentctx.NewUserMessage("m2"))
	sess.AppendMessage(agentctx.NewUserMessage("m3"))
	id4, _ := sess.AppendMessage(agentctx.NewUserMessage("m4"))
	sess.AppendMessage(agentctx.NewUserMessage("m5"))

	deltaID := addDeltaCompact(t, sess, id2, id4, "summary of m2-m4")

	msgs := sess.GetMessages()

	// Expect: m1, delta_summary, m5  (m2,m3,m4 replaced)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(msgs), msgs)
	}
	if got := extractText(msgs[0]); got != "m1" {
		t.Errorf("msgs[0] = %q, want %q", got, "m1")
	}
	if msgs[1].Metadata == nil || msgs[1].Metadata.Kind != "delta_summary" {
		t.Errorf("msgs[1] metadata.kind = %v, want delta_summary", metadataKind(msgs[1]))
	}
	if extractText(msgs[1]) != "summary of m2-m4" {
		t.Errorf("msgs[1] text = %q, want summary", extractText(msgs[1]))
	}
	if msgs[1].EntryID != deltaID {
		t.Errorf("msgs[1].EntryID = %q, want %q", msgs[1].EntryID, deltaID)
	}
	if msgs[1].Role != "user" {
		t.Errorf("msgs[1].Role = %q, want user", msgs[1].Role)
	}
	if got := extractText(msgs[2]); got != "m5" {
		t.Errorf("msgs[2] = %q, want %q", got, "m5")
	}
}

// TestBuildSessionContext_MultipleDeltaCompact verifies multiple independent
// delta_compact entries: each interval is replaced by its own summary.
func TestBuildSessionContext_MultipleDeltaCompact(t *testing.T) {
	sess := NewSession("")

	id1, _ := sess.AppendMessage(agentctx.NewUserMessage("m1"))
	id2, _ := sess.AppendMessage(agentctx.NewUserMessage("m2"))
	sess.AppendMessage(agentctx.NewUserMessage("m3"))
	id4, _ := sess.AppendMessage(agentctx.NewUserMessage("m4"))
	id5, _ := sess.AppendMessage(agentctx.NewUserMessage("m5"))
	sess.AppendMessage(agentctx.NewUserMessage("m6"))

	d1 := addDeltaCompact(t, sess, id1, id2, "sum12")
	d2 := addDeltaCompact(t, sess, id4, id5, "sum45")

	msgs := sess.GetMessages()

	// Expect: delta(sum12) [m1,m2 replaced], m3, delta(sum45) [m4,m5 replaced], m6
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(msgs), msgs)
	}
	if metadataKind(msgs[0]) != "delta_summary" || extractText(msgs[0]) != "sum12" || msgs[0].EntryID != d1 {
		t.Errorf("msgs[0] = %+v", msgs[0])
	}
	if extractText(msgs[1]) != "m3" {
		t.Errorf("msgs[1] = %q, want m3", extractText(msgs[1]))
	}
	if metadataKind(msgs[2]) != "delta_summary" || extractText(msgs[2]) != "sum45" || msgs[2].EntryID != d2 {
		t.Errorf("msgs[2] = %+v", msgs[2])
	}
	if extractText(msgs[3]) != "m6" {
		t.Errorf("msgs[3] = %q, want m6", extractText(msgs[3]))
	}
}

// TestBuildSessionContext_OldAndNew verifies that an old-format
// EntryTypeCompaction and a new delta_compact can coexist: the old compaction
// acts as a global cut-point, and the delta interval applies to the surviving
// messages after it.
func TestBuildSessionContext_OldAndNew(t *testing.T) {
	sess := NewSession("")

	sess.AppendMessage(agentctx.NewUserMessage("m1"))
	sess.AppendMessage(agentctx.NewUserMessage("m2"))
	id3, _ := sess.AppendMessage(agentctx.NewUserMessage("m3"))
	sess.AppendMessage(agentctx.NewUserMessage("m4"))
	sess.AppendMessage(agentctx.NewUserMessage("m5"))

	// Insert an old-format compaction keeping from id3 onward.
	sess.mu.Lock()
	compactionEntry := &SessionEntry{
		Type:             EntryTypeCompaction,
		ID:               generateEntryID(sess.byID),
		ParentID:         sess.leafID,
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		FirstKeptEntryID: id3,
		Summary:          "old global summary",
	}
	sess.addEntry(compactionEntry)
	sess.mu.Unlock()

	// More messages after the compaction.
	id6, _ := sess.AppendMessage(agentctx.NewUserMessage("m6"))
	id7, _ := sess.AppendMessage(agentctx.NewUserMessage("m7"))
	sess.AppendMessage(agentctx.NewUserMessage("m8"))

	// Delta-compact m6 and m7.
	addDeltaCompact(t, sess, id6, id7, "delta of m6-m7")

	msgs := sess.GetMessages()

	// Expect: <old compaction summary>, m3, m4, m5, <delta summary>, m8
	// m1,m2 dropped by old cut-point; m6,m7 replaced by delta summary.
	if len(msgs) != 6 {
		t.Fatalf("expected 6 messages, got %d", len(msgs))
	}
	// First message is the old compaction summary (user role, wrapped text).
	if msgs[0].Role != "user" {
		t.Errorf("msgs[0].Role = %q, want user", msgs[0].Role)
	}
	if got := extractText(msgs[0]); got == "" || !strings.Contains(got, "old global summary") {
		t.Errorf("msgs[0] text = %q, want to contain old global summary", got)
	}
	if extractText(msgs[1]) != "m3" {
		t.Errorf("msgs[1] = %q, want m3", extractText(msgs[1]))
	}
	if extractText(msgs[2]) != "m4" {
		t.Errorf("msgs[2] = %q, want m4", extractText(msgs[2]))
	}
	if extractText(msgs[3]) != "m5" {
		t.Errorf("msgs[3] = %q, want m5", extractText(msgs[3]))
	}
	if metadataKind(msgs[4]) != "delta_summary" || extractText(msgs[4]) != "delta of m6-m7" {
		t.Errorf("msgs[4] = %+v, want delta_summary", msgs[4])
	}
	if extractText(msgs[5]) != "m8" {
		t.Errorf("msgs[5] = %q, want m8", extractText(msgs[5]))
	}
}

// TestBuildSessionContext_OldCompactionUnchanged ensures a session with only
// an old-format compaction (no delta_compact) replays exactly as before.
func TestBuildSessionContext_OldCompactionUnchanged(t *testing.T) {
	sess := NewSession("")

	sess.AppendMessage(agentctx.NewUserMessage("m1"))
	id2, _ := sess.AppendMessage(agentctx.NewUserMessage("m2"))
	sess.AppendMessage(agentctx.NewUserMessage("m3"))
	sess.AppendMessage(agentctx.NewUserMessage("m4"))

	sess.mu.Lock()
	compactionEntry := &SessionEntry{
		Type:             EntryTypeCompaction,
		ID:               generateEntryID(sess.byID),
		ParentID:         sess.leafID,
		Timestamp:        time.Now().UTC().Format(time.RFC3339Nano),
		FirstKeptEntryID: id2,
		Summary:          "global summary",
	}
	sess.addEntry(compactionEntry)
	sess.mu.Unlock()

	sess.AppendMessage(agentctx.NewUserMessage("m5"))

	msgs := sess.GetMessages()
	// Expect: summary, m2, m3, m4, m5 (m1 dropped, kept from m2)
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("msgs[0].Role = %q, want user (summary)", msgs[0].Role)
	}
	if extractText(msgs[1]) != "m2" {
		t.Errorf("msgs[1] = %q, want m2", extractText(msgs[1]))
	}
	if extractText(msgs[4]) != "m5" {
		t.Errorf("msgs[4] = %q, want m5", extractText(msgs[4]))
	}
}

// TestDeltaSummaryMessage checks the delta_summary message shape directly.
func TestDeltaSummaryMessage(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	msg := deltaSummaryMessage("hello summary", ts)

	if msg.Role != "user" {
		t.Errorf("Role = %q, want user", msg.Role)
	}
	if msg.Metadata == nil || msg.Metadata.Kind != "delta_summary" {
		t.Errorf("Metadata.Kind = %v, want delta_summary", metadataKind(msg))
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	tc, ok := msg.Content[0].(agentctx.TextContent)
	if !ok {
		t.Fatalf("content[0] type = %T, want TextContent", msg.Content[0])
	}
	if tc.Text != "hello summary" {
		t.Errorf("text = %q, want %q", tc.Text, "hello summary")
	}
	if tc.Type != "text" {
		t.Errorf("type = %q, want text", tc.Type)
	}
	if msg.Timestamp <= 0 {
		t.Errorf("Timestamp = %d, want > 0", msg.Timestamp)
	}
}

func metadataKind(m agentctx.AgentMessage) string {
	if m.Metadata == nil {
		return ""
	}
	return m.Metadata.Kind
}

// TestAppendDeltaCompact_PersistsEntry verifies that AppendDeltaCompact writes
// a delta_compact entry that is replayed correctly by buildSessionContext.
func TestAppendDeltaCompact_PersistsEntry(t *testing.T) {
	sess := NewSession("")

	sess.AppendMessage(agentctx.NewUserMessage("m1"))
	id2, _ := sess.AppendMessage(agentctx.NewUserMessage("m2"))
	sess.AppendMessage(agentctx.NewUserMessage("m3"))
	id4, _ := sess.AppendMessage(agentctx.NewUserMessage("m4"))
	sess.AppendMessage(agentctx.NewUserMessage("m5"))

	if err := sess.AppendDeltaCompact("summary of m2-m4", id2, id4); err != nil {
		t.Fatalf("AppendDeltaCompact failed: %v", err)
	}

	msgs := sess.GetMessages()
	// Expect: m1, delta_summary, m5 (m2,m3,m4 replaced).
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if extractText(msgs[0]) != "m1" {
		t.Errorf("msgs[0] = %q, want m1", extractText(msgs[0]))
	}
	if metadataKind(msgs[1]) != "delta_summary" || extractText(msgs[1]) != "summary of m2-m4" {
		t.Errorf("msgs[1] = %+v, want delta_summary", msgs[1])
	}
	if extractText(msgs[2]) != "m5" {
		t.Errorf("msgs[2] = %q, want m5", extractText(msgs[2]))
	}
}
