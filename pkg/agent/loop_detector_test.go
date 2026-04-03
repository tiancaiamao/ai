package agent

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// helper: create an assistant message with tool calls
func makeAssistantWithToolCalls(toolNames ...string) *agentctx.AgentMessage {
	calls := make([]agentctx.ContentBlock, len(toolNames))
	for i, name := range toolNames {
		calls[i] = agentctx.ToolCallContent{
			ID:   "call_" + name,
			Name: name,
		}
	}
	return &agentctx.AgentMessage{
		Role:    "assistant",
		Content: calls,
	}
}

// helper: create a text-only assistant message (no tool calls)
func makeTextAssistant() *agentctx.AgentMessage {
	return &agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "Hello!"},
		},
	}
}

func TestLoopDetector_SameToolRepeated(t *testing.T) {
	ld := newLoopDetector(3) // low threshold for testing

	// Call "bash" 3 times → should trigger
	for i := 0; i < 3; i++ {
		_, err := ld.check(makeAssistantWithToolCalls("bash"))
		if i < 2 && err != nil {
			t.Fatalf("unexpected error on call %d: %v", i+1, err)
		}
	}
	_, err := ld.check(makeAssistantWithToolCalls("bash"))
	if err == nil {
		t.Fatal("expected loop detection error after 3 consecutive calls")
	}
}

func TestLoopDetector_DifferentToolsResets(t *testing.T) {
	ld := newLoopDetector(3)

	// Call "bash" twice
	ld.check(makeAssistantWithToolCalls("bash"))
	ld.check(makeAssistantWithToolCalls("bash"))

	// Call "read" once — should reset bash counter
	_, err := ld.check(makeAssistantWithToolCalls("read"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// bash counter should be gone now; calling bash 2 more times should be fine
	ld.check(makeAssistantWithToolCalls("bash"))
	_, err = ld.check(makeAssistantWithToolCalls("bash"))
	if err != nil {
		t.Fatalf("bash counter was not reset: %v", err)
	}
}

func TestLoopDetector_TextResponseResets(t *testing.T) {
	ld := newLoopDetector(3)

	// Call "bash" twice
	ld.check(makeAssistantWithToolCalls("bash"))
	ld.check(makeAssistantWithToolCalls("bash"))

	// Text response resets
	_, err := ld.check(makeTextAssistant())
	if err != nil {
		t.Fatalf("unexpected error on text response: %v", err)
	}

	// bash counter should be gone now
	ld.check(makeAssistantWithToolCalls("bash"))
	_, err = ld.check(makeAssistantWithToolCalls("bash"))
	if err != nil {
		t.Fatalf("bash counter was not reset after text response: %v", err)
	}
}

func TestLoopDetector_OscillationDetection(t *testing.T) {
	ld := newLoopDetector(7) // default threshold

	// Oscillate between bash and git for 4 rounds
	// oscillationCount threshold = 7/2 = 3
	for i := 0; i < 3; i++ {
		_, err := ld.check(makeAssistantWithToolCalls("bash", "git"))
		if err != nil {
			t.Fatalf("unexpected error on oscillation round %d: %v", i+1, err)
		}
	}

	// 4th oscillation should trigger
	_, err := ld.check(makeAssistantWithToolCalls("bash", "git"))
	if err == nil {
		t.Fatal("expected oscillation detection error")
	}
}

func TestLoopDetector_OscillationBreaksOnDifferentSet(t *testing.T) {
	ld := newLoopDetector(7)

	// Oscillate between bash and git for 2 rounds
	ld.check(makeAssistantWithToolCalls("bash", "git"))
	ld.check(makeAssistantWithToolCalls("bash", "git"))

	// Different set → resets oscillation
	_, err := ld.check(makeAssistantWithToolCalls("bash", "read"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be able to oscillate bash+git for 2 more rounds
	ld.check(makeAssistantWithToolCalls("bash", "git"))
	_, err = ld.check(makeAssistantWithToolCalls("bash", "git"))
	if err != nil {
		t.Fatalf("oscillation was not reset: %v", err)
	}
}

func TestLoopDetector_Reset(t *testing.T) {
	ld := newLoopDetector(3)

	// Call "bash" twice
	ld.check(makeAssistantWithToolCalls("bash"))
	ld.check(makeAssistantWithToolCalls("bash"))

	// Reset
	ld.reset()

	// bash counter should be gone; calling 2 more times should be fine
	ld.check(makeAssistantWithToolCalls("bash"))
	_, err := ld.check(makeAssistantWithToolCalls("bash"))
	if err != nil {
		t.Fatalf("reset did not clear counters: %v", err)
	}
}

func TestLoopDetector_PersistsAcrossCheckCalls(t *testing.T) {
	ld := newLoopDetector(5)

	// Simulate the bug scenario: context management interrupts,
	// but the detector persists because it's on AgentNew, not local.
	for i := 0; i < 4; i++ {
		_, err := ld.check(makeAssistantWithToolCalls("git_worktree_list"))
		if err != nil {
			t.Fatalf("unexpected error on call %d: %v", i+1, err)
		}
	}

	// 5th call should trigger
	_, err := ld.check(makeAssistantWithToolCalls("git_worktree_list"))
	if err == nil {
		t.Fatal("expected loop detection on 5th consecutive call")
	}
}

func TestBuildToolCallSignature(t *testing.T) {
	tc := agentctx.ToolCallContent{
		ID:   "call_123",
		Name: "bash",
		Arguments: map[string]any{
			"command": "ls",
		},
	}
	sig := buildToolCallSignature(tc)
	if sig != `bash:{"command":"ls"}` {
		t.Fatalf("unexpected signature: %s", sig)
	}

	// Nil arguments
	tc2 := agentctx.ToolCallContent{
		ID:   "call_456",
		Name: "read",
	}
	sig2 := buildToolCallSignature(tc2)
	if sig2 != "read:{}" {
		t.Fatalf("unexpected signature for nil args: %s", sig2)
	}
}
