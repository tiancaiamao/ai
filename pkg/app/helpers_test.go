package app

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
)

func TestHelpers_Wrappers(t *testing.T) {
	// formatIntOrUnknown
	if got := formatIntOrUnknown(42); got != "42" {
		t.Errorf("formatIntOrUnknown = %q", got)
	}
	if got := formatIntOrUnknown(0); got != "unknown" {
		t.Errorf("formatIntOrUnknown zero = %q", got)
	}

	// formatTokenLimit
	if got := formatTokenLimit(nil); got != "unknown" {
		t.Errorf("formatTokenLimit nil = %q", got)
	}
	if got := formatTokenLimit(&compact.CompactionState{TokenLimit: 50000}); got != "50000" {
		t.Errorf("formatTokenLimit = %q", got)
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

func TestSkillLoadWarnings(t *testing.T) {
	if got := skillLoadWarnings(nil); got != nil {
		t.Errorf("nil result should return nil warnings, got %v", got)
	}

	legacy := skill.Diagnostic{
		Type:    "warning",
		Code:    skill.DiagnosticCodeLegacyProjectSkills,
		Message: "legacy project skills directory .ai/skills is no longer loaded; move skills to .agents/skills",
		Path:    "/proj/.ai/skills",
	}
	other := skill.Diagnostic{Type: "warning", Message: "unknown frontmatter field \"tools\"", Path: "/x/SKILL.md"}

	if got := skillLoadWarnings(&skill.LoadResult{Diagnostics: []skill.Diagnostic{other}}); len(got) != 0 {
		t.Errorf("non-legacy warnings should be filtered out, got %v", got)
	}
	got := skillLoadWarnings(&skill.LoadResult{Diagnostics: []skill.Diagnostic{other, legacy}})
	if len(got) != 1 || got[0] != legacy.Message {
		t.Errorf("expected only the legacy warning, got %v", got)
	}
}

func TestBuildAgentContextPrefixIncludesSkillWarnings(t *testing.T) {
	app := &App{
		skillResult: &skill.LoadResult{Diagnostics: []skill.Diagnostic{{
			Type:    "warning",
			Code:    skill.DiagnosticCodeLegacyProjectSkills,
			Message: "legacy project skills directory .ai/skills is no longer loaded; move skills to .agents/skills",
		}}},
	}

	prefix := app.buildAgentContextPrefix()
	if !contains(prefix, "<agent:skills>") || !contains(prefix, "legacy project skills directory") {
		t.Fatalf("expected legacy warning in agent context prefix, got %q", prefix)
	}
}

func ptrString(s string) *string { return &s }
