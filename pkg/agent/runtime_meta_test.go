package agent

import (
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"testing"
)

func TestUpdateRuntimeMetaSnapshotRefreshRules(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	meta := agentctx.ContextMeta{
		TokensUsed:        12345,
		TokensMax:         128000,
		TokensPercent:     25.0,
		MessagesInHistory: 18,
		LLMContextSize:    3000,
	}

	snapshot, refreshed := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "")
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

	snapshot2, refreshed2 := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "")
	if refreshed2 {
		t.Fatal("did not expect refresh before heartbeat")
	}
	if snapshot2 != snapshot {
		t.Fatal("expected snapshot to stay stable before refresh")
	}

	_, refreshed3 := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "")
	if refreshed3 {
		t.Fatal("did not expect refresh on second non-heartbeat turn")
	}

	snapshot4, refreshed4 := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "")
	if !refreshed4 {
		t.Fatal("expected heartbeat refresh")
	}
	if snapshot4 == "" {
		t.Fatal("expected non-empty snapshot after heartbeat refresh")
	}

	meta.TokensPercent = 61.0
	snapshot5, refreshed5 := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "")
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
	if !containsString(appendix, "<llm_context>") {
		t.Fatalf("expected llm_context section, got: %s", appendix)
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
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("old request"),
		agentctx.NewAssistantMessage(),
		agentctx.NewUserMessage("new request"),
		agentctx.NewAssistantMessage(),
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "tool output"},
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
	msgs := []agentctx.AgentMessage{
		agentctx.NewAssistantMessage(),
		agentctx.NewToolResultMessage("call-1", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "tail"},
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
	msgs := []agentctx.AgentMessage{
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "orphaned tool result 1"},
		}, false),
		agentctx.NewToolResultMessage("call-2", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "orphaned tool result 2"},
		}, false),
		agentctx.NewUserMessage("user message"),
		agentctx.NewAssistantMessage(),
		agentctx.NewToolResultMessage("call-3", "grep", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "valid tool result"},
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

func TestBuildToolOutputsSummaryUsesStaleHistoryExcludingRecent10(t *testing.T) {
	msgs := []agentctx.AgentMessage{agentctx.NewUserMessage("old")}
	msgs = append(msgs, agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "stale-1"},
	}, false))
	msgs = append(msgs, agentctx.NewToolResultMessage("call-2", "bash", []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "stale-2"},
	}, false))
	for i := 3; i <= 12; i++ {
		msgs = append(msgs, agentctx.NewToolResultMessage(
			fmt.Sprintf("call-%d", i),
			"grep",
			[]agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("recent-%d", i)},
			},
			false,
		))
	}
	msgs = append(msgs, agentctx.NewUserMessage("latest"))

	summary := buildToolOutputsSummary(msgs)
	if !containsString(summary, "2 stale outputs") {
		t.Fatalf("expected 2 stale outputs in summary, got: %s", summary)
	}
	if !containsString(summary, "1 bash, 1 read") {
		t.Fatalf("expected grouped tool counts, got: %s", summary)
	}
	// Note: guidance is no longer included in buildToolOutputsSummary
	// LLM decides based on runtime_state telemetry
}

func TestUpdateRuntimeMetaSnapshotIncludesCompactDecisionSignals(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.Messages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("修复 truncate compact 协议"),
		func() agentctx.AgentMessage {
			m := agentctx.NewAssistantMessage()
			m.Content = []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "阶段完成：truncate 已完成"},
			}
			return m
		}(),
		agentctx.NewUserMessage("顺便看看前端动画设计"),
	}
	meta := agentctx.ContextMeta{
		TokensUsed:        64000,
		TokensMax:         128000,
		TokensPercent:     50.0,
		MessagesInHistory: len(agentCtx.Messages),
		LLMContextSize:    1200,
	}

	snapshot, refreshed := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "")
	if !refreshed {
		t.Fatal("expected refreshed snapshot")
	}
	if !containsString(snapshot, "compact_decision_signals:") {
		t.Fatalf("expected compact_decision_signals section, got: %s", snapshot)
	}
	if !containsString(snapshot, "topic_shift_since_last_user: llm_judge") {
		t.Fatalf("expected llm_judge topic shift signal, got: %s", snapshot)
	}
	if !containsString(snapshot, "phase_completed_recently: llm_judge") {
		t.Fatalf("expected llm_judge phase completion signal, got: %s", snapshot)
	}
	if !containsString(snapshot, "llm_judge_hint: Compare the latest user intent") {
		t.Fatalf("expected llm_judge_hint, got: %s", snapshot)
	}
}

