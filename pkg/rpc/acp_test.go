package rpc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

// runACPSmoke starts RunACP with the given NDJSON lines, returns all
// JSON-RPC messages (responses and notifications) read from stdout.
func runACPSmoke(t *testing.T, tmpDir string, lines []string) []map[string]any {
	t.Helper()

	// Ensure API key is set so resolveModelAndKey doesn't fail in CI.
	t.Setenv("ZAI_API_KEY", "test-key")

	// Isolate config to avoid polluting ~/.ai/config.json.
	configPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("AI_CONFIG_PATH", configPath)

	reader, writer := io.Pipe()
	outReader, outWriter := io.Pipe()

	go func() {
		for _, l := range lines {
			writer.Write([]byte(l + "\n"))
		}
		writer.Close()
	}()

	respCh := make(chan []map[string]any, 1)
	go func() {
		var results []map[string]any
		scanner := bufio.NewScanner(outReader)
		scanner.Buffer(make([]byte, 0, 4*1024*1024), 16*1024*1024)
		for scanner.Scan() {
			var m map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
				continue
			}
			results = append(results, m)
		}
		respCh <- results
	}()

	_ = RunACP(tmpDir, "", reader, outWriter, "", 0, 5*time.Second, "", "", "acp-smoke")
	outWriter.Close()

	return <-respCh
}

func TestACPInitialize(t *testing.T) {
	msgs := runACPSmoke(t, t.TempDir(), []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`,
	})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %v", len(msgs), msgs)
	}
	m := msgs[0]
	if m["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %v", m["jsonrpc"])
	}
	if id, _ := m["id"].(float64); id != 1 {
		t.Errorf("expected id 1, got %v", m["id"])
	}
	result, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", m)
	}
	if v, _ := result["protocolVersion"].(float64); v != acpProtocolVersion {
		t.Errorf("expected protocolVersion %d, got %v", acpProtocolVersion, result["protocolVersion"])
	}
	caps, ok := result["agentCapabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected agentCapabilities object, got %v", result)
	}
	promptCaps, ok := caps["promptCapabilities"].(map[string]any)
	if !ok {
		t.Fatalf("expected promptCapabilities object, got %v", caps)
	}
	if embedded, _ := promptCaps["embeddedContext"].(bool); !embedded {
		t.Errorf("expected embeddedContext capability true, got %v", promptCaps["embeddedContext"])
	}
	if loadSession, _ := caps["loadSession"].(bool); !loadSession {
		t.Errorf("expected loadSession capability true, got %v", caps["loadSession"])
	}
}

func TestACPSessionNewAndPromptErrors(t *testing.T) {
	msgs := runACPSmoke(t, t.TempDir(), []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`,
		// Unknown session id must be rejected.
		`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":"bogus","prompt":[{"type":"text","text":"hi"}]}}`,
		// Empty prompt must be rejected before any LLM call.
		`{"jsonrpc":"2.0","id":4,"method":"session/prompt","params":{"sessionId":"","prompt":[{"type":"text","text":"   "}]}}`,
	})
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d: %v", len(msgs), msgs)
	}

	// initialize
	result, _ := msgs[0]["result"].(map[string]any)
	if v, _ := result["protocolVersion"].(float64); v != acpProtocolVersion {
		t.Errorf("initialize: unexpected protocolVersion: %v", result)
	}

	// session/new
	result, _ = msgs[1]["result"].(map[string]any)
	sid, _ := result["sessionId"].(string)
	if sid == "" {
		t.Errorf("session/new: expected non-empty sessionId, got %v", result)
	}

	// slash-command advertisement notification right after session/new
	if m, _ := msgs[2]["method"].(string); m != "session/update" {
		t.Errorf("expected session/update advertisement after session/new, got %q", m)
	}
	params, _ := msgs[2]["params"].(map[string]any)
	upd, _ := params["update"].(map[string]any)
	if got, _ := upd["sessionUpdate"].(string); got != "available_commands_update" {
		t.Errorf("expected available_commands_update advertisement, got %v", upd)
	}
	cmds, _ := upd["commands"].([]any)
	if len(cmds) == 0 {
		t.Errorf("expected advertised commands, got none")
	}

	// unknown session id -> invalid params error
	if errObj, ok := msgs[3]["error"].(map[string]any); !ok {
		t.Errorf("prompt with unknown sessionId: expected error, got %v", msgs[3])
	} else if code, _ := errObj["code"].(float64); code != acpErrInvalidParams {
		t.Errorf("expected error code %d, got %v", acpErrInvalidParams, errObj["code"])
	}

	// empty prompt -> invalid params error
	if errObj, ok := msgs[4]["error"].(map[string]any); !ok {
		t.Errorf("empty prompt: expected error, got %v", msgs[4])
	} else if code, _ := errObj["code"].(float64); code != acpErrInvalidParams {
		t.Errorf("expected error code %d, got %v", acpErrInvalidParams, errObj["code"])
	}
}

