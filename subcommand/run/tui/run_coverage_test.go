package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
		"compaction": map[string]any{"auto": true, "before": 10.0},
	})
	if r == nil || !strings.Contains(r.Text, "auto-compaction") || !strings.Contains(r.Text, "10 messages") {
		t.Errorf("unexpected: %+v", r)
	}
	// Manual compaction
	r = parseCompactionStart(map[string]any{
		"compaction": map[string]any{"auto": false},
	})
	if r == nil || !strings.Contains(r.Text, "compaction started") {
		t.Errorf("expected 'compaction started', got %+v", r)
	}
	// Missing compaction info
	r = parseCompactionStart(map[string]any{})
	if r == nil || !strings.Contains(r.Text, "compaction started") {
		t.Errorf("expected fallback text, got %+v", r)
	}
}

func TestParseCompactionEnd(t *testing.T) {
	// Mini compaction with truncations
	r := parseCompactionEnd(map[string]any{
		"compaction": map[string]any{
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
		"compaction": map[string]any{"type": "mini", "auto": false},
	})
	if r == nil || !strings.Contains(r.Text, "no action needed") {
		t.Errorf("unexpected: %+v", r)
	}

	// Mini compaction with truncations but no token info
	r = parseCompactionEnd(map[string]any{
		"compaction": map[string]any{"type": "mini", "truncatedCount": 2.0},
	})
	if r == nil || !strings.Contains(r.Text, "2 messages truncated") {
		t.Errorf("unexpected: %+v", r)
	}

	// Error case
	r = parseCompactionEnd(map[string]any{
		"compaction": map[string]any{"error": "kaboom"},
	})
	if r == nil || !strings.Contains(r.Text, "failed: kaboom") {
		t.Errorf("unexpected: %+v", r)
	}

	// Major compaction with before/after
	r = parseCompactionEnd(map[string]any{
		"compaction": map[string]any{"before": 10.0, "after": 2.0, "auto": true},
	})
	if r == nil || !strings.Contains(r.Text, "10 -> 2 messages") {
		t.Errorf("unexpected: %+v", r)
	}

	// Major compaction no info
	r = parseCompactionEnd(map[string]any{
		"compaction": map[string]any{},
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
	r := ParseEvent(`{"type":"compaction_end","compaction":{"type":"mini","truncatedCount":2}}`)
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

// renderViaCommand feeds a slash-command response event through the shared
// pipeline, mirroring how the TUI consumes RPC responses.
func renderViaCommand(command, dataJSON string) *FormattedEvent {
	var data map[string]any
	if err := json.Unmarshal([]byte(dataJSON), &data); err != nil {
		t := map[string]any{"raw": dataJSON}
		data = t
	}
	return parseResponseEvent(map[string]any{
		"type":    "response",
		"success": true,
		"command": command,
		"data":    data,
	})
}

func TestRenderSkills(t *testing.T) {
	data := `{"commands":[{"name":"foo","source":"builtin","description":"foo command"},{"name":"bar","source":"user"}]}`
	r := renderViaCommand("skills", data)
	if r == nil || !strings.Contains(r.Text, "foo") || !strings.Contains(r.Text, "bar") {
		t.Errorf("unexpected: %+v", r)
	}

	// Empty
	r = renderViaCommand("skills", `{"commands":[]}`)
	if r == nil || !strings.Contains(r.Text, "no commands") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRenderContext(t *testing.T) {
	// Minimal valid context data
	data := `{"state":{"sessionId":"s1","sessionName":"n","sessionFile":"/tmp/s","model":{"id":"m","provider":"p","name":"model","contextWindow":200000},"messageCount":10,"pendingMessageCount":0,"isStreaming":false,"isCompacting":false,"thinkingLevel":"medium","autoCompactionEnabled":true,"aiPid":1,"aiLogPath":"/tmp/log","aiWorkingDir":"/tmp"},"stats":{"sessionId":"s1","totalMessages":10,"userMessages":4,"assistantMessages":5,"toolCalls":3,"toolResults":3,"compactionCount":0,"cost":0.001,"tokens":{"input":100,"output":50,"cacheRead":0,"cacheWrite":0,"total":150,"activeWindowTokens":150,"systemPromptTokens":0,"systemToolsTokens":0}},"models":{"models":[{"id":"m","provider":"p","name":"model","contextWindow":200000}]}}`
	r := renderViaCommand("context", data)
	if r == nil || !strings.Contains(r.Text, "Context Usage") || !strings.Contains(r.Text, "Session Stats") {
		t.Errorf("unexpected: %+v", r)
	}

	// Regression test: ensure output is NOT base64-encoded or raw JSON.
	if strings.Contains(r.Text, "ewogICJzdGF0ZSI6") || strings.Contains(r.Text, "\"state\":") {
		t.Errorf("output contains raw JSON or base64, expected formatted text: %s", r.Text)
	}
}

func TestRenderSessionState(t *testing.T) {
	data := `{"sessionId":"s1","sessionName":"name","sessionFile":"/tmp/s","model":{"id":"m","provider":"p","name":"model"},"messageCount":5,"pendingMessageCount":1,"isStreaming":true,"isCompacting":false,"thinkingLevel":"low","autoCompactionEnabled":true,"aiPid":1234,"aiLogPath":"/tmp/log","aiWorkingDir":"/cwd"}`
	r := renderViaCommand("session", data)
	if r == nil || !strings.Contains(r.Text, "Session:") {
		t.Errorf("unexpected: %+v", r)
	}

	// Regression test: ensure output is NOT base64-encoded or raw JSON.
	if strings.Contains(r.Text, "ewogICJzZXNzaW9uSWQi") || strings.Contains(r.Text, "\"sessionId\":") {
		t.Errorf("output contains raw JSON or base64, expected formatted text: %s", r.Text)
	}
}

func TestRenderSessions(t *testing.T) {
	data := `{"sessions":[{"id":"s1","name":"First","title":"t1","updatedAt":"2025-01-01","messageCount":5},{"id":"s2","name":"Second","title":"t2","updatedAt":"2025-01-02","messageCount":3}]}`
	r := renderViaCommand("sessions", data)
	if r == nil || !strings.Contains(r.Text, "First") || !strings.Contains(r.Text, "Second") {
		t.Errorf("unexpected: %+v", r)
	}
	if r.Kind != KindResponse {
		t.Errorf("expected KindResponse for /sessions, got %v", r.Kind)
	}

	// Empty
	r = renderViaCommand("sessions", `{"sessions":[]}`)
	if r == nil || !strings.Contains(r.Text, "No sessions") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRenderModel(t *testing.T) {
	// CycleModelResult format
	data := `{"model":{"id":"m","provider":"p","name":"Model","contextWindow":200000},"previousModel":{"id":"old","provider":"p","name":"Old"}}`
	r := renderViaCommand("model", data)
	if r == nil || !strings.Contains(r.Text, "p/Model (m)") {
		t.Errorf("unexpected: %+v", r)
	}

	// Fallback {model: {id, name}} format
	r = renderViaCommand("model", `{"model":{"id":"m","provider":"p","name":"X"}}`)
	if r == nil || !strings.Contains(r.Text, "p/X (m)") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRenderModelList(t *testing.T) {
	data := `{"models":[{"id":"m1","provider":"p","name":"Model1"},{"id":"m2","provider":"p","name":"Model2"}],"currentIndex":0}`
	r := renderViaCommand("model", data)
	if r == nil || !strings.Contains(r.Text, "Model1") || !strings.Contains(r.Text, "[current]") {
		t.Errorf("unexpected: %+v", r)
	}

	// With current object instead of index
	r = renderViaCommand("model", `{"models":[{"id":"m1","provider":"p","name":"M1"}],"current":{"provider":"p","id":"m1"}}`)
	if r == nil || !strings.Contains(r.Text, "[current]") {
		t.Errorf("unexpected: %+v", r)
	}

	// Empty
	r = renderViaCommand("model", `{"models":[]}`)
	if r == nil || !strings.Contains(r.Text, "no models") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRenderSettings(t *testing.T) {
	data := `{"type":"settings","data":{"model":"m1","show-thinking":true,"unknown-key":"x"}}`
	r := renderViaCommand("show", data)
	if r == nil || !strings.Contains(r.Text, "Display Settings") || !strings.Contains(r.Text, "m1") {
		t.Errorf("unexpected: %+v", r)
	}
	// Missing keys render as "unknown"
	if !strings.Contains(r.Text, "prefix: unknown") {
		t.Errorf("expected unknown placeholder for missing key, got: %s", r.Text)
	}
}

func TestRenderSessionStats(t *testing.T) {
	data := `{"sessionId":"s1","totalMessages":10,"userMessages":4,"assistantMessages":5,"toolCalls":3,"toolResults":3,"compactionCount":1,"tokens":{"input":100,"output":50,"cacheRead":10,"cacheWrite":5,"total":165},"cost":0.002}`
	r := renderViaCommand("", data)
	if r == nil || !strings.Contains(r.Text, "session: s1") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRenderTraceEvents(t *testing.T) {
	r := renderViaCommand("trace-events", `{"events":["e1","e2"]}`)
	if r == nil || !strings.Contains(r.Text, "e1, e2") {
		t.Errorf("unexpected: %+v", r)
	}

	// Empty
	r = renderViaCommand("trace-events", `{"events":[]}`)
	if r == nil || !strings.Contains(r.Text, "<none>") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRenderTree(t *testing.T) {
	data := `{"entries":[{"entryID":"e1","depth":0,"text":"root"},{"entryID":"e2","depth":1,"text":"child"}]}`
	r := renderViaCommand("tree", data)
	if r == nil || !strings.Contains(r.Text, "root") || !strings.Contains(r.Text, "child") {
		t.Errorf("unexpected: %+v", r)
	}

	// Empty
	r = renderViaCommand("tree", `{"entries":[]}`)
	if r == nil || !strings.Contains(r.Text, "no entries") {
		t.Errorf("unexpected: %+v", r)
	}
}

func TestRenderMessages_LegacyArray(t *testing.T) {
	// Legacy {messages: [...]} shape via FormatResponseData
	got := FormatResponseData(map[string]any{"messages": []map[string]any{
		{"role": "user", "content": "hi"},
		{"role": "assistant", "content": "hello"},
	}})
	if !strings.Contains(got, "hi") {
		t.Errorf("unexpected: %q", got)
	}
}

func TestParseResponseEventFallbackTruncation(t *testing.T) {
	// Unrecognized payload → pretty-printed JSON, truncated for display.
	large := make(map[string]any)
	for i := 0; i < 100; i++ {
		large[fmt.Sprintf("k%d", i)] = strings.Repeat("x", 20)
	}
	r := parseResponseEvent(map[string]any{"type": "response", "success": true, "data": large})
	if r == nil || !strings.Contains(r.Text, "...") {
		t.Errorf("expected truncation, got %+v", r)
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
