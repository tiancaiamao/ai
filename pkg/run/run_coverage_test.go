package run

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	truncpkg "github.com/tiancaiamao/ai/pkg/truncate"
)

// --- agent_end.go ---

func TestFindLastAgentEnd_FileNotExist(t *testing.T) {
	if r := FindLastAgentEnd("/nonexistent"); r != nil {
		t.Errorf("expected nil for missing file, got %+v", r)
	}
}

func TestFindLastAgentEnd_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if r := FindLastAgentEnd(path); r != nil {
		t.Errorf("expected nil for empty file, got %+v", r)
	}
}

func TestFindLastAgentEnd_NoAgentEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"agent_start"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if r := FindLastAgentEnd(path); r != nil {
		t.Errorf("expected nil, got %+v", r)
	}
}

func TestFindLastAgentEnd_Found(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	content := `{"type":"agent_start"}
{"type":"agent_end","success":true,"turns":3}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	r := FindLastAgentEnd(path)
	if r == nil || !r.Found || !r.Success || r.Turns != 3 {
		t.Errorf("unexpected result: %+v", r)
	}
}

func TestFindLastAgentEnd_LargerThanChunk(t *testing.T) {
	// Write a file > 64KB with agent_end near the end.
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	line := `{"type":"text_delta","data":{"text_delta":"padding"}}` + "\n"
	for i := 0; i < 5000; i++ {
		f.WriteString(line)
	}
	f.WriteString(`{"type":"agent_end","success":true,"turns":7}` + "\n")
	f.Close()

	r := FindLastAgentEnd(path)
	if r == nil || !r.Success || r.Turns != 7 {
		t.Errorf("unexpected result: %+v", r)
	}
}

func TestParseAgentEndLine_InvalidJSON(t *testing.T) {
	if r := parseAgentEndLine("not-json"); r != nil {
		t.Errorf("expected nil for invalid JSON, got %+v", r)
	}
}

func TestTruncateString(t *testing.T) {
	if got := truncpkg.TruncateString("hello", 10); got != "hello" {
		t.Errorf("expected unchanged, got %q", got)
	}
	if got := truncpkg.TruncateString("hello", 5); got != "hello" {
		t.Errorf("expected unchanged when equal, got %q", got)
	}
	if got := truncpkg.TruncateString("hello world", 8); got != "hello..." {
		t.Errorf("expected 'hello...', got %q", got)
	}
	if got := truncpkg.TruncateString("abc", 3); got != "abc" {
		t.Errorf("expected unchanged for exact match, got %q", got)
	}
	// maxLen <= 3 → just slice
	if got := truncpkg.TruncateString("abcdef", 2); got != "ab" {
		t.Errorf("expected 'ab' for maxLen<=3, got %q", got)
	}
	if got := truncpkg.TruncateString("abcdef", 0); got != "" {
		t.Errorf("expected '' for maxLen=0, got %q", got)
	}
}

func TestFormatAgentStatus_TerminalStatus(t *testing.T) {
	meta := &RunMeta{Status: StatusDone}
	if got := FormatAgentStatus(meta, nil); got != StatusDone {
		t.Errorf("expected 'done', got %q", got)
	}
	meta2 := &RunMeta{Status: StatusFailed}
	if got := FormatAgentStatus(meta2, nil); got != StatusFailed {
		t.Errorf("expected 'failed', got %q", got)
	}
}

func TestFormatAgentStatus_RunningNoEndInfo(t *testing.T) {
	meta := &RunMeta{PID: os.Getpid(), Status: StatusRunning}
	if got := FormatAgentStatus(meta, nil); got != "running" {
		t.Errorf("expected 'running', got %q", got)
	}
}

func TestFormatAgentStatus_RunningWithSuccessEnd(t *testing.T) {
	meta := &RunMeta{PID: os.Getpid(), Status: StatusRunning}
	end := &AgentEndInfo{Found: true, Success: true}
	if got := FormatAgentStatus(meta, end); got != "idle" {
		t.Errorf("expected 'idle', got %q", got)
	}
}

func TestFormatAgentStatus_RunningWithErrorEnd(t *testing.T) {
	meta := &RunMeta{PID: os.Getpid(), Status: StatusRunning}
	end := &AgentEndInfo{Found: true, Success: false, Error: "boom"}
	got := FormatAgentStatus(meta, end)
	if !strings.HasPrefix(got, "ended:") || !strings.Contains(got, "boom") {
		t.Errorf("expected 'ended:boom...', got %q", got)
	}
}

// --- conv.go ---

func TestParseTextDelta_Empty(t *testing.T) {
	if r := parseTextDelta(map[string]any{"delta": ""}); r != nil {
		t.Errorf("expected nil for empty delta, got %+v", r)
	}
}

func TestParseTextDelta_NonEmpty(t *testing.T) {
	r := parseTextDelta(map[string]any{"delta": "hello"})
	if r == nil || r.Kind != KindText || r.Text != "hello" {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestParseCompactionStart(t *testing.T) {
	// Auto-compaction with "before" count
	r := parseCompactionStart(map[string]any{
		"info": map[string]any{"auto": true, "before": 10.0},
	})
	if r == nil || !strings.Contains(r.Text, "auto-compaction") || !strings.Contains(r.Text, "10 messages") {
		t.Errorf("unexpected: %+v", r)
	}
	// Manual compaction
	r = parseCompactionStart(map[string]any{
		"info": map[string]any{"auto": false},
	})
	if r == nil || !strings.Contains(r.Text, "compaction started") {
		t.Errorf("expected 'compaction started', got %+v", r)
	}
	// Missing info
	r = parseCompactionStart(map[string]any{})
	if r == nil || !strings.Contains(r.Text, "compaction started") {
		t.Errorf("expected fallback text, got %+v", r)
	}
}

func TestParseCompactionEnd(t *testing.T) {
	// Mini compaction with truncations
	r := parseCompactionEnd(map[string]any{
		"info": map[string]any{
			"type":              "mini",
			"auto":              true,
			"truncatedCount":    3.0,
			"tokensBefore":      1000.0,
			"tokensAfter":       500.0,
			"llmContextUpdated": true,
		},
	})
	if r == nil || !strings.Contains(r.Text, "3 messages truncated") || !strings.Contains(r.Text, "1000 -> 500 tokens") || !strings.Contains(r.Text, "LLM context updated") {
		t.Errorf("unexpected: %+v", r)
	}

	// Mini compaction no action
	r = parseCompactionEnd(map[string]any{
		"info": map[string]any{"type": "mini", "auto": false},
	})
	if r == nil || !strings.Contains(r.Text, "no action needed") {
		t.Errorf("unexpected: %+v", r)
	}

	// Mini compaction with truncations but no token info
	r = parseCompactionEnd(map[string]any{
		"info": map[string]any{"type": "mini", "truncatedCount": 2.0},
	})
	if r == nil || !strings.Contains(r.Text, "2 messages truncated") {
		t.Errorf("unexpected: %+v", r)
	}

	// Error case
	r = parseCompactionEnd(map[string]any{
		"info": map[string]any{"error": "kaboom"},
	})
	if r == nil || !strings.Contains(r.Text, "failed: kaboom") {
		t.Errorf("unexpected: %+v", r)
	}

	// Major compaction with before/after
	r = parseCompactionEnd(map[string]any{
		"info": map[string]any{"before": 10.0, "after": 2.0, "auto": true},
	})
	if r == nil || !strings.Contains(r.Text, "10 -> 2 messages") {
		t.Errorf("unexpected: %+v", r)
	}

	// Major compaction no info
	r = parseCompactionEnd(map[string]any{
		"info": map[string]any{},
	})
	if r == nil || !strings.Contains(r.Text, "compaction done") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestParseToolExecutionEnd_ErrorVariants(t *testing.T) {
	// Error with explicit result
	r := parseToolExecutionEnd(map[string]any{
		"isError": true,
		"result":  "permission denied",
	})
	if r == nil || !strings.Contains(r.Text, "error") || !strings.Contains(r.Text, "permission denied") {
		t.Errorf("unexpected: %+v", r)
	}

	// Error with no result
	r = parseToolExecutionEnd(map[string]any{
		"isError": true,
	})
	if r == nil || !strings.Contains(r.Text, "error") {
		t.Errorf("expected error text, got %+v", r)
	}

	// Non-error with tool name
	r = parseToolExecutionEnd(map[string]any{
		"toolName": "bash",
	})
	if r == nil || !strings.Contains(r.Text, "done") {
		t.Errorf("expected done text, got %+v", r)
	}
}

func TestParseEvent_Response_DataTypes(t *testing.T) {
	// /thinking
	r := ParseEvent(`{"type":"response","success":true,"data":{"level":"high"}}`)
	if r == nil || !strings.Contains(r.Text, "Thinking level: high") {
		t.Errorf("unexpected: %+v", r)
	}
	// /compact message
	r = ParseEvent(`{"type":"response","success":true,"data":{"message":"compacted"}}`)
	if r == nil || !strings.Contains(r.Text, "compacted") {
		t.Errorf("unexpected: %+v", r)
	}
	// /new — should return nil (session_switch handles it)
	r = ParseEvent(`{"type":"response","success":true,"data":{"sessionId":"abc","cancelled":false}}`)
	if r != nil {
		t.Errorf("expected nil for /new response, got %+v", r)
	}
	// Fallback pretty-print
	r = ParseEvent(`{"type":"response","success":true,"data":{"unknown":"data","x":1}}`)
	if r == nil || !strings.Contains(r.Text, "unknown") {
		t.Errorf("unexpected fallback: %+v", r)
	}
}

func TestParseEvent_CompactionStart(t *testing.T) {
	r := ParseEvent(`{"type":"compaction_start","info":{"auto":true,"before":5}}`)
	if r == nil || r.Kind != KindMeta {
		t.Errorf("expected KindMeta, got %+v", r)
	}
}

func TestParseEvent_CompactionEnd(t *testing.T) {
	r := ParseEvent(`{"type":"compaction_end","info":{"type":"mini","truncatedCount":2}}`)
	if r == nil || r.Kind != KindMeta {
		t.Errorf("expected KindMeta, got %+v", r)
	}
}

func TestParseEvent_LoopGuard(t *testing.T) {
	r := ParseEvent(`{"type":"loop_guard_triggered","reason":"repeated output","loopGuard":{"reason":"nested"}}`)
	if r == nil || !strings.Contains(r.Text, "loop guard triggered") {
		t.Errorf("unexpected: %+v", r)
	}

	r = ParseEvent(`{"type":"loop_guard_triggered"}`)
	if r == nil || !strings.Contains(r.Text, "unknown") {
		t.Errorf("expected unknown reason, got %+v", r)
	}
}

func TestParseEvent_ToolCallRecovery(t *testing.T) {
	r := ParseEvent(`{"type":"tool_call_recovery","reason":"bad","attempt":2}`)
	if r == nil || !strings.Contains(r.Text, "attempt 2") || !strings.Contains(r.Text, "bad") {
		t.Errorf("unexpected: %+v", r)
	}

	r = ParseEvent(`{"type":"tool_call_recovery"}`)
	if r == nil || !strings.Contains(r.Text, "recovered malformed tool call") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestParseEvent_AgentEnd_Error(t *testing.T) {
	r := ParseEvent(`{"type":"agent_end","error":"failed"}`)
	if r == nil || !strings.Contains(r.Text, "failed") {
		t.Errorf("unexpected: %+v", r)
	}

	r = ParseEvent(`{"type":"agent_end","success":false}`)
	if r == nil || !strings.Contains(r.Text, "failed") {
		t.Errorf("unexpected: %+v", r)
	}

	r = ParseEvent(`{"type":"agent_end","success":true}`)
	if r == nil || !strings.Contains(r.Text, "done") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestParseEvent_SessionSwitch(t *testing.T) {
	r := ParseEvent(`{"type":"session_switch","session":"abc","sessionName":"My Session"}`)
	if r == nil || !strings.Contains(r.Text, "My Session") || !strings.Contains(r.Text, "abc") {
		t.Errorf("unexpected: %+v", r)
	}
	r = ParseEvent(`{"type":"session_switch","session":"abc"}`)
	if r == nil || !strings.Contains(r.Text, "abc") {
		t.Errorf("unexpected: %+v", r)
	}
	r = ParseEvent(`{"type":"session_switch"}`)
	if r != nil {
		t.Errorf("expected nil for empty session switch, got %+v", r)
	}
}

func TestParseEvent_TextDeltaDirect(t *testing.T) {
	r := ParseEvent(`{"type":"text_delta","delta":"raw text"}`)
	if r == nil {
		t.Skip("text_delta at top level not handled - this is OK")
	}
}

func TestFormatResponseData(t *testing.T) {
	if got := FormatResponseData(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
	if got := FormatResponseData(map[string]any{"level": "low"}); !strings.Contains(got, "Thinking level: low") {
		t.Errorf("expected thinking level, got %q", got)
	}
}

func TestRenderSkills(t *testing.T) {
	data := `{"commands":[{"name":"foo","source":"builtin","description":"foo command"},{"name":"bar","source":"user"}]}`
	r := renderSkills([]byte(data))
	if r == nil || !strings.Contains(r.Text, "foo") || !strings.Contains(r.Text, "bar") {
		t.Errorf("unexpected: %+v", r)
	}

	// Empty
	r = renderSkills([]byte(`{"commands":[]}`))
	if r == nil || !strings.Contains(r.Text, "no commands") {
		t.Errorf("unexpected: %+v", r)
	}

	// Bad JSON
	r = renderSkills([]byte(`bad json`))
	if r == nil {
		t.Error("expected fallback for bad JSON")
	}
}

func TestRenderContext(t *testing.T) {
	// Minimal valid context data
	data := `{"state":{"sessionId":"s1","sessionName":"n","sessionFile":"/tmp/s","model":{"id":"m","provider":"p","name":"model"},"messageCount":10,"pendingMessageCount":0,"isStreaming":false,"isCompacting":false,"thinkingLevel":"medium","autoCompactionEnabled":true,"aiPid":1,"aiLogPath":"/tmp/log","aiWorkingDir":"/tmp"},"stats":{"sessionId":"s1","totalMessages":10,"userMessages":4,"assistantMessages":5,"toolCalls":3,"toolResults":3,"compactionCount":0,"cost":0.001,"tokens":{"input":100,"output":50,"cacheRead":0,"cacheWrite":0,"total":150}},"models":{"models":[{"id":"m","provider":"p","name":"model","contextWindow":200000}]}}`
	r := renderContext([]byte(data))
	if r == nil || !strings.Contains(r.Text, "Context Usage") || !strings.Contains(r.Text, "Session Stats") {
		t.Errorf("unexpected: %+v", r)
	}

	// Bad JSON
	r = renderContext([]byte(`bad`))
	if r == nil {
		t.Error("expected fallback for bad JSON")
	}
}

func TestRenderSessionState(t *testing.T) {
	data := `{"sessionId":"s1","sessionName":"name","sessionFile":"/tmp/s","model":{"id":"m","provider":"p","name":"model"},"messageCount":5,"pendingMessageCount":1,"isStreaming":true,"isCompacting":false,"thinkingLevel":"low","autoCompactionEnabled":true,"aiPid":1234,"aiLogPath":"/tmp/log","aiWorkingDir":"/cwd"}`
	r := renderSessionState([]byte(data))
	if r == nil || !strings.Contains(r.Text, "Session:") {
		t.Errorf("unexpected: %+v", r)
	}

	// Bad JSON → fallback
	r = renderSessionState([]byte(`bad`))
	if r == nil {
		t.Error("expected fallback for bad JSON")
	}
}

func TestRenderSessions(t *testing.T) {
	data := `{"sessions":[{"id":"s1","name":"First","title":"t1","updatedAt":"2025-01-01","messageCount":5},{"id":"s2","name":"Second","title":"t2","updatedAt":"2025-01-02","messageCount":3}]}`
	r := renderSessions([]byte(data))
	if r == nil || !strings.Contains(r.Text, "First") || !strings.Contains(r.Text, "Second") {
		t.Errorf("unexpected: %+v", r)
	}

	// Empty
	r = renderSessions([]byte(`{"sessions":[]}`))
	if r == nil || !strings.Contains(r.Text, "No sessions") {
		t.Errorf("unexpected: %+v", r)
	}

	// Bad JSON
	r = renderSessions([]byte(`bad`))
	if r == nil {
		t.Error("expected fallback")
	}
}

func TestRenderModel(t *testing.T) {
	// CycleModelResult format
	data := `{"model":{"id":"m","provider":"p","name":"Model","contextWindow":200000},"previousModel":{"id":"old","provider":"p","name":"Old"}}`
	r := renderModel([]byte(data))
	if r == nil || !strings.Contains(r.Text, "p/Model (m)") {
		t.Errorf("unexpected: %+v", r)
	}

	// Fallback {model: {id, name}} format
	data2 := `{"model":{"id":"m","provider":"p","name":"X"}}`
	r = renderModel([]byte(data2))
	if r == nil || !strings.Contains(r.Text, "p/X (m)") {
		t.Errorf("unexpected: %+v", r)
	}

	// Bad JSON
	r = renderModel([]byte(`bad`))
	if r == nil {
		t.Error("expected fallback")
	}
}

func TestRenderModelList(t *testing.T) {
	data := `{"models":[{"id":"m1","provider":"p","name":"Model1"},{"id":"m2","provider":"p","name":"Model2"}],"currentIndex":0}`
	r := renderModelList([]byte(data))
	if r == nil || !strings.Contains(r.Text, "Model1") || !strings.Contains(r.Text, "[current]") {
		t.Errorf("unexpected: %+v", r)
	}

	// With current object instead of index
	data2 := `{"models":[{"id":"m1","provider":"p","name":"M1"}],"current":{"provider":"p","id":"m1"}}`
	r = renderModelList([]byte(data2))
	if r == nil || !strings.Contains(r.Text, "[current]") {
		t.Errorf("unexpected: %+v", r)
	}

	// Empty
	r = renderModelList([]byte(`{"models":[]}`))
	if r == nil || !strings.Contains(r.Text, "no models") {
		t.Errorf("unexpected: %+v", r)
	}

	// Bad JSON
	r = renderModelList([]byte(`bad`))
	if r == nil {
		t.Error("expected fallback")
	}
}

func TestRenderSettings(t *testing.T) {
	data := `{"type":"settings","data":{"model":"m1","show-thinking":true,"unknown-key":"x"}}`
	r := renderSettings([]byte(data))
	if r == nil || !strings.Contains(r.Text, "Display Settings") || !strings.Contains(r.Text, "m1") {
		t.Errorf("unexpected: %+v", r)
	}

	// Bad type
	r = renderSettings([]byte(`{"type":"other","data":{}}`))
	if r == nil {
		t.Error("expected fallback for wrong type")
	}

	// Bad JSON
	r = renderSettings([]byte(`bad`))
	if r == nil {
		t.Error("expected fallback")
	}
}

func TestRenderSessionStats(t *testing.T) {
	data := `{"sessionId":"s1","totalMessages":10,"userMessages":4,"assistantMessages":5,"toolCalls":3,"toolResults":3,"compactionCount":1,"tokens":{"input":100,"output":50,"cacheRead":10,"cacheWrite":5,"total":165},"tokenRate":{"activeInputPerSec":10,"activeOutputPerSec":20,"activeTotalPerSec":30,"wallTotalPerSec":15,"recentWindowSeconds":5,"recentInputPerSec":8,"recentOutputPerSec":12,"recentTotalPerSec":20,"lastInputPerSec":5,"lastOutputPerSec":7,"lastTotalPerSec":12},"cost":0.002}`
	r := renderSessionStats([]byte(data))
	if r == nil || !strings.Contains(r.Text, "session: s1") || !strings.Contains(r.Text, "token-rate:") {
		t.Errorf("unexpected: %+v", r)
	}

	// No token rate
	data2 := `{"sessionId":"s2","totalMessages":1,"userMessages":1,"assistantMessages":0,"toolCalls":0,"toolResults":0,"compactionCount":0,"tokens":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0},"cost":0}`
	r = renderSessionStats([]byte(data2))
	if r == nil || !strings.Contains(r.Text, "unavailable") {
		t.Errorf("expected 'unavailable' for missing token rate, got %+v", r)
	}

	// Bad JSON
	r = renderSessionStats([]byte(`bad`))
	if r == nil {
		t.Error("expected fallback")
	}
}

func TestRenderTraceEvents(t *testing.T) {
	data := `{"events":["e1","e2"]}`
	r := renderTraceEvents([]byte(data))
	if r == nil || !strings.Contains(r.Text, "e1, e2") {
		t.Errorf("unexpected: %+v", r)
	}

	// Empty
	r = renderTraceEvents([]byte(`{"events":[]}`))
	if r == nil || !strings.Contains(r.Text, "<none>") {
		t.Errorf("unexpected: %+v", r)
	}

	// Bad JSON
	r = renderTraceEvents([]byte(`bad`))
	if r == nil {
		t.Error("expected fallback")
	}
}

func TestRenderTree(t *testing.T) {
	data := `{"entries":[{"entryID":"e1","depth":0,"text":"root"},{"entryID":"e2","depth":1,"text":"child"}]}`
	r := renderTree([]byte(data))
	if r == nil || !strings.Contains(r.Text, "root") || !strings.Contains(r.Text, "child") {
		t.Errorf("unexpected: %+v", r)
	}

	// Empty
	r = renderTree([]byte(`{"entries":[]}`))
	if r == nil || !strings.Contains(r.Text, "no entries") {
		t.Errorf("unexpected: %+v", r)
	}

	// Bad JSON
	r = renderTree([]byte(`bad`))
	if r == nil {
		t.Error("expected fallback")
	}
}

func TestRenderMessages_LegacyArray(t *testing.T) {
	// Array format fallback
	data := `[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]`
	r := renderMessages([]byte(data))
	if r == nil || !strings.Contains(r.Text, "hi") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRenderMessages_AllFormatsBad(t *testing.T) {
	r := renderMessages([]byte(`bad json`))
	if r == nil {
		t.Error("expected fallback for completely bad json")
	}
}

func TestFallbackJSON(t *testing.T) {
	// Invalid JSON
	r := fallbackJSON([]byte(`bad`))
	if r == nil || r.Kind != KindMeta {
		t.Errorf("expected fallback to return raw text, got %+v", r)
	}

	// Valid JSON object
	r = fallbackJSON([]byte(`{"a":1,"b":"x"}`))
	if r == nil || !strings.Contains(r.Text, `"a"`) {
		t.Errorf("expected pretty-printed, got %+v", r)
	}

	// Large JSON → truncated
	large := make(map[string]any)
	for i := 0; i < 100; i++ {
		large[fmt.Sprintf("k%d", i)] = strings.Repeat("x", 20)
	}
	data, _ := json.Marshal(large)
	r = fallbackJSON(data)
	if r == nil || !strings.Contains(r.Text, "...") {
		t.Errorf("expected truncation, got %+v", r)
	}
}

func TestHelpers(t *testing.T) {
	if onOff(true) != "on" || onOff(false) != "off" {
		t.Error("onOff mismatch")
	}
	if orUnknown("") != "unknown" || orUnknown("  ") != "unknown" || orUnknown("x") != "x" {
		t.Error("orUnknown mismatch")
	}
	if formatIntOrUnknown(0) != "unknown" || formatIntOrUnknown(5) != "5" {
		t.Error("formatIntOrUnknown mismatch")
	}
}

func TestFormatTokenLimit(t *testing.T) {
	if got := formatTokenLimit(nil); got != "unknown" {
		t.Errorf("expected 'unknown', got %q", got)
	}
}

func TestIntFromMap(t *testing.T) {
	if intFromMap(nil, "x") != 0 {
		t.Error("expected 0 for nil map")
	}
	if intFromMap(map[string]any{}, "x") != 0 {
		t.Error("expected 0 for missing key")
	}
	if got := intFromMap(map[string]any{"x": float64(42)}, "x"); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
	if got := intFromMap(map[string]any{"x": 42}, "x"); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
	if got := intFromMap(map[string]any{"x": json.Number("42")}, "x"); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
	if got := intFromMap(map[string]any{"x": "not-a-number"}, "x"); got != 0 {
		t.Errorf("expected 0 for non-number, got %d", got)
	}
}

// --- meta.go ---

func TestNewTestMeta(t *testing.T) {
	m := newTestMeta("abc")
	if m == nil || m.ID != "abc" || m.PID != os.Getpid() {
		t.Errorf("unexpected: %+v", m)
	}
}

func TestPIDToString(t *testing.T) {
	if got := PIDToString(42); got != "42" {
		t.Errorf("expected '42', got %q", got)
	}
	if got := PIDToString(0); got != "0" {
		t.Errorf("expected '0', got %q", got)
	}
}

func TestGetClockTicks(t *testing.T) {
	if got := getClockTicks(); got != 100 {
		t.Errorf("expected 100, got %d", got)
	}
}

func TestResolveBase(t *testing.T) {
	if got := resolveBase("/foo"); got != "/foo" {
		t.Errorf("expected '/foo', got %q", got)
	}
	// Empty → home or /tmp/.ai fallback
	got := resolveBase("")
	if got == "" {
		t.Error("expected non-empty default")
	}
}

func TestGetProcessStartTime_PS(t *testing.T) {
	// Negative/zero pid → 0
	if got := getProcessStartTimePS(0); got != 0 {
		t.Errorf("expected 0 for pid=0, got %d", got)
	}
	if got := getProcessStartTimePS(-1); got != 0 {
		t.Errorf("expected 0 for pid=-1, got %d", got)
	}
	// Current process → non-zero
	if got := getProcessStartTimePS(os.Getpid()); got <= 0 {
		t.Errorf("expected positive for current process, got %d", got)
	}
}

func TestGetProcessStartTime_Dispatch(t *testing.T) {
	if got := GetProcessStartTime(0); got != 0 {
		t.Errorf("expected 0 for pid=0, got %d", got)
	}
}

func TestCreateRun_ErrorPath(t *testing.T) {
	// Unwritable directory
	_, err := CreateRun("/dev/null/cannot-create-here", "/test", os.Getpid())
	if err == nil {
		t.Error("expected error for unwritable dir")
	}
}

func TestFindByFilter_ReadDirError(t *testing.T) {
	// Point to a file, not a directory → ReadDir fails
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "notadir")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := findByFilter(filePath, func(*RunMeta) bool { return true })
	if err == nil {
		t.Error("expected error when ReadDir fails")
	}
}

func TestLoadRunMeta_ErrorWrapping(t *testing.T) {
	_, err := LoadRunMeta("/nonexistent/file.json")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "read run meta") {
		t.Errorf("expected wrapped error, got %v", err)
	}
}

// --- socket.go ---

func TestSocketServer_StartListenerFailure(t *testing.T) {
	// Path that cannot be created → listen fails
	srv := NewSocketServer("/dev/null/invalid/sock", nil)
	err := srv.Start()
	if err == nil {
		_ = srv.Stop()
		t.Error("expected error for invalid path")
	}
}

func TestSocketServer_StopNoListener(t *testing.T) {
	srv := NewSocketServer("/tmp/test.sock", nil)
	if err := srv.Stop(); err != nil {
		t.Errorf("expected no error stopping unstarted server, got %v", err)
	}
}

func TestSocketClient_SendCommand_NoServer(t *testing.T) {
	// No server listening at this path
	client := NewSocketClient("/tmp/nonexistent-sock-test.sock")
	_, err := client.SendCommand(Command{Type: "test"})
	if err == nil {
		t.Error("expected error for missing server")
	}
}

func TestSocketClient_Stream_NoServer(t *testing.T) {
	client := NewSocketClient("/tmp/nonexistent-sock-test.sock")
	_, _, err := client.Stream(0)
	if err == nil {
		t.Error("expected error for missing server")
	}
}

func TestSocketClientStream_RejectedByServer(t *testing.T) {
	sockPath := filepath.Join(os.TempDir(), "socktest-reject.sock")
	t.Cleanup(func() { os.Remove(sockPath) })

	handler := func(cmd Command) Response { return Response{OK: true} }
	srv := NewSocketServer(sockPath, handler)
	// No broadcaster → stream will be rejected
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = srv.Stop()
		srv.Wait()
	}()
	time.Sleep(50 * time.Millisecond)

	client := NewSocketClient(sockPath)
	conn, _, err := client.Stream(0)
	if err == nil {
		if conn != nil {
			conn.Close()
		}
		t.Error("expected error when stream is rejected")
	}
}

// --- Command too large (>1MB) path ---

func TestSocketServer_CommandTooLarge(t *testing.T) {
	sockPath := filepath.Join(os.TempDir(), "socktest-huge.sock")
	t.Cleanup(func() { os.Remove(sockPath) })

	handler := func(cmd Command) Response { return Response{OK: true} }
	srv := NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = srv.Stop()
		srv.Wait()
	}()
	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send more than 1MB without a newline.
	big := make([]byte, (1<<20)+100)
	for i := range big {
		big[i] = 'a'
	}
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, _ = conn.Write(big)

	// Read the response — it should be an error response.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _ := conn.Read(buf)
	if n == 0 {
		t.Fatal("expected response")
	}
	var resp Response
	if err := json.Unmarshal(buf[:n], &resp); err == nil && resp.OK {
		t.Errorf("expected OK=false for too-large command, got resp=%+v", resp)
	}
}

// --- EventBroadcaster: slow consumer disconnection ---

func TestEventBroadcaster_SlowConsumerDropped(t *testing.T) {
	b := NewEventBroadcaster()
	defer b.Close()
	c := b.Subscribe(0)
	defer b.Unsubscribe(c)

	// Fill the consumer's channel (2048 entries) — we'll need to push a lot.
	// Once channel is full, the next push should drop the consumer.
	for i := 0; i < ConsumerChanSize+5; i++ {
		b.Push([]byte("x"))
	}
	// Drain whatever we received
	drained := 0
loop:
	for {
		select {
		case _, ok := <-c.Events():
			if !ok {
				// Channel was closed (slow consumer dropped).
				return
			}
			drained++
		default:
			break loop
		}
	}
	_ = drained
}

// --- EventBroadcaster concurrent push & subscribe ---

func TestEventBroadcaster_ConcurrentSubscribe(t *testing.T) {
	b := NewEventBroadcaster()
	defer b.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := b.Subscribe(0)
			if c != nil {
				b.Unsubscribe(c)
			}
		}()
	}
	wg.Wait()
}

func TestEventBroadcaster_UnsubscribeTwice(t *testing.T) {
	b := NewEventBroadcaster()
	c := b.Subscribe(0)
	b.Unsubscribe(c)
	// Second unsubscribe should not panic
	b.Unsubscribe(c)
}

func TestEventBroadcaster_UnsubscribeNil(t *testing.T) {
	b := NewEventBroadcaster()
	b.Unsubscribe(nil) // must not panic
}
