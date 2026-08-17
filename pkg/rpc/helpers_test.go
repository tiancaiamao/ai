package rpc

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/session"
)

func TestHelpers_Wrappers(t *testing.T) {
	// truncateText delegates to TruncateText
	if got := truncateText("hello world", 5); got != "he..." {
		t.Errorf("truncateText = %q, want %q", got, "he...")
	}
	if got := truncateText("hi", 10); got != "hi" {
		t.Errorf("truncateText no-trunc = %q, want %q", got, "hi")
	}

	// formatIntOrUnknown
	if got := formatIntOrUnknown(42); got != "42" {
		t.Errorf("formatIntOrUnknown = %q", got)
	}
	if got := formatIntOrUnknown(0); got != "unknown" {
		t.Errorf("formatIntOrUnknown zero = %q", got)
	}

	// formatLimit
	if got := formatLimit(100); got != "100" {
		t.Errorf("formatLimit = %q", got)
	}
	if got := formatLimit(-1); got != "disabled" {
		t.Errorf("formatLimit disabled = %q", got)
	}

	// formatTokenLimit
	if got := formatTokenLimit(nil); got != "unknown" {
		t.Errorf("formatTokenLimit nil = %q", got)
	}
	if got := formatTokenLimit(&compact.CompactionState{TokenLimit: 50000}); got != "50000" {
		t.Errorf("formatTokenLimit = %q", got)
	}

	// formatTokenLimitSource
	if got := formatTokenLimitSource("context_window"); got != "context-window" {
		t.Errorf("formatTokenLimitSource = %q", got)
	}
}

func TestBuildTreeEntries(t *testing.T) {
	entries := []session.SessionEntry{
		{Type: session.EntryTypeMessage, ID: "a"},
		{Type: session.EntryTypeMessage, ID: "b"},
	}
	leafID := ptrString("b")
	result := buildTreeEntries(entries, leafID)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0].EntryID != "a" {
		t.Errorf("first entry ID = %q", result[0].EntryID)
	}
	if result[1].EntryID != "b" {
		t.Errorf("second entry ID = %q", result[1].EntryID)
	}
}

func TestTreeEntryLabel(t *testing.T) {
	e := session.SessionEntry{Type: session.EntryTypeMessage}
	role, text := treeEntryLabel(e)
	// Should delegate to session.TreeEntryLabel
	if role == "" && text == "" {
		t.Errorf("treeEntryLabel returned empty for message entry")
	}
}

func ptrString(s string) *string { return &s }
