package prompt

import (
	"strings"
	"testing"
)

func TestCompactSummarizePrompt(t *testing.T) {
	p := CompactSummarizePrompt()
	if strings.TrimSpace(p) == "" {
		t.Error("CompactSummarizePrompt should not be empty")
	}
	// Both calls return the same embedded template.
	if p != CompactSummarizePrompt() {
		t.Error("CompactSummarizePrompt should be deterministic")
	}
}

func TestCompactSummarizePromptRequiresExecutableSkillRules(t *testing.T) {
	p := CompactSummarizePrompt()
	for _, want := range []string{
		"the exact rules it needs are written in this summary",
		"including exact commands, flags, tool-call syntax, and file paths",
		"do not replace exact syntax with abstract advice",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("CompactSummarizePrompt should contain %q", want)
		}
	}
	if strings.Contains(p, "The agent MUST reload these skills") {
		t.Error("CompactSummarizePrompt should not demand unconditional skill reloads")
	}
}

func TestCompactHintSkillReloadIsConditional(t *testing.T) {
	p := CompactHint()
	for _, want := range []string{
		"Check the summary first",
		"only when you need details that are not preserved there",
		"a paraphrase, general knowledge, or recollection does NOT count",
		"If exact rules are missing, reload before using the skill",
		"do not reload skills whose needed rules are fully captured",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("CompactHint should contain %q", want)
		}
	}
}

func TestCompactCheckPrompt(t *testing.T) {
	p := CompactCheckPrompt()
	if strings.TrimSpace(p) == "" {
		t.Error("CompactCheckPrompt should not be empty")
	}
	if !strings.Contains(p, "%s") {
		t.Error("CompactCheckPrompt must contain a budget-percentage placeholder")
	}
}
