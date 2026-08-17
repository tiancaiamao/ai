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

func TestCompactCheckPrompt(t *testing.T) {
	p := CompactCheckPrompt()
	if strings.TrimSpace(p) == "" {
		t.Error("CompactCheckPrompt should not be empty")
	}
			if !strings.Contains(p, "%s") {
		t.Error("CompactCheckPrompt must contain a budget-percentage placeholder")
	}
}