func TestACPUnknownMethodAndNotification(t *testing.T) {
	msgs := runACPSmoke(t, t.TempDir(), []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`,
		// Unknown method -> method not found.
		`{"jsonrpc":"2.0","id":3,"method":"fs/read_text_file","params":{}}`,
		// session/cancel is a notification: no response expected.
		`{"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":"sess"}}`,
		// Malformed JSON -> invalid request error (no id).
		`this is not json`,
	})
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d: %v", len(msgs), msgs)
	}

	// session/new is followed by the slash-command advertisement notification.
	if m, _ := msgs[2]["method"].(string); m != "session/update" {
		t.Fatalf("expected advertisement notification at msgs[2], got %v", msgs[2])
	}

	errObj, ok := msgs[3]["error"].(map[string]any)
	if !ok {
		t.Fatalf("unknown method: expected error, got %v", msgs[3])
	}
	if code, _ := errObj["code"].(float64); code != acpErrMethodNotFound {
		t.Errorf("expected method not found code %d, got %v", acpErrMethodNotFound, errObj["code"])
	}

	// Cancel notification produced no message; the malformed line produced
	// an invalid request error.
	errObj, ok = msgs[4]["error"].(map[string]any)
	if !ok {
		t.Fatalf("malformed json: expected error, got %v", msgs[4])
	}
	if code, _ := errObj["code"].(float64); code != acpErrInvalidRequest {
		t.Errorf("expected invalid request code %d, got %v", acpErrInvalidRequest, errObj["code"])
	}
}

// TestACPSessionLoad seeds a persisted session on disk, then resumes it via
// session/load: history must be replayed as session/update notifications
// (user_message_chunk / agent_message_chunk / tool_call / tool_call_update)
// followed by an available_commands_update and the stopReason response.
func TestACPSessionLoad(t *testing.T) {
	tmpDir := t.TempDir()

	// Seed a second session next to the startup one (the SessionManager in
	// RunACP is rooted at filepath.Dir(sessionPath)).
	mgr := session.NewSessionManager(filepath.Dir(tmpDir))
	sess, err := mgr.CreateSession("acp-load-test", "acp-load-test")
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	seed := []agentctx.AgentMessage{
		agentctx.NewUserMessage("list the files"),
		agentctx.NewAssistantMessage(),
	}
	seed[1].Content = []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "sure, checking"},
		agentctx.ToolCallContent{ID: "call-1", Type: "toolCall", Name: "bash", Arguments: map[string]any{"command": "ls"}},
	}
	for _, m := range seed {
		if _, err := sess.AppendMessage(m); err != nil {
			t.Fatalf("append seed message: %v", err)
		}
	}
	if _, err := sess.AppendMessage(agentctx.NewToolResultMessage("call-1", "bash", []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "file1.txt"},
	}, false)); err != nil {
		t.Fatalf("append seed tool result: %v", err)
	}

	msgs := runACPSmoke(t, tmpDir, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		// Unknown id must be rejected.
		`{"jsonrpc":"2.0","id":2,"method":"session/load","params":{"sessionId":"bogus"}}`,
		// Empty sessionId must be rejected.
		`{"jsonrpc":"2.0","id":3,"method":"session/load","params":{}}`,
		// Resume the seeded session.
		fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"session/load","params":{"sessionId":%q}}`, sess.GetID()),
	})
	if len(msgs) != 9 {
		t.Fatalf("expected 9 messages, got %d: %+v", len(msgs), msgs)
	}

	// bogus / empty session ids -> invalid params errors
	for i, wantID := range []float64{2, 3} {
		m := msgs[i+1]
		errObj, ok := m["error"].(map[string]any)
		if !ok {
			t.Fatalf("message %d: expected error, got %v", i+1, m)
		}
		if code, _ := errObj["code"].(float64); code != acpErrInvalidParams {
			t.Errorf("message %d: expected error code %d, got %v", i+1, acpErrInvalidParams, errObj)
		}
		if id, _ := m["id"].(float64); id != wantID {
			t.Errorf("message %d: expected id %v, got %v", i+1, wantID, m["id"])
		}
	}

	// Replay of the seeded history, in order:
	// user_message_chunk, agent_message_chunk, tool_call(pending),
	// tool_call_update(completed), available_commands_update.
	wantUpdates := []string{
		"user_message_chunk",
		"agent_message_chunk",
		"tool_call",
		"tool_call_update",
		"available_commands_update",
	}
	for i, want := range wantUpdates {
		m := msgs[3+i]
		params, ok := m["params"].(map[string]any)
		if !ok {
			t.Fatalf("update %d: expected params object, got %v", i, m)
		}
		if sid, _ := params["sessionId"].(string); sid != sess.GetID() {
			t.Errorf("update %d: expected sessionId %q, got %q", i, sess.GetID(), sid)
		}
		upd, _ := params["update"].(map[string]any)
		if got, _ := upd["sessionUpdate"].(string); got != want {
			t.Errorf("update %d: expected %q, got %q", i, want, upd)
		}
	}

	// tool_call carries id/title/kind/status
	toolCall := msgs[5]["params"].(map[string]any)["update"].(map[string]any)
	if tcID, _ := toolCall["toolCallId"].(string); tcID != "call-1" {
		t.Errorf("tool_call: expected toolCallId call-1, got %v", toolCall)
	}
	if kind, _ := toolCall["kind"].(string); kind != "execute" {
		t.Errorf("tool_call: expected kind execute (bash), got %v", kind)
	}
	if status, _ := toolCall["status"].(string); status != "pending" {
		t.Errorf("tool_call: expected pending, got %v", status)
	}

	// tool_call_update carries the result content
	toolUpd := msgs[6]["params"].(map[string]any)["update"].(map[string]any)
	if status, _ := toolUpd["status"].(string); status != "completed" {
		t.Errorf("tool_call_update: expected completed, got %v", toolUpd)
	}
	content, _ := toolUpd["content"].([]any)
	if len(content) == 0 {
		t.Errorf("tool_call_update: expected result content, got %v", toolUpd)
	}

	// Final response: stopReason end_turn
	result, ok := msgs[8]["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected session/load result, got %v", msgs[8])
	}
	if stop, _ := result["stopReason"].(string); stop != "end_turn" {
		t.Errorf("expected stopReason end_turn, got %v", result)
	}
}

