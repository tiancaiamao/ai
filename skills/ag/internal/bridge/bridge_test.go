package bridge

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genius/ag/internal/storage"
)

// ---------------------------------------------------------------------------
// ActivityWriter tests
// ---------------------------------------------------------------------------

// TestNewActivityWriter_CreatesFile: New writer creates activity.json on first write
func TestNewActivityWriter_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)

	aw.UpdateStatus(StatusRunning)

	path := filepath.Join(dir, activityFileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("activity.json should have been created after UpdateStatus")
	}

	var act AgentActivity
	if err := storage.ReadJSON(path, &act); err != nil {
		t.Fatalf("failed to read activity.json: %v", err)
	}
	if act.Status != StatusRunning {
		t.Fatalf("expected status running, got %s", act.Status)
	}

	// Verify aw is usable (not nil)
	_ = aw
}

// TestActivityWriter_UpdateStatus: Status change writes immediately
func TestActivityWriter_UpdateStatus(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)

	aw.UpdateStatus(StatusRunning)
	aw.UpdateStatus(StatusDone)

	path := filepath.Join(dir, activityFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read activity.json: %v", err)
	}

	var act AgentActivity
	if err := json.Unmarshal(data, &act); err != nil {
		t.Fatalf("failed to parse activity.json: %v", err)
	}

	if act.Status != StatusDone {
		t.Fatalf("expected status done, got %s", act.Status)
	}
	if act.FinishedAt == 0 {
		t.Fatal("expected FinishedAt to be set for done status")
	}
}

// TestActivityWriter_RateLimiting: Two rapid text-only updates within 2s result in only 1 write
func TestActivityWriter_RateLimiting(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)

	// First write to establish baseline
	aw.UpdateActivity(func(a *AgentActivity) {
		a.LastText = "first"
	})

	// Rapid second write — should be rate-limited
	aw.UpdateActivity(func(a *AgentActivity) {
		a.LastText = "second"
	})

	path := filepath.Join(dir, activityFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read activity.json: %v", err)
	}

	var act AgentActivity
	if err := json.Unmarshal(data, &act); err != nil {
		t.Fatalf("failed to parse activity.json: %v", err)
	}

	// Because rate limiting skipped the second write, the file should still have "first"
	if act.LastText != "first" {
		t.Fatalf("expected rate-limited write to keep 'first', got %q", act.LastText)
	}
}

// TestActivityWriter_ToolUpdate_Immediate: Tool update bypasses rate limiting
func TestActivityWriter_ToolUpdate_Immediate(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)

	// First text write
	aw.UpdateActivity(func(a *AgentActivity) {
		a.LastText = "hello"
	})

	// Rapid tool update (not just LastText change) — should bypass rate limit
	aw.UpdateActivity(func(a *AgentActivity) {
		a.LastTool = "Bash"
	})

	path := filepath.Join(dir, activityFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read activity.json: %v", err)
	}

	var act AgentActivity
	if err := json.Unmarshal(data, &act); err != nil {
		t.Fatalf("failed to parse activity.json: %v", err)
	}

	if act.LastTool != "Bash" {
		t.Fatalf("expected tool update to be written immediately, got lastTool=%q", act.LastTool)
	}
}

// TestActivityWriter_TerminalStatus_SetsFinishedAt: done/failed/killed sets FinishedAt
func TestActivityWriter_TerminalStatus_SetsFinishedAt(t *testing.T) {
	for _, status := range []AgentStatus{StatusDone, StatusFailed, StatusKilled} {
		t.Run(string(status), func(t *testing.T) {
			subDir := t.TempDir()
			aw := NewActivityWriter(subDir)
			aw.UpdateStatus(status)

			path := filepath.Join(subDir, activityFileName)
			var act AgentActivity
			if err := storage.ReadJSON(path, &act); err != nil {
				t.Fatalf("failed to read activity.json: %v", err)
			}
			if act.FinishedAt == 0 {
				t.Fatalf("expected FinishedAt to be set for status %s", status)
			}
		})
	}
}

// TestActivityWriter_SetError: Sets error message and status to failed
func TestActivityWriter_SetError(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)

	aw.SetError("something went wrong")

	path := filepath.Join(dir, activityFileName)
	var act AgentActivity
	if err := storage.ReadJSON(path, &act); err != nil {
		t.Fatalf("failed to read activity.json: %v", err)
	}

	if act.Status != StatusFailed {
		t.Fatalf("expected status failed, got %s", act.Status)
	}
	if act.Error != "something went wrong" {
		t.Fatalf("expected error message, got %q", act.Error)
	}
	if act.FinishedAt == 0 {
		t.Fatal("expected FinishedAt to be set")
	}
}

// TestActivityWriter_Resume: Can read existing activity.json on construction
func TestActivityWriter_Resume(t *testing.T) {
	dir := t.TempDir()

	// Write initial activity.json
	initial := AgentActivity{
		Status:   StatusRunning,
		Turns:    5,
		TokensIn: 100,
		LastTool: "Read",
	}
	path := filepath.Join(dir, activityFileName)
	if err := storage.AtomicWriteJSON(path, &initial); err != nil {
		t.Fatalf("failed to write initial activity: %v", err)
	}

	// Create a new writer — it should resume from existing data
	aw := NewActivityWriter(dir)

	aw.UpdateStatus(StatusDone)

	// Read back and verify prior fields were preserved
	var act AgentActivity
	if err := storage.ReadJSON(path, &act); err != nil {
		t.Fatalf("failed to read activity.json: %v", err)
	}

	if act.Turns != 5 {
		t.Fatalf("expected resumed Turns=5, got %d", act.Turns)
	}
	if act.TokensIn != 100 {
		t.Fatalf("expected resumed TokensIn=100, got %d", act.TokensIn)
	}
	if act.LastTool != "Read" {
		t.Fatalf("expected resumed LastTool=Read, got %s", act.LastTool)
	}
	if act.Status != StatusDone {
		t.Fatalf("expected status done after update, got %s", act.Status)
	}
}

// ---------------------------------------------------------------------------
// EventReader tests
// ---------------------------------------------------------------------------

// helper: create an EventReader reading from a string
func newTestEventReader(input string, dir string) *EventReader {
	aw := NewActivityWriter(dir)
	return NewEventReader(strings.NewReader(input), aw, dir)
}

func readActivity(t *testing.T, dir string) AgentActivity {
	t.Helper()
	path := filepath.Join(dir, activityFileName)
	var act AgentActivity
	if err := storage.ReadJSON(path, &act); err != nil {
		t.Fatalf("failed to read activity.json: %v", err)
	}
	return act
}

// TestEventReader_AgentStart: Parses agent_start event → status running
func TestEventReader_AgentStart(t *testing.T) {
	dir := t.TempDir()
	er := newTestEventReader(`{"type":"agent_start"}`, dir)

	if err := er.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	act := readActivity(t, dir)
	if act.Status != StatusRunning {
		t.Fatalf("expected status running, got %s", act.Status)
	}
}

// TestEventReader_TurnEvents: turn_start increments turns, turn_end updates tokens
func TestEventReader_TurnEvents(t *testing.T) {
	dir := t.TempDir()
	input := `{"type":"agent_start"}
{"type":"turn_start"}
{"type":"turn_start"}
{"type":"turn_end","data":{"tokensBefore":100,"tokensAfter":200}}`

	er := newTestEventReader(input, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	act := readActivity(t, dir)
	if act.Turns != 2 {
		t.Fatalf("expected 2 turns, got %d", act.Turns)
	}
	if act.TokensIn != 100 {
		t.Fatalf("expected TokensIn=100, got %d", act.TokensIn)
	}
	if act.TokensOut != 100 {
		t.Fatalf("expected TokensOut=100, got %d", act.TokensOut)
	}
	if act.TokensTotal != 200 {
		t.Fatalf("expected TokensTotal=200, got %d", act.TokensTotal)
	}
}

// TestEventReader_ToolEvents: tool_execution_start sets LastTool
func TestEventReader_ToolEvents(t *testing.T) {
	dir := t.TempDir()
	input := `{"type":"tool_execution_start","data":{"tool":"Bash"}}`

	er := newTestEventReader(input, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	act := readActivity(t, dir)
	if act.LastTool != "Bash" {
		t.Fatalf("expected LastTool=Bash, got %q", act.LastTool)
	}
}

// TestEventReader_MessageUpdate: message_update accumulates text_delta into LastText
func TestEventReader_MessageUpdate(t *testing.T) {
	dir := t.TempDir()
	input := `{"type":"message_update","data":{"text_delta":"Hello "}}
{"type":"message_update","data":{"text_delta":"World"}}`

	er := newTestEventReader(input, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	output := er.Output()
	if output != "Hello World" {
		t.Fatalf("expected output 'Hello World', got %q", output)
	}
}

// TestEventReader_ErrorEvent: error event sets status to failed
func TestEventReader_ErrorEvent(t *testing.T) {
	dir := t.TempDir()
	input := `{"type":"error","error":"out of memory"}`

	er := newTestEventReader(input, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	act := readActivity(t, dir)
	if act.Status != StatusFailed {
		t.Fatalf("expected status failed, got %s", act.Status)
	}
	if act.Error != "out of memory" {
		t.Fatalf("expected error 'out of memory', got %q", act.Error)
	}
}

// TestEventReader_MalformedJSON: Malformed line is skipped without error
func TestEventReader_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	input := `not json at all
{"type":"agent_start"}
{broken json
{"type":"tool_execution_start","data":{"tool":"Read"}}`

	er := newTestEventReader(input, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	act := readActivity(t, dir)
	if act.Status != StatusRunning {
		t.Fatalf("expected status running, got %s", act.Status)
	}
	if act.LastTool != "Read" {
		t.Fatalf("expected LastTool=Read, got %q", act.LastTool)
	}
}

// TestEventReader_EOF: Clean EOF returns nil error
func TestEventReader_EOF(t *testing.T) {
	dir := t.TempDir()
	er := newTestEventReader("", dir)

	if err := er.Run(); err != nil {
		t.Fatalf("expected nil error on clean EOF, got: %v", err)
	}
}

// TestEventReader_Output: Accumulated output is retrievable via Output()
func TestEventReader_Output(t *testing.T) {
	dir := t.TempDir()
	input := `{"type":"message_update","data":{"text_delta":"line1"}}
{"type":"message_update","data":{"text_delta":"line2"}}`

	er := newTestEventReader(input, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	got := er.Output()
	if got != "line1line2" {
		t.Fatalf("expected output 'line1line2', got %q", got)
	}

	// Also check the output file was written
	outputPath := filepath.Join(dir, "output")
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if string(data) != "line1line2" {
		t.Fatalf("expected output file content 'line1line2', got %q", string(data))
	}
}

// ---------------------------------------------------------------------------
// SocketServer tests
// ---------------------------------------------------------------------------

// helper: send a command and read response over a unix socket
func sendCommand(t *testing.T, sockPath string, cmd BridgeCommand) BridgeResponse {
	t.Helper()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to dial socket: %v", err)
	}
	defer conn.Close()

	// Set deadline to prevent hangs
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("failed to marshal command: %v", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		t.Fatalf("failed to write command: %v", err)
	}

	// Read response
	var buf [4096]byte
	n, err := conn.Read(buf[:])
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp BridgeResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	return resp
}

// TestSocketServer_BasicCommand: Connect, send command, get response
func TestSocketServer_BasicCommand(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	handler := func(cmd BridgeCommand) BridgeResponse {
		if cmd.Type == CmdGetState {
			return BridgeResponse{OK: true, Data: "state-data"}
		}
		return BridgeResponse{OK: false, Error: "unknown command"}
	}

	srv := NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop()

	resp := sendCommand(t, sockPath, BridgeCommand{Type: CmdGetState})
	if !resp.OK {
		t.Fatalf("expected OK response, got error: %s", resp.Error)
	}
	if resp.Data != "state-data" {
		t.Fatalf("expected data 'state-data', got %v", resp.Data)
	}
}

// TestSocketServer_UnknownCommand: Unknown type returns error response
func TestSocketServer_UnknownCommand(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "s.sock")

	handler := func(cmd BridgeCommand) BridgeResponse {
		return BridgeResponse{OK: false, Error: fmt.Sprintf("unknown command type: %s", cmd.Type)}
	}

	srv := NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop()

	resp := sendCommand(t, sockPath, BridgeCommand{Type: "nonexistent"})
	if resp.OK {
		t.Fatal("expected error response for unknown command")
	}
	if !strings.Contains(resp.Error, "unknown command type: nonexistent") {
		t.Fatalf("unexpected error message: %s", resp.Error)
	}
}

// TestSocketServer_LargePayload: 100KB command is handled correctly
func TestSocketServer_LargePayload(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	// Create a command with a large message (~100KB)
	largeMsg := strings.Repeat("x", 100*1024)

	handler := func(cmd BridgeCommand) BridgeResponse {
		return BridgeResponse{OK: true, Data: len(cmd.Message)}
	}

	srv := NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop()

	resp := sendCommand(t, sockPath, BridgeCommand{Type: CmdSteer, Message: largeMsg})
	if !resp.OK {
		t.Fatalf("expected OK response for large payload, got error: %s", resp.Error)
	}
	// Data should contain the length as a float64 (JSON number)
	dataLen := int64(resp.Data.(float64))
	if dataLen != int64(len(largeMsg)) {
		t.Fatalf("expected message length %d, got %d", len(largeMsg), dataLen)
	}
}

// TestSocketServer_Stop: Stop() terminates the accept loop
func TestSocketServer_Stop(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	handler := func(cmd BridgeCommand) BridgeResponse {
		return BridgeResponse{OK: true}
	}

	srv := NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// Stop the server
	if err := srv.Stop(); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}

	// Wait should return promptly (within a timeout)
	done := make(chan struct{})
	go func() {
		srv.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(3 * time.Second):
		t.Fatal("Wait() did not return after Stop()")
	}
}

// TestSocketServer_StaleSocket: New server removes stale socket file
func TestSocketServer_StaleSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	handler := func(cmd BridgeCommand) BridgeResponse {
		return BridgeResponse{OK: true}
	}

	// Create a stale socket file (or any file at the path)
	if err := os.WriteFile(sockPath, []byte("stale"), 0644); err != nil {
		t.Fatalf("failed to create stale socket file: %v", err)
	}

	// Starting a new server should remove the stale file and succeed
	srv := NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start server with stale socket: %v", err)
	}
	defer srv.Stop()

	// Verify the socket is now a real socket (not our stale file)
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to connect to new socket: %v", err)
	}
	conn.Close()
}

// ---------------------------------------------------------------------------
// EventReader extended tests
// ---------------------------------------------------------------------------

// TestEventReader_LargeToken verifies EventReader handles lines > 64KB
// (default bufio.Scanner limit) without error.
func TestEventReader_LargeToken(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)
	aw.UpdateStatus(StatusRunning)

	// Build a JSON line with ~100KB of text_delta content
	largeText := strings.Repeat("A", 100*1024)
	rpcLine := map[string]any{
		"type": "message_update",
		"data": map[string]any{
			"text_delta": largeText,
		},
	}
	lineData, err := json.Marshal(rpcLine)
	if err != nil {
		t.Fatalf("marshal rpc line: %v", err)
	}
	lineData = append(lineData, '\n')

	// Feed it through EventReader
	r := strings.NewReader(string(lineData))
	er := NewEventReader(r, aw, dir)

	if err := er.Run(); err != nil {
		t.Fatalf("EventReader.Run() failed on large token: %v", err)
	}

	// Verify output accumulated correctly
	output := er.Output()
	if len(output) < 100*1024 {
		t.Fatalf("expected at least 100KB of output, got %d bytes", len(output))
	}
}

// ---------------------------------------------------------------------------
// Actual ai RPC format tests — the real event format emitted by ai --mode rpc
// ---------------------------------------------------------------------------

// TestEventReader_AiRpc_AgentEnd_NoSuccessField: agent_end without "success"
// should be treated as success (no error = done).
func TestEventReader_AiRpc_AgentEnd_NoSuccessField(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)
	aw.UpdateStatus(StatusRunning)

	// Actual ai RPC output: {"type":"agent_end","messages":[...]}
	// No "success" field at all.
	input := `{"type":"agent_end","messages":[{"role":"user","content":"hi"}]}` + "\n"

	r := strings.NewReader(input)
	er := NewEventReader(r, aw, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	path := filepath.Join(dir, activityFileName)
	var act AgentActivity
	if err := storage.ReadJSON(path, &act); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if act.Status != StatusDone {
		t.Fatalf("expected status done (no error = success), got %s", act.Status)
	}
}