func TestUpdateRuntimeMetaSnapshotRecordsReminderUsingCurrentTurn(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.ContextMgmtState = agentctx.DefaultContextMgmtState()
	agentCtx.ContextMgmtState.CurrentTurn = 7

	meta := agentctx.ContextMeta{
		TokensUsed:        90000,
		TokensMax:         128000,
		TokensPercent:     70.0,
		MessagesInHistory: 10,
		LLMContextSize:    1000,
	}

	_, refreshed := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "")
	if !refreshed {
		t.Fatal("expected refreshed snapshot")
	}
	// Note: LastReminderTurn is now set when reminder is actually shown (in streamAssistantResponse)
	// not in updateRuntimeMetaSnapshot which is telemetry-only
}

func TestRuntimeContextManagementHintByUsageStage(t *testing.T) {
	cases := []struct {
		percent float64
		expect  string
	}{
		{percent: 12, expect: "only TRUNCATE"},
		{percent: 25, expect: "TRUNCATE stale outputs in batches"},
		{percent: 32, expect: "consider COMPACT only after completing current task phase"},
		{percent: 60, expect: "prepare for COMPACT"},
		{percent: 70, expect: "COMPACT now"},
		{percent: 90, expect: "COMPACT immediately"},
	}

	for _, tc := range cases {
		got := runtimeContextManagementHint(tc.percent)
		if !containsString(got, tc.expect) {
			t.Fatalf("percent %.1f expected hint containing %q, got %q", tc.percent, tc.expect, got)
		}
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

func TestInsertBeforeLastUserMessage(t *testing.T) {
	cases := []struct {
		name     string
		messages []llm.LLMMessage
		insert   llm.LLMMessage
		wantLen  int
		check    func([]llm.LLMMessage) bool
	}{
		{
			name:     "empty messages",
			messages: nil,
			insert:   llm.LLMMessage{Role: "user", Content: "runtime"},
			wantLen:  1,
			check: func(msgs []llm.LLMMessage) bool {
				return len(msgs) == 1 && msgs[0].Content == "runtime"
			},
		},
		{
			name: "no user message - append to end",
			messages: []llm.LLMMessage{
				{Role: "assistant", Content: "hi"},
			},
			insert:  llm.LLMMessage{Role: "user", Content: "runtime"},
			wantLen: 2,
			check: func(msgs []llm.LLMMessage) bool {
				return msgs[1].Content == "runtime"
			},
		},
		{
			name: "single user message - insert before",
			messages: []llm.LLMMessage{
				{Role: "user", Content: "hello"},
			},
			insert:  llm.LLMMessage{Role: "user", Content: "runtime"},
			wantLen: 2,
			check: func(msgs []llm.LLMMessage) bool {
				return msgs[0].Content == "runtime" && msgs[1].Content == "hello"
			},
		},
		{
			name: "multiple messages - insert before last user",
			messages: []llm.LLMMessage{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "response"},
				{Role: "user", Content: "last"},
			},
			insert:  llm.LLMMessage{Role: "user", Content: "runtime"},
			wantLen: 4,
			check: func(msgs []llm.LLMMessage) bool {
				// runtime should be at index 2, before "last" at index 3
				return msgs[2].Content == "runtime" && msgs[3].Content == "last"
			},
		},
		{
			name: "tool results after user - insert before user",
			messages: []llm.LLMMessage{
				{Role: "user", Content: "request"},
				{Role: "assistant", Content: ""},
				{Role: "toolResult", Content: "output"},
				{Role: "user", Content: "follow-up"},
			},
			insert:  llm.LLMMessage{Role: "user", Content: "runtime"},
			wantLen: 5,
			check: func(msgs []llm.LLMMessage) bool {
				// runtime should be before "follow-up"
				return msgs[3].Content == "runtime" && msgs[4].Content == "follow-up"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := insertBeforeLastUserMessage(tc.messages, tc.insert)
			if len(result) != tc.wantLen {
				t.Fatalf("expected %d messages, got %d", tc.wantLen, len(result))
			}
			if !tc.check(result) {
				t.Fatalf("check failed for messages: %+v", result)
			}
		})
	}
}

func TestCollectStaleToolOutputStatsProtectsTaskTracking(t *testing.T) {
	// Create messages with multiple task_tracking calls
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("first request"),
		agentctx.NewToolResultMessage("call-1", "task_tracking", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "old update"},
		}, false),
		agentctx.NewToolResultMessage("call-2", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "file content"},
		}, false),
		agentctx.NewUserMessage("second request"),
		agentctx.NewToolResultMessage("call-3", "task_tracking", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "latest update"},
		}, false),
		agentctx.NewToolResultMessage("call-4", "bash", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "command output"},
		}, false),
	}

	// Use keepRecent=0 to test task_tracking protection specifically
	staleCount, byTool := collectStaleToolOutputStats(msgs, 0)

	// Should have 2 stale: 1 old task_tracking + 1 read (bash is after last user, latest task_tracking is protected)
	if staleCount != 2 {
		t.Fatalf("expected 2 stale outputs, got %d", staleCount)
	}

	// Check that task_tracking count is 1 (not 2)
	if byTool["task_tracking"] != 1 {
		t.Fatalf("expected 1 stale task_tracking, got %d", byTool["task_tracking"])
	}

	// Check that read is counted
	if byTool["read"] != 1 {
		t.Fatalf("expected 1 stale read, got %d", byTool["read"])
	}
}

