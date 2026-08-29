package rpc

// Tests for the implementation-specific `_`-prefixed session/update values the
// event translator emits for kernel events that the standard ACP v1 vocabulary
// does not cover (compaction, error, llm_retry, loop_guard, tool_call_recovery).
//
// These are ACP extensions (custom data lives in _meta). They intentionally do
// NOT conform to the official v1 schema, so they are asserted structurally here
// rather than validated in acp_schema_test.go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// emitExtEvents drives the translator with the 5 custom kernel events and
// returns the parsed session/update notifications in order.
func emitExtEvents(t *testing.T) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	srv := &acpServer{out: &buf, sessionID: "sess-ext"}

	srv.emit(agent.NewCompactionStartEvent(agent.CompactionInfo{
		Auto:    true,
		Before:  120,
		Trigger: "token_limit",
	}))
	srv.emit(agent.NewCompactionEndEvent(agent.CompactionInfo{
		Auto:   true,
		Before: 120,
		After:  45,
	}))
	srv.emit(agent.NewErrorEvent(errors.New("boom")))
	srv.emit(agent.NewLLMRetryEvent(agent.LLMRetryInfo{
		Attempt:    1,
		MaxRetries: 3,
		Delay:      time.Second,
		ErrorType:  "rate_limit",
	}))
	srv.emit(agent.NewLoopGuardTriggeredEvent(agent.LoopGuardInfo{Reason: "repeated tool call"}))
	srv.emit(agent.NewToolCallRecoveryEvent(agent.ToolCallRecoveryInfo{
		Reason:  "malformed args",
		Attempt: 1,
	}))

	lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n"))
	if len(lines) != 6 {
		t.Fatalf("expected 6 notifications, got %d: %s", len(lines), buf.String())
	}
	var out []map[string]any
	for i, line := range lines {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("notification %d not valid JSON: %v", i, err)
		}
		if m["method"] != "session/update" {
			t.Errorf("notification %d: expected method session/update, got %v", i, m["method"])
		}
		params, ok := m["params"].(map[string]any)
		if !ok {
			t.Fatalf("notification %d: expected params object, got %v", i, m)
		}
		if sid, _ := params["sessionId"].(string); sid != "sess-ext" {
			t.Errorf("notification %d: expected sessionId sess-ext, got %v", i, sid)
		}
		out = append(out, params["update"].(map[string]any))
	}
	return out
}

func TestACPCustomEventNotifications(t *testing.T) {
	upds := emitExtEvents(t)

	// sessionUpdate discriminator per notification.
	wantKind := []string{
		"_compaction", "_compaction", "_error",
		"_llm_retry", "_loop_guard", "_tool_call_recovery",
	}
	for i, want := range wantKind {
		if got, _ := upds[i]["sessionUpdate"].(string); got != want {
			t.Errorf("update %d: expected sessionUpdate %q, got %q", i, want, got)
		}
	}

	// compaction_start / compaction_end: status discriminator in _meta.
	if st, _ := upds[0]["_meta"].(map[string]any)["status"].(string); st != "start" {
		t.Errorf("compaction_start: expected _meta.status start, got %v", upds[0]["_meta"])
	}
	if st, _ := upds[1]["_meta"].(map[string]any)["status"].(string); st != "end" {
		t.Errorf("compaction_end: expected _meta.status end, got %v", upds[1]["_meta"])
	}
	// compaction_end carries the reduced token count.
	if after, _ := upds[1]["_meta"].(map[string]any)["info"].(map[string]any)["after"].(float64); after != 45 {
		t.Errorf("compaction_end: expected _meta.info.after 45, got %v", upds[1]["_meta"])
	}

	// _error carries the message text.
	if err, _ := upds[2]["_meta"].(map[string]any)["error"].(string); err != "boom" {
		t.Errorf("_error: expected _meta.error \"boom\", got %v", upds[2]["_meta"])
	}

	// _llm_retry carries the attempt counter.
	if att, _ := upds[3]["_meta"].(map[string]any)["attempt"].(float64); att != 1 {
		t.Errorf("_llm_retry: expected _meta.attempt 1, got %v", upds[3]["_meta"])
	}

	// _loop_guard carries the reason.
	if r, _ := upds[4]["_meta"].(map[string]any)["reason"].(string); r != "repeated tool call" {
		t.Errorf("_loop_guard: expected _meta.reason, got %v", upds[4]["_meta"])
	}

	// _tool_call_recovery carries reason + attempt.
	trMeta := upds[5]["_meta"].(map[string]any)
	if r, _ := trMeta["reason"].(string); r != "malformed args" {
		t.Errorf("_tool_call_recovery: expected _meta.reason, got %v", trMeta)
	}
	if a, _ := trMeta["attempt"].(float64); a != 1 {
		t.Errorf("_tool_call_recovery: expected _meta.attempt 1, got %v", trMeta)
	}
}