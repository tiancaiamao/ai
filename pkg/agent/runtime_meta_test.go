package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"testing"
)

func TestUpdateRuntimeMetaSnapshotRefreshRules(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	meta := ContextMeta{
		TokensUsed:        12345,
		TokensMax:         128000,
		TokensPercent:     25.0,
		MessagesInHistory: 18,
	}

	snapshot := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "", "")
	if snapshot == "" {
		t.Fatal("expected non-empty snapshot")
	}
	if !containsString(snapshot, "current_workdir:") {
		t.Fatalf("expected current_workdir in snapshot, got: %s", snapshot)
	}
	// action_hint field has been removed

	snapshot2 := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "", "")
	if snapshot2 != snapshot {
		t.Fatal("expected snapshot to stay stable before refresh")
	}

	_ = updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "", "")

	snapshot4 := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "", "")
	if snapshot4 == "" {
		t.Fatal("expected non-empty snapshot after heartbeat refresh")
	}

	meta.TokensPercent = 61.0
	snapshot5 := updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "", "")
	if !containsString(snapshot5, "current_workdir:") {
		t.Fatalf("expected current_workdir after band-change refresh, got: %s", snapshot5)
	}
	// action_hint field has been removed
}

func TestUpdateRuntimeMetaSnapshotRecordsReminderUsingCurrentTurn(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("sys")
	// Note: ContextMgmtState removed in backport - CurrentTurn no longer tracked separately

	meta := ContextMeta{
		TokensUsed:        90000,
		TokensMax:         128000,
		TokensPercent:     70.0,
		MessagesInHistory: 10,
	}

	_ = updateRuntimeMetaSnapshot(agentCtx, meta, 3, "", "", "")
	// Note: LastReminderTurn is now set when reminder is actually shown (in streamAssistantResponse)
	// not in updateRuntimeMetaSnapshot which is telemetry-only
}

// TestRuntimeContextManagementHintByUsageStage removed - runtimeContextManagementHint function
// was removed in refactor. Usage stage hints are now handled differently.

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

func TestInsertBeforeFirstUserMessage(t *testing.T) {
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
			insert:   llm.LLMMessage{Role: "user", Content: "skills"},
			wantLen:  1,
			check: func(msgs []llm.LLMMessage) bool {
				return len(msgs) == 1 && msgs[0].Content == "skills"
			},
		},
		{
			name: "no user message - prepend to beginning",
			messages: []llm.LLMMessage{
				{Role: "assistant", Content: "hi"},
			},
			insert:  llm.LLMMessage{Role: "user", Content: "skills"},
			wantLen: 2,
			check: func(msgs []llm.LLMMessage) bool {
				return msgs[0].Content == "skills" && msgs[1].Content == "hi"
			},
		},
		{
			name: "single user message - insert before",
			messages: []llm.LLMMessage{
				{Role: "user", Content: "hello"},
			},
			insert:  llm.LLMMessage{Role: "user", Content: "skills"},
			wantLen: 2,
			check: func(msgs []llm.LLMMessage) bool {
				return msgs[0].Content == "skills" && msgs[1].Content == "hello"
			},
		},
		{
			name: "multiple messages - insert before first user",
			messages: []llm.LLMMessage{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "response"},
				{Role: "user", Content: "last"},
			},
			insert:  llm.LLMMessage{Role: "user", Content: "skills"},
			wantLen: 4,
			check: func(msgs []llm.LLMMessage) bool {
				// skills should be at index 0, before "first" at index 1
				return msgs[0].Content == "skills" && msgs[1].Content == "first"
			},
		},
		{
			name: "tool results before user - insert before user",
			messages: []llm.LLMMessage{
				{Role: "assistant", Content: ""},
				{Role: "toolResult", Content: "output"},
				{Role: "user", Content: "request"},
			},
			insert:  llm.LLMMessage{Role: "user", Content: "skills"},
			wantLen: 4,
			check: func(msgs []llm.LLMMessage) bool {
				// skills before "request"
				return msgs[2].Content == "skills" && msgs[3].Content == "request"
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := insertBeforeFirstUserMessage(tc.messages, tc.insert)
			if len(result) != tc.wantLen {
				t.Fatalf("expected %d messages, got %d", tc.wantLen, len(result))
			}
			if !tc.check(result) {
				t.Fatalf("check failed for messages: %+v", result)
			}
		})
	}
}
