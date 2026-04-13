package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestAppendCompactEvent_AppendsNotOverwrites(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir, nil)
	

	// Append 3 messages
	for i := 0; i < 3; i++ {
		_, err := sess.AppendMessage(agentctx.AgentMessage{
			Role: "user",
			Content: []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "hello"},
			},
		})
		if err != nil {
			t.Fatalf("AppendMessage %d: %v", i, err)
		}
	}

	// Verify: header + 3 messages = 4 lines
	lines := readTestJSONLLines(t, filepath.Join(dir, "messages.jsonl"))
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}

	// Append a compact event
	err := sess.AppendCompactEvent(&agentctx.CompactEventDetail{
		Action: agentctx.CompactActionTruncate,
		IDs:    []string{"call_abc", "call_def"},
	})
	if err != nil {
		t.Fatalf("AppendCompactEvent: %v", err)
	}

	// Verify: now 5 lines (appended, not rewritten)
	lines = readTestJSONLLines(t, filepath.Join(dir, "messages.jsonl"))
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}

	// Verify the compact event line content
	var entry map[string]interface{}
	if err := json.Unmarshal([]byte(lines[4]), &entry); err != nil {
		t.Fatalf("parse compact event: %v", err)
	}
	if entry["type"] != "compact_event" {
		t.Errorf("expected type=compact_event, got %v", entry["type"])
	}
	ce, ok := entry["compactEvent"].(map[string]interface{})
	if !ok {
		t.Fatal("compactEvent field missing or wrong type")
	}
	if ce["action"] != "truncate" {
		t.Errorf("expected action=truncate, got %v", ce["action"])
	}
}

func TestAppendCompactEvent_DoesNotModifyExistingEntries(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir, nil)
	

	// Append a message
	_, err := sess.AppendMessage(agentctx.AgentMessage{
		Role: "toolResult",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "original content that is long enough"},
		},
		ToolCallID: "call_abc",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// Snapshot the original line
	lines := readTestJSONLLines(t, filepath.Join(dir, "messages.jsonl"))
	original := lines[1]

	// Append compact event
	err = sess.AppendCompactEvent(&agentctx.CompactEventDetail{
		Action: agentctx.CompactActionTruncate,
		IDs:    []string{"call_abc"},
	})
	if err != nil {
		t.Fatalf("AppendCompactEvent: %v", err)
	}

	// Verify original line is unchanged
	lines = readTestJSONLLines(t, filepath.Join(dir, "messages.jsonl"))
	if lines[1] != original {
		t.Errorf("original message was modified!\nbefore: %s\nafter:  %s", original, lines[1])
	}
}

func TestGetMessages_AppliesCompactEvents(t *testing.T) {
	dir := t.TempDir()
	sess := NewSession(dir, nil)
	

	// Append a toolResult with long content
	longContent := "This is a very long tool output that should be truncated. " +
		"Lorem ipsum dolor sit amet, consectetur adipiscing elit. " +
		"Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua."
	_, err := sess.AppendMessage(agentctx.AgentMessage{
		Role: "toolResult",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: longContent},
		},
		ToolCallID: "call_abc",
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// Before compact: content is original
	msgs := sess.GetMessages()
	found := false
	for _, m := range msgs {
		if m.ToolCallID == "call_abc" {
			found = true
			if m.ExtractText() != longContent {
				t.Errorf("before compact: expected original content")
			}
		}
	}
	if !found {
		t.Fatal("message call_abc not found before compact")
	}

	// Append compact event
	err = sess.AppendCompactEvent(&agentctx.CompactEventDetail{
		Action: agentctx.CompactActionTruncate,
		IDs:    []string{"call_abc"},
	})
	if err != nil {
		t.Fatalf("AppendCompactEvent: %v", err)
	}

	// After compact: GetMessages replays events
	msgs = sess.GetMessages()
	found = false
	for _, m := range msgs {
		if m.ToolCallID == "call_abc" {
			found = true
			if m.ExtractText() == longContent {
				t.Errorf("after compact: content should be truncated")
			}
			if !m.Truncated {
				t.Error("after compact: should be marked Truncated")
			}
			if m.OriginalSize != len(longContent) {
				t.Errorf("OriginalSize: expected %d, got %d", len(longContent), m.OriginalSize)
			}
		}
	}
	if !found {
		t.Fatal("message call_abc not found after compact")
	}
}

func TestCompactEventJSON(t *testing.T) {
	detail := &agentctx.CompactEventDetail{
		Action: agentctx.CompactActionTruncate,
		IDs:    []string{"call_abc", "call_def", "call_xyz"},
	}
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed agentctx.CompactEventDetail
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Action != agentctx.CompactActionTruncate {
		t.Errorf("action mismatch: %v", parsed.Action)
	}
	if len(parsed.IDs) != 3 {
		t.Errorf("ids length: got %d", len(parsed.IDs))
	}
}

func readTestJSONLLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	rawLines := splitLines(data)
	var lines []string
	for _, line := range rawLines {
		s := string(line)
		if s != "" {
			lines = append(lines, s)
		}
	}
	return lines
}