package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

func fingerprint(model string, system string, msgs ...string) *agentctx.LLMRequestFingerprint {
	return fingerprintLLMRequest(model, llm.LLMContext{
		SystemPrompt: system,
		Messages:     userMessages(msgs...),
	})
}

func userMessages(contents ...string) []llm.LLMMessage {
	msgs := make([]llm.LLMMessage, 0, len(contents))
	for _, c := range contents {
		msgs = append(msgs, llm.LLMMessage{Role: "user", Content: c})
	}
	return msgs
}

func sameFingerprint(a, b *agentctx.LLMRequestFingerprint) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Model != b.Model || a.SystemHash != b.SystemHash || a.ToolsHash != b.ToolsHash || len(a.MsgHashes) != len(b.MsgHashes) {
		return false
	}
	for i := range a.MsgHashes {
		if a.MsgHashes[i] != b.MsgHashes[i] {
			return false
		}
	}
	return true
}

func TestFingerprintLLMRequest(t *testing.T) {
	base := fingerprint("m", "sys", "a", "b")

	// Identical requests produce identical fingerprints.
	if !sameFingerprint(base, fingerprint("m", "sys", "a", "b")) {
		t.Fatal("expected identical fingerprints for identical requests")
	}

	cases := []struct {
		name string
		got  *agentctx.LLMRequestFingerprint
	}{
		{"model", fingerprint("other", "sys", "a", "b")},
		{"system", fingerprint("m", "sys2", "a", "b")},
		{"message_content", fingerprint("m", "sys", "a", "changed")},
		{"message_count", fingerprint("m", "sys", "a", "b", "c")},
	}
	for _, tc := range cases {
		if sameFingerprint(base, tc.got) {
			t.Errorf("%s: expected fingerprint to change", tc.name)
		}
	}

	// Tools are part of the fingerprint.
	withTools := fingerprintLLMRequest("m", llm.LLMContext{
		SystemPrompt: "sys",
		Messages:     userMessages("a", "b"),
		Tools:        []llm.LLMTool{{Type: "function", Function: llm.ToolFunction{Name: "f"}}},
	})
	if sameFingerprint(base, withTools) {
		t.Error("tools: expected fingerprint to change")
	}
}

func TestCompareLLMFingerprints(t *testing.T) {
	prev := fingerprint("m", "sys", "a", "b", "c")

	tests := []struct {
		name         string
		prev         *agentctx.LLMRequestFingerprint
		curr         *agentctx.LLMRequestFingerprint
		pendingReset string
		wantReason   string
		wantDiverge  int
		wantHit      bool
	}{
		{
			name:       "cold start",
			curr:       prev,
			wantReason: prefixCacheColdStart, wantDiverge: -1,
		},
		{
			name:         "known reset after compaction",
			curr:         prev,
			pendingReset: "compaction:pre_llm_threshold",
			wantReason:   "compaction:pre_llm_threshold", wantDiverge: -1,
		},
		{
			name:       "pure append",
			prev:       prev,
			curr:       fingerprint("m", "sys", "a", "b", "c", "d"),
			wantReason: prefixCacheAppended, wantDiverge: -1, wantHit: true,
		},
		{
			name:       "identical request (retry)",
			prev:       prev,
			curr:       fingerprint("m", "sys", "a", "b", "c"),
			wantReason: prefixCacheAppended, wantDiverge: -1, wantHit: true,
		},
		{
			name:       "shrunk to strict prefix",
			prev:       prev,
			curr:       fingerprint("m", "sys", "a"),
			wantReason: prefixCacheTruncated, wantDiverge: -1, wantHit: true,
		},
		{
			name:       "mid-history divergence",
			prev:       prev,
			curr:       fingerprint("m", "sys", "a", "EDITED", "c", "d"),
			wantReason: prefixCacheDiverged, wantDiverge: 1,
		},
		{
			name:       "first message changed",
			prev:       prev,
			curr:       fingerprint("m", "sys", "EDITED", "b", "c"),
			wantReason: prefixCacheDiverged, wantDiverge: 0,
		},
		{
			name:       "model changed",
			prev:       prev,
			curr:       fingerprint("m2", "sys", "a", "b", "c"),
			wantReason: prefixCacheModelChanged, wantDiverge: -1,
		},
		{
			name:       "system prompt changed",
			prev:       prev,
			curr:       fingerprint("m", "sys2", "a", "b", "c"),
			wantReason: prefixCacheSystemChanged, wantDiverge: -1,
		},
		{
			name: "tools changed",
			prev: prev,
			curr: fingerprintLLMRequest("m", llm.LLMContext{
				SystemPrompt: "sys",
				Messages:     userMessages("a", "b", "c"),
				Tools:        []llm.LLMTool{{Type: "function", Function: llm.ToolFunction{Name: "f"}}},
			}),
			wantReason: prefixCacheToolsChanged, wantDiverge: -1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reason, diverge, hit := compareLLMFingerprints(tc.prev, tc.curr, tc.pendingReset)
			if reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
			if diverge != tc.wantDiverge {
				t.Errorf("divergeIndex = %d, want %d", diverge, tc.wantDiverge)
			}
			if hit != tc.wantHit {
				t.Errorf("hit = %v, want %v", hit, tc.wantHit)
			}
		})
	}
}

// withPrefixCacheTrace wires a trace buffer with default events enabled and
// returns a context plus a function retrieving the recorded
// llm_prefix_cache_check events.
func withPrefixCacheTrace(t *testing.T) (context.Context, func() []traceevent.TraceEvent) {
	t.Helper()

	originalEvents := traceevent.GetEnabledEvents()
	traceevent.ResetToDefaultEvents()
	t.Cleanup(func() {
		traceevent.DisableAllEvents()
		for _, name := range originalEvents {
			traceevent.EnableEvent(name)
		}
	})

	tb := traceevent.NewTraceBuf()
	tb.SetMaxEvents(1000)
	var mu sync.Mutex
	var recorded []traceevent.TraceEvent
	tb.AddSink(func(e traceevent.TraceEvent) {
		if e.Name != "llm_prefix_cache_check" {
			return
		}
		mu.Lock()
		recorded = append(recorded, e)
		mu.Unlock()
	})
	return traceevent.WithTraceBuf(context.Background(), tb), func() []traceevent.TraceEvent {
		mu.Lock()
		defer mu.Unlock()
		return recorded
	}
}

func traceFieldValue(t *testing.T, e traceevent.TraceEvent, key string) any {
	t.Helper()
	for _, f := range e.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	t.Fatalf("event %q has no field %q", e.Name, key)
	return nil
}

func TestCheckPrefixCache_TracesAndUpdatesState(t *testing.T) {
	ctx, events := withPrefixCacheTrace(t)

	agentCtx := agentctx.NewAgentContext("sys")
	span := traceevent.StartSpan(ctx, "llm_call", traceevent.CategoryLLM)
	defer span.End()

	// First request: cold start.
	checkPrefixCache(ctx, span, agentCtx, "m", llm.LLMContext{
		SystemPrompt: "sys",
		Messages:     userMessages("a"),
	})
	if agentCtx.AgentState.LastLLMRequest == nil {
		t.Fatal("expected fingerprint stored after first check")
	}
	if agentCtx.AgentState.PendingCacheResetReason != "" {
		t.Fatalf("expected pending reset cleared, got %q", agentCtx.AgentState.PendingCacheResetReason)
	}

	// Second request: pure append.
	checkPrefixCache(ctx, span, agentCtx, "m", llm.LLMContext{
		SystemPrompt: "sys",
		Messages:     userMessages("a", "b"),
	})

	// Third request: cleared fingerprint reports the pending reset reason.
	agentCtx.AgentState.ResetLLMRequestFingerprint("compaction:test")
	checkPrefixCache(ctx, span, agentCtx, "m", llm.LLMContext{
		SystemPrompt: "sys",
		Messages:     userMessages("compacted summary"),
	})
	if agentCtx.AgentState.LastLLMRequest == nil {
		t.Fatal("expected fingerprint stored after reset check")
	}

	recorded := events()
	if len(recorded) != 3 {
		t.Fatalf("expected 3 llm_prefix_cache_check events, got %d", len(recorded))
	}

	expectations := []struct {
		hit    bool
		reason string
	}{
		{hit: false, reason: prefixCacheColdStart},
		{hit: true, reason: prefixCacheAppended},
		{hit: false, reason: "compaction:test"},
	}
	for i, want := range expectations {
		e := recorded[i]
		if got := traceFieldValue(t, e, "hit").(bool); got != want.hit {
			t.Errorf("event %d: hit = %v, want %v", i, got, want.hit)
		}
		if got := traceFieldValue(t, e, "reason").(string); got != want.reason {
			t.Errorf("event %d: reason = %q, want %q", i, got, want.reason)
		}
	}
	if got := traceFieldValue(t, recorded[1], "prev_messages").(int); got != 1 {
		t.Errorf("event 1: prev_messages = %d, want 1", got)
	}
	if got := traceFieldValue(t, recorded[1], "curr_messages").(int); got != 2 {
		t.Errorf("event 1: curr_messages = %d, want 2", got)
	}
}