func TestFindLatestToolCallID(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "first"},
		}, false),
		agentctx.NewToolResultMessage("call-2", "task_tracking", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "old"},
		}, false),
		agentctx.NewToolResultMessage("call-3", "task_tracking", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "latest"},
		}, false),
	}

	id := findLatestToolCallID(msgs, "task_tracking")
	if id != "call-3" {
		t.Fatalf("expected call-3, got %s", id)
	}

	// Test not found
	id = findLatestToolCallID(msgs, "nonexistent")
	if id != "" {
		t.Fatalf("expected empty string for nonexistent tool, got %s", id)
	}
}

func TestUpdateRuntimeMetaSnapshotIncludesContextMetrics(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.LLMContext = agentctx.NewLLMContext(t.TempDir())
	agentCtx.TaskTrackingState = agentctx.NewTaskTrackingState(t.TempDir())

	meta := agentctx.ContextMeta{
		TokensUsed:        12345,
		TokensMax:         128000,
		TokensPercent:     15.0,
		MessagesInHistory: 10,
		LLMContextSize:    500,
	}

	snapshot, _ := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "")

	// Should contain workspace section even when values are missing
	if !containsString(snapshot, "workspace:") {
		t.Fatalf("expected workspace section in snapshot:\n%s", snapshot)
	}
	if !containsString(snapshot, `current_workdir: "unknown"`) {
		t.Fatalf("expected unknown current_workdir in snapshot:\n%s", snapshot)
	}
	if !containsString(snapshot, `startup_path: "unknown"`) {
		t.Fatalf("expected unknown startup_path in snapshot:\n%s", snapshot)
	}

	// Should contain context_metrics section
	if !containsString(snapshot, "context_metrics:") {
		t.Fatalf("expected context_metrics section in snapshot:\n%s", snapshot)
	}

	// Should have update subsection with no_data initially
	if !containsString(snapshot, "update:") {
		t.Fatalf("expected update subsection:\n%s", snapshot)
	}
	if !containsString(snapshot, "total: 0") {
		t.Fatalf("expected total: 0 for no data yet:\n%s", snapshot)
	}

	// Should have decision subsection
	if !containsString(snapshot, "decision:") {
		t.Fatalf("expected decision subsection:\n%s", snapshot)
	}
}

func TestUpdateRuntimeMetaSnapshotIncludesWorkspacePaths(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	meta := agentctx.ContextMeta{
		TokensUsed:        1000,
		TokensMax:         128000,
		TokensPercent:     1.0,
		MessagesInHistory: 1,
		LLMContextSize:    10,
	}

	snapshot, _ := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "/repo/worktrees/feature-x", "/repo")
	if !containsString(snapshot, `current_workdir: "/repo/worktrees/feature-x"`) {
		t.Fatalf("expected current_workdir in snapshot:\n%s", snapshot)
	}
	if !containsString(snapshot, `startup_path: "/repo"`) {
		t.Fatalf("expected startup_path in snapshot:\n%s", snapshot)
	}
}