// TestEventReader_AiRpc_AgentEnd_WithError: agent_end with error field
// should be treated as failed.
func TestEventReader_AiRpc_AgentEnd_WithError(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)
	aw.UpdateStatus(StatusRunning)

	input := `{"type":"agent_end","error":"API rate limit exceeded"}` + "\n"

	r := strings.NewReader(input)
	er := NewEventReader(r, aw, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	path := filepath.Join(dir, activityFileName)
	var act AgentActivity
	if err := storage.ReadJSON(path, &act); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if act.Status != StatusFailed {
		t.Fatalf("expected status failed, got %s", act.Status)
	}
	if act.Error != "API rate limit exceeded" {
		t.Fatalf("expected error message, got %q", act.Error)
	}
}

// TestEventReader_AiRpc_MessageUpdate_AssistantMessageEvent: actual format
// uses assistantMessageEvent.delta, not data.text_delta.
func TestEventReader_AiRpc_MessageUpdate_AssistantMessageEvent(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)
	aw.UpdateStatus(StatusRunning)

	lines := strings.Join([]string{
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hello "}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"World!"}}`,
	}, "\n") + "\n"

	r := strings.NewReader(lines)
	er := NewEventReader(r, aw, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	output := er.Output()
	if output != "Hello World!" {
		t.Fatalf("expected 'Hello World!', got %q", output)
	}
}

// TestEventReader_AiRpc_TurnEnd_UsageInMessage: actual format puts usage
// in message.usage.input/output, not data.tokensBefore/tokensAfter.
func TestEventReader_AiRpc_TurnEnd_UsageInMessage(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)
	aw.UpdateStatus(StatusRunning)

	// Simulate actual ai RPC turn_end event
	input := `{"type":"turn_end","message":{"role":"assistant","usage":{"input":3839,"output":42,"totalTokens":3881}}}` + "\n"

	r := strings.NewReader(input)
	er := NewEventReader(r, aw, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	path := filepath.Join(dir, activityFileName)
	var act AgentActivity
	if err := storage.ReadJSON(path, &act); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if act.TokensIn != 3839 {
		t.Fatalf("expected TokensIn=3839, got %d", act.TokensIn)
	}
	if act.TokensOut != 42 {
		t.Fatalf("expected TokensOut=42, got %d", act.TokensOut)
	}
	if act.TokensTotal != 3881 {
		t.Fatalf("expected TokensTotal=3881, got %d", act.TokensTotal)
	}
}

// TestEventReader_AiRpc_ToolExecution_TopLevelToolName: actual format puts
// toolName at top level, not inside data.tool.
func TestEventReader_AiRpc_ToolExecution_TopLevelToolName(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)
	aw.UpdateStatus(StatusRunning)

	input := `{"type":"tool_execution_start","toolName":"bash","toolCallId":"call_123","args":{"command":"ls"}}` + "\n"

	r := strings.NewReader(input)
	er := NewEventReader(r, aw, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	path := filepath.Join(dir, activityFileName)
	var act AgentActivity
	if err := storage.ReadJSON(path, &act); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if act.LastTool != "bash" {
		t.Fatalf("expected LastTool='bash', got %q", act.LastTool)
	}
}

// TestEventReader_AiRpc_FullWorkflow: simulate a complete agent interaction
// using actual ai RPC event format and verify status=done, output, tokens.
func TestEventReader_AiRpc_FullWorkflow(t *testing.T) {
	dir := t.TempDir()
	aw := NewActivityWriter(dir)
	aw.UpdateStatus(StatusRunning)

	events := []string{
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hello"}}`,
		`{"type":"turn_end","message":{"role":"assistant","usage":{"input":100,"output":5,"totalTokens":105}}}`,
		`{"type":"agent_end","messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"Hello"}]}`,
	}
	input := strings.Join(events, "\n") + "\n"

	r := strings.NewReader(input)
	er := NewEventReader(r, aw, dir)
	if err := er.Run(); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	// Check output
	if er.Output() != "Hello" {
		t.Fatalf("expected output 'Hello', got %q", er.Output())
	}

	// Check activity
	path := filepath.Join(dir, activityFileName)
	var act AgentActivity
	if err := storage.ReadJSON(path, &act); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	if act.Status != StatusDone {
		t.Fatalf("expected status done, got %s", act.Status)
	}
	if act.Turns != 1 {
		t.Fatalf("expected 1 turn, got %d", act.Turns)
	}
	if act.TokensIn != 100 || act.TokensOut != 5 {
		t.Fatalf("expected tokens 100/5, got %d/%d", act.TokensIn, act.TokensOut)
	}
}
