package prompt

import (
	"strings"
	"testing"
)

func TestBasePromptsAreDefined(t *testing.T) {
	if strings.TrimSpace(CompactorBasePrompt()) == "" {
		t.Fatal("CompactorBasePrompt should not be empty")
	}
	if strings.TrimSpace(RPCBasePrompt()) == "" {
		t.Fatal("RPCBasePrompt should not be empty")
	}
	if strings.TrimSpace(HeadlessBasePrompt(false)) == "" {
		t.Fatal("HeadlessBasePrompt(false) should not be empty")
	}
	if strings.TrimSpace(HeadlessBasePrompt(true)) == "" {
		t.Fatal("HeadlessBasePrompt(true) should not be empty")
	}
	if strings.TrimSpace(JSONModeBasePrompt()) == "" {
		t.Fatal("JSONModeBasePrompt should not be empty")
	}
}

func TestRPCBasePromptNoForcedJSON(t *testing.T) {
	p := RPCBasePrompt()
	if strings.Contains(strings.ToLower(p), "return an empty json with error field") {
		t.Fatalf("RPC base prompt still forces legacy JSON fallback: %q", p)
	}
}

func TestCompactPromptsAreDefined(t *testing.T) {
	if strings.TrimSpace(CompactSystemPrompt()) == "" {
		t.Fatal("CompactSystemPrompt should not be empty")
	}
	if strings.TrimSpace(CompactSummarizePrompt()) == "" {
		t.Fatal("CompactSummarizePrompt should not be empty")
	}
	if strings.TrimSpace(CompactUpdatePrompt()) == "" {
		t.Fatal("CompactUpdatePrompt should not be empty")
	}
}
