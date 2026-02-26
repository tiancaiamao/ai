package agent

import "testing"

func TestUpdateRuntimeMetaSnapshotRefreshRules(t *testing.T) {
	agentCtx := NewAgentContext("sys")
	meta := ContextMeta{
		TokensUsed:        12345,
		TokensMax:         128000,
		TokensPercent:     25.0,
		MessagesInHistory: 18,
		WorkingMemorySize: 3000,
	}

	snapshot, refreshed := updateRuntimeMetaSnapshot(agentCtx, meta, 3)
	if !refreshed {
		t.Fatal("expected initial snapshot refresh")
	}
	if snapshot == "" {
		t.Fatal("expected non-empty snapshot")
	}
	if !containsString(snapshot, "tokens_band: 20-40") {
		t.Fatalf("expected 20-40 band, got: %s", snapshot)
	}
	if !containsString(snapshot, "action_hint: light_compression") {
		t.Fatalf("expected light_compression hint, got: %s", snapshot)
	}

	snapshot2, refreshed2 := updateRuntimeMetaSnapshot(agentCtx, meta, 3)
	if refreshed2 {
		t.Fatal("did not expect refresh before heartbeat")
	}
	if snapshot2 != snapshot {
		t.Fatal("expected snapshot to stay stable before refresh")
	}

	_, refreshed3 := updateRuntimeMetaSnapshot(agentCtx, meta, 3)
	if refreshed3 {
		t.Fatal("did not expect refresh on second non-heartbeat turn")
	}

	snapshot4, refreshed4 := updateRuntimeMetaSnapshot(agentCtx, meta, 3)
	if !refreshed4 {
		t.Fatal("expected heartbeat refresh")
	}
	if snapshot4 == "" {
		t.Fatal("expected non-empty snapshot after heartbeat refresh")
	}

	meta.TokensPercent = 61.0
	snapshot5, refreshed5 := updateRuntimeMetaSnapshot(agentCtx, meta, 3)
	if !refreshed5 {
		t.Fatal("expected refresh on band change")
	}
	if !containsString(snapshot5, "tokens_band: 60-75") {
		t.Fatalf("expected 60-75 band, got: %s", snapshot5)
	}
	if !containsString(snapshot5, "action_hint: heavy_compression") {
		t.Fatalf("expected heavy_compression hint, got: %s", snapshot5)
	}
}

func TestBuildRuntimeSystemAppendix(t *testing.T) {
	appendix := buildRuntimeSystemAppendix("# wm", "<runtime_state>ok</runtime_state>")
	if !containsString(appendix, "<working_memory>") {
		t.Fatalf("expected working_memory section, got: %s", appendix)
	}
	if !containsString(appendix, "<runtime_state>ok</runtime_state>") {
		t.Fatalf("expected runtime_state section, got: %s", appendix)
	}
	if !containsString(appendix, "runtime_state is telemetry") {
		t.Fatalf("expected telemetry reminder, got: %s", appendix)
	}

	empty := buildRuntimeSystemAppendix("", "")
	if empty != "" {
		t.Fatalf("expected empty appendix, got: %s", empty)
	}
}

func TestExtractActiveTurnMessages(t *testing.T) {
	msgs := []AgentMessage{
		NewUserMessage("old request"),
		NewAssistantMessage(),
		NewUserMessage("new request"),
		NewAssistantMessage(),
		NewToolResultMessage("call-1", "read", []ContentBlock{
			TextContent{Type: "text", Text: "tool output"},
		}, false),
	}

	active := extractActiveTurnMessages(msgs, 20000)
	if len(active) != 3 {
		t.Fatalf("expected 3 active messages, got %d", len(active))
	}
	if got := active[0].ExtractText(); got != "new request" {
		t.Fatalf("expected active window to start from latest user message, got %q", got)
	}
	if active[2].Role != "toolResult" {
		t.Fatalf("expected tool result to be preserved in active window, got %q", active[2].Role)
	}
}

func TestExtractActiveTurnMessagesNoUserFallsBackToTail(t *testing.T) {
	msgs := []AgentMessage{
		NewAssistantMessage(),
		NewToolResultMessage("call-1", "bash", []ContentBlock{
			TextContent{Type: "text", Text: "tail"},
		}, false),
	}

	active := extractActiveTurnMessages(msgs, 20000)
	// When there's no user message and the result would start with toolResult,
	// we should return empty since toolResult cannot start a valid message sequence.
	if len(active) != 0 {
		t.Fatalf("expected empty result (toolResult cannot start sequence), got %d messages", len(active))
	}
}

func TestExtractRecentMessagesSkipsOrphanedToolResults(t *testing.T) {
	// Simulate a message sequence where truncation would leave orphaned toolResults at the start
	msgs := []AgentMessage{
		NewToolResultMessage("call-1", "read", []ContentBlock{
			TextContent{Type: "text", Text: "orphaned tool result 1"},
		}, false),
		NewToolResultMessage("call-2", "bash", []ContentBlock{
			TextContent{Type: "text", Text: "orphaned tool result 2"},
		}, false),
		NewUserMessage("user message"),
		NewAssistantMessage(),
		NewToolResultMessage("call-3", "grep", []ContentBlock{
			TextContent{Type: "text", Text: "valid tool result"},
		}, false),
	}

	// Use a small token budget to force truncation that would include orphaned toolResults
	active := extractRecentMessages(msgs, 100)
	if len(active) == 0 {
		t.Fatalf("expected some messages, got empty")
	}
	// First message should NOT be a toolResult
	if active[0].Role == "toolResult" || active[0].Role == "tool" {
		t.Fatalf("expected first message to not be toolResult/tool, got %q", active[0].Role)
	}
	// Should start with user or assistant
	if active[0].Role != "user" && active[0].Role != "assistant" {
		t.Fatalf("expected first message to be user or assistant, got %q", active[0].Role)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