func TestBuildACPMessage(t *testing.T) {
	msg := buildACPMessage([]acpContentBlock{
		{Type: "text", Text: "Analyze this:"},
		{Type: "resource", Resource: &acpEmbeddedResource{URI: "file:///tmp/a.go", Text: "package main"}},
		{Type: "resource_link", URI: "file:///tmp/b.go"},
	})
	for _, want := range []string{"Analyze this:", "=== file:///tmp/a.go ===", "package main", "[file: /tmp/b.go]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("buildACPMessage output missing %q:\n%s", want, msg)
		}
	}
}

// runACPSmokeSession runs initialize + session/new against a live server,
// extracts the allocated sessionId, feeds additionalLines(sessionID), and
// returns every message emitted by the server.
func runACPSmokeSession(t *testing.T, tmpDir string, additionalLines func(sessionID string) []string) []map[string]any {
	t.Helper()

	t.Setenv("ZAI_API_KEY", "test-key")
	configPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("AI_CONFIG_PATH", configPath)

	reader, writer := io.Pipe()
	outReader, outWriter := io.Pipe()

	gotSession := make(chan string, 1)
	respCh := make(chan []map[string]any, 1)

	go func() { // reader
		var results []map[string]any
		scanner := bufio.NewScanner(outReader)
		scanner.Buffer(make([]byte, 0, 4*1024*1024), 16*1024*1024)
		for scanner.Scan() {
			var m map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
				continue
			}
			results = append(results, m)
			if len(results) == 2 { // initialize result + session/new result
				r, _ := results[1]["result"].(map[string]any)
				sid, _ := r["sessionId"].(string)
				gotSession <- sid
			}
		}
		respCh <- results
	}()

	go func() { // writer
		writer.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"))
		writer.Write([]byte(`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}` + "\n"))
		sid := <-gotSession
		for _, l := range additionalLines(sid) {
			writer.Write([]byte(l + "\n"))
		}
		writer.Close()
	}()

	_ = RunACP(tmpDir, "", reader, outWriter, "", 0, 5*time.Second, "", "", "acp-smoke")
	outWriter.Close()

	return <-respCh
}

