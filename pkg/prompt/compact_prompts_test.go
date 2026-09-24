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

func TestCompactHintSkillReloadIsConditional(t *testing.T) {
	p := CompactHint()
	if !strings.Contains(p, "Check the summary first") {
		t.Error("CompactHint should direct the agent to check the summary before reloading a skill")
	}
	if !strings.Contains(p, "only when you need details that are not preserved there") {
		t.Error("CompactHint should make skill reload conditional on missing details")
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