// TestStreamAssistantResponse_PrefixCacheCheckAcrossTurns verifies the wiring
// in streamAssistantResponse: the first request is a cold start, and the next
// one extends the previous prefix and reports a hit.
func TestStreamAssistantResponse_PrefixCacheCheckAcrossTurns(t *testing.T) {
	ctx, events := withPrefixCacheTrace(t)

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"reply %d\"}}]}\n\n", calls.Add(1))
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n")
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))

	config := &LoopConfig{
		Model: llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		},
		APIKey:        "test-key",
		ThinkingLevel: "high",
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(ctx, agentCtx, config, stream)
	if err != nil {
		t.Fatalf("first turn returned error: %v", err)
	}

	// Second turn: previous request messages are preserved, tail appended.
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, *msg, agentctx.NewUserMessage("again"))
	stream = newTestAgentEventStream()
	if _, err := streamAssistantResponse(ctx, agentCtx, config, stream); err != nil {
		t.Fatalf("second turn returned error: %v", err)
	}

	// Turn 1 sent [user] = 1 message; turn 2 sends the same prefix plus
	// [assistant, user] = 3 in total. streamAssistantResponse no longer
	// injects anything itself: runtime_state is frozen at the turn boundary
	// in RunLoop, so consecutive requests are pure tail appends.
	if got := len(agentCtx.AgentState.LastLLMRequest.MsgHashes); got != 3 {
		t.Fatalf("expected 3 message hashes after second turn, got %d", got)
	}

	recorded := events()
	if len(recorded) != 2 {
		t.Fatalf("expected 2 llm_prefix_cache_check events, got %d", len(recorded))
	}
	if reason := traceFieldValue(t, recorded[0], "reason").(string); reason != prefixCacheColdStart {
		t.Errorf("first event reason = %q, want %q", reason, prefixCacheColdStart)
	}

	// The second turn is a pure append of the first request: no ephemeral
	// re-injection shifts mid-history positions anymore, so the provider
	// prefix chain stays intact across turn boundaries.
	if hit := traceFieldValue(t, recorded[1], "hit").(bool); !hit {
		t.Errorf("second event hit = false, want true")
	}
	if reason := traceFieldValue(t, recorded[1], "reason").(string); reason != prefixCacheAppended {
		t.Errorf("second event reason = %q, want %q", reason, prefixCacheAppended)
	}
	if got := traceFieldValue(t, recorded[1], "prev_messages").(int); got != 1 {
		t.Errorf("second event prev_messages = %d, want 1", got)
	}
	if got := traceFieldValue(t, recorded[1], "curr_messages").(int); got != 3 {
		t.Errorf("second event curr_messages = %d, want 3", got)
	}
}