// TestACPSlashCommandAdvertisement checks shape and contents of the
// available_commands_update notification emitted after session/new.
func TestACPSlashCommandAdvertisement(t *testing.T) {
	compiler := newACPSchemaCompiler(t)
	msgs := runACPSmoke(t, t.TempDir(), []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`,
	})
	if len(msgs) < 3 {
		t.Fatalf("expected >=3 messages, got %d: %v", len(msgs), msgs)
	}

	raw, err := json.Marshal(msgs[2])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assertACPMessageValid(t, compiler, raw)

	params, _ := msgs[2]["params"].(map[string]any)
	upd, _ := params["update"].(map[string]any)
	if got, _ := upd["sessionUpdate"].(string); got != "available_commands_update" {
		t.Fatalf("expected available_commands_update, got %v", upd)
	}
	cmds, _ := upd["commands"].([]any)
	if len(cmds) == 0 {
		t.Fatal("expected advertised commands, got none")
	}

	var names []string
	for _, c := range cmds {
		cm, _ := c.(map[string]any)
		name, _ := cm["name"].(string)
		desc, _ := cm["description"].(string)
		if name == "" || desc == "" {
			t.Errorf("advertised command missing name/description: %v", cm)
		}
		names = append(names, name)
	}
	// Built-in registry commands must be present.
	var hasHelp bool
	for _, n := range names {
		if n == "help" {
			hasHelp = true
		}
		if strings.HasPrefix(n, "/") {
			t.Errorf("advertised name %q must not start with '/' (clients render it)", n)
		}
	}
	if !hasHelp {
		t.Errorf("expected built-in 'help' among advertised commands, got %v", names)
	}
}

// TestACPSlashHelpOverACP runs "/help" through a live server: a registered
// command must answer synchronously with one agent_message_chunk followed by
// stopReason end_turn, without any LLM call or hang.
func TestACPSlashHelpOverACP(t *testing.T) {
	msgs := runACPSmokeSession(t, t.TempDir(), func(sessionID string) []string {
		return []string{fmt.Sprintf(
			`{"jsonrpc":"2.0","id":3,"method":"session/prompt","params":{"sessionId":%q,"prompt":[{"type":"text","text":"/help"}]}}`,
			sessionID)}
	})
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages (init, new, advertisement, chunk, response), got %d: %v", len(msgs), msgs)
	}

	chunk := msgs[3]
	if _, hasID := chunk["id"]; hasID {
		t.Errorf("agent_message_chunk must be a notification, got id field: %v", chunk["id"])
	}
	if m, _ := chunk["method"].(string); m != "session/update" {
		t.Fatalf("expected session/update chunk, got %q", m)
	}
	params, _ := chunk["params"].(map[string]any)
	upd, _ := params["update"].(map[string]any)
	if got, _ := upd["sessionUpdate"].(string); got != "agent_message_chunk" {
		t.Fatalf("expected agent_message_chunk, got %v", upd)
	}
	content, _ := upd["content"].(map[string]any)
	text, _ := content["text"].(string)
	if !strings.Contains(text, "commands") && !strings.Contains(text, "Available") {
		t.Errorf("/help output should mention commands, got: %s", text)
	}

	result, ok := msgs[4]["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected prompt response result, got %v", msgs[4])
	}
	stop, _ := result["stopReason"].(string)
	if stop != acpStopEndTurn {
		t.Errorf("expected stopReason %q, got %v", acpStopEndTurn, result)
	}
}

// TestMatchACPCommand covers the allow-list gate: only registered commands
// dispatch; unregistered '/', comments and plain text stay raw; skill prompts
// are excluded from sync dispatch (they need the expansion path).
func TestMatchACPCommand(t *testing.T) {
	srv := NewServer()
	srv.RegisterSlash("help", "List commands", func(args string) (any, error) { return nil, nil })

	cases := []struct {
		msg     string
		wantOK  bool
		wantCmd string
	}{
		{msg: "/help x", wantOK: true, wantCmd: "help"},
		{msg: "/help", wantOK: true, wantCmd: "help"},
		{msg: "   /help   ", wantOK: true, wantCmd: "help"},
		{msg: "// this is not a command", wantOK: false},
		{msg: "/", wantOK: false},
		{msg: "/nosuchcmd hi", wantOK: false},
		{msg: "/skill:code-review fix bugs", wantOK: false}, // excluded on purpose
		{msg: "plain text question", wantOK: false},
	}
	for _, c := range cases {
		name, _, ok := matchACPCommand(srv, c.msg)
		if ok != c.wantOK || name != c.wantCmd {
			t.Errorf("matchACPCommand(%q) = (%q,%v), want (%q,%v)", c.msg, name, ok, c.wantCmd, c.wantOK)
		}
	}
}
