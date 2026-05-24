package adapter

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/rpc"
)

// newTestRPCConn creates an RPCConn wired to os.Pipe pairs instead of a real
// subprocess. It returns:
//   - conn: the RPCConn under test
//   - serverWrite: write end that simulates subprocess stdout (test writes responses here)
//   - serverRead:  read end that receives what the RPCConn sends to "stdin" (optional to consume)
//
// The readLoop is started automatically and alive is set to true.
func newTestRPCConn() (*RPCConn, *os.File, *os.File) {
	// Pipe for conn.stdout: readEnd → readLoop, writeEnd → test injects responses.
	stdoutReadEnd, stdoutWriteEnd, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	// Pipe for conn.stdin: writeEnd → Prompt writes here, readEnd → test reads commands.
	stdinReadEnd, stdinWriteEnd, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	_, cancel := context.WithCancel(context.Background())
	conn := &RPCConn{
		stdin:    stdinWriteEnd,
		stdout:   stdoutReadEnd,
		cancel:   cancel,
		done:     make(chan struct{}),
		pending:  make(map[string]chan *rpcResponseOrEvent),
		eventsCh: make(chan json.RawMessage, 256),
	}
	conn.alive.Store(true)
	go conn.readLoop()

	return conn, stdoutWriteEnd, stdinReadEnd
}

// closeTestConn closes the pipe ends that the test controls so the readLoop exits.
func closeTestConn(serverWrite *os.File, serverRead *os.File) {
	if serverWrite != nil {
		serverWrite.Close()
	}
	if serverRead != nil {
		serverRead.Close()
	}
}

// writeLine writes a JSON-encoded line to the pipe (simulating subprocess stdout).
func writeLine(w io.Writer, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	w.Write(append(data, '\n'))
}

// --- Helpers to build protocol messages ---

func makeResponse(id string, success bool, errMsg string) map[string]any {
	m := map[string]any{
		"id":      id,
		"type":    "response",
		"command": "prompt",
		"success": success,
	}
	if errMsg != "" {
		m["error"] = errMsg
	}
	return m
}

func makeTurnEndEvent(text string) map[string]any {
	return map[string]any{
		"type": "turn_end",
		"message": map[string]any{
			"role": "assistant",
			"content": []any{
				map[string]any{
					"type": "text",
					"text": text,
				},
			},
		},
	}
}

func makeAgentEndEvent() map[string]any {
	return map[string]any{
		"type":     "agent_end",
		"messages": []any{},
	}
}

func makeErrorEvent(errMsg string) map[string]any {
	return map[string]any{
		"type":  "error",
		"error": errMsg,
	}
}

// --- Tests ---

// TestRPCConnReadLoopDispatch verifies that a response with an ID is dispatched
// to the registered pending channel.
func TestRPCConnReadLoopDispatch(t *testing.T) {
	conn, serverWrite, serverRead := newTestRPCConn()
	defer closeTestConn(serverWrite, serverRead)

	// Register a pending request.
	const testID = "req-dispatch-123"
	ch := conn.registerPending(testID)

	// Write a response with that ID.
	writeLine(serverWrite, makeResponse(testID, true, ""))

	// The channel should receive the response.
	select {
	case msg := <-ch:
		if msg.resp == nil {
			t.Fatal("expected non-nil resp")
		}
		if msg.resp.ID != testID {
			t.Fatalf("expected ID %q, got %q", testID, msg.resp.ID)
		}
		if !msg.resp.Success {
			t.Fatal("expected success=true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatch")
	}
}

// TestRPCConnEventDispatch verifies that a line without an ID (or non-response type)
// is forwarded to eventsCh.
func TestRPCConnEventDispatch(t *testing.T) {
	conn, serverWrite, serverRead := newTestRPCConn()
	defer closeTestConn(serverWrite, serverRead)

	// Write an event (no ID, not type "response").
	writeLine(serverWrite, makeTurnEndEvent("hello"))

	select {
	case raw := <-conn.eventsCh:
		var peek struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &peek); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		if peek.Type != "turn_end" {
			t.Fatalf("expected type turn_end, got %q", peek.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event on eventsCh")
	}
}

// TestRPCConnPromptSuccess tests the full Prompt cycle:
// send prompt → receive success response → turn_end → agent_end.
func TestRPCConnPromptSuccess(t *testing.T) {
	conn, serverWrite, serverRead := newTestRPCConn()
	defer closeTestConn(serverWrite, serverRead)

	// We need to consume what Prompt sends on stdin so the pipe doesn't block.
	go func() {
		scanner := bufio.NewScanner(serverRead)
		for scanner.Scan() {
			// Discard — just drain the pipe.
		}
	}()

	// Background goroutine to simulate the subprocess responding.
	go func() {
		// Give Prompt a moment to start and write its command.
		time.Sleep(50 * time.Millisecond)

		// Read the command that Prompt sent so we know the ID.
		// Actually we can just watch pending — but the simplest way is to
		// peek at the stdin pipe. Since we have a separate goroutine draining it,
		// let's use a different approach: read the command directly.

		// Alternative: use a buffered reader on serverRead to capture the command.
		// For simplicity, we'll write the response using a known pattern.
		// The Prompt method generates ID as "req-<nanos>", so let's peek at pending.
	}()

	// Simpler approach: intercept the command via a custom stdin reader.
	// Let's redo with a helper that captures the request ID.
	{
		// Close the default pipes and create fresh ones with interception.
		serverWrite.Close()
		serverRead.Close()
		<-conn.done // wait for readLoop to exit

		// Create fresh pipes.
		stdoutRead, stdoutWrite, _ := os.Pipe()
		stdinRead, stdinWrite, _ := os.Pipe()

		_, cancel := context.WithCancel(context.Background())
		conn = &RPCConn{
			stdin:    stdinWrite,
			stdout:   stdoutRead,
			cancel:   cancel,
			done:     make(chan struct{}),
			pending:  make(map[string]chan *rpcResponseOrEvent),
			eventsCh: make(chan json.RawMessage, 256),
		}
		conn.alive.Store(true)
		go conn.readLoop()

		defer stdoutWrite.Close()
		defer stdinRead.Close()

		// Read the command Prompt will send.
		cmdCh := make(chan rpc.RPCCommand, 1)
		go func() {
			scanner := bufio.NewScanner(stdinRead)
			if scanner.Scan() {
				var cmd rpc.RPCCommand
				json.Unmarshal(scanner.Bytes(), &cmd)
				cmdCh <- cmd
			}
		}()

		// Run Prompt in a goroutine.
		promptCtx, promptCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer promptCancel()

		resultCh := make(chan promptResult, 1)
		go func() {
			text, err := conn.Prompt(promptCtx, "hello agent")
			resultCh <- promptResult{text: text, err: err}
		}()

		// Wait for the command to arrive so we know the request ID.
		var reqCmd rpc.RPCCommand
		select {
		case reqCmd = <-cmdCh:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for Prompt to send command")
		}

		if reqCmd.Type != rpc.CommandPrompt {
			t.Fatalf("expected command type %q, got %q", rpc.CommandPrompt, reqCmd.Type)
		}

		// Send success response.
		writeLine(stdoutWrite, makeResponse(reqCmd.ID, true, ""))

		// Send turn_end with text.
		writeLine(stdoutWrite, makeTurnEndEvent("Hello world"))

		// Send agent_end.
		writeLine(stdoutWrite, makeAgentEndEvent())

		// Wait for Prompt to return.
		select {
		case res := <-resultCh:
			if res.err != nil {
				t.Fatalf("Prompt returned error: %v", res.err)
			}
			if res.text != "Hello world" {
				t.Fatalf("expected text %q, got %q", "Hello world", res.text)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for Prompt to return")
		}
	}
}

type promptResult struct {
	text string
	err  error
}

// TestRPCConnPromptErrorResponse tests that a non-success response returns an error.
func TestRPCConnPromptErrorResponse(t *testing.T) {
	stdoutRead, stdoutWrite, _ := os.Pipe()
	stdinRead, stdinWrite, _ := os.Pipe()

	_, cancel := context.WithCancel(context.Background())
	conn := &RPCConn{
		stdin:    stdinWrite,
		stdout:   stdoutRead,
		cancel:   cancel,
		done:     make(chan struct{}),
		pending:  make(map[string]chan *rpcResponseOrEvent),
		eventsCh: make(chan json.RawMessage, 256),
	}
	conn.alive.Store(true)
	go conn.readLoop()

	defer stdoutWrite.Close()
	defer stdinRead.Close()

	// Capture the command ID.
	cmdCh := make(chan rpc.RPCCommand, 1)
	go func() {
		scanner := bufio.NewScanner(stdinRead)
		if scanner.Scan() {
			var cmd rpc.RPCCommand
			json.Unmarshal(scanner.Bytes(), &cmd)
			cmdCh <- cmd
		}
	}()

	promptCtx, promptCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer promptCancel()

	resultCh := make(chan promptResult, 1)
	go func() {
		text, err := conn.Prompt(promptCtx, "fail please")
		resultCh <- promptResult{text: text, err: err}
	}()

	// Get the request ID.
	var reqCmd rpc.RPCCommand
	select {
	case reqCmd = <-cmdCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Prompt command")
	}

	// Send a failure response.
	writeLine(stdoutWrite, makeResponse(reqCmd.ID, false, "prompt rejected: bad input"))

	select {
	case res := <-resultCh:
		if res.err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(res.err.Error(), "prompt rejected") {
			t.Fatalf("expected error containing 'prompt rejected', got: %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Prompt to return")
	}
}

// TestRPCConnContextCancel tests that cancelling the context while Prompt is
// waiting causes Prompt to return the context error.
func TestRPCConnContextCancel(t *testing.T) {
	stdoutRead, stdoutWrite, _ := os.Pipe()
	stdinRead, stdinWrite, _ := os.Pipe()

	_, cancel := context.WithCancel(context.Background())
	conn := &RPCConn{
		stdin:    stdinWrite,
		stdout:   stdoutRead,
		cancel:   cancel,
		done:     make(chan struct{}),
		pending:  make(map[string]chan *rpcResponseOrEvent),
		eventsCh: make(chan json.RawMessage, 256),
	}
	conn.alive.Store(true)
	go conn.readLoop()

	defer stdoutWrite.Close()
	defer stdinRead.Close()

	// Drain stdin commands.
	go func() {
		io.Copy(io.Discard, stdinRead)
	}()

	promptCtx, promptCancel := context.WithCancel(context.Background())

	resultCh := make(chan promptResult, 1)
	go func() {
		text, err := conn.Prompt(promptCtx, "will be cancelled")
		resultCh <- promptResult{text: text, err: err}
	}()

	// Give Prompt time to register pending and start waiting.
	time.Sleep(100 * time.Millisecond)

	// Cancel the context.
	promptCancel()

	select {
	case res := <-resultCh:
		if res.err == nil {
			t.Fatal("expected error from cancelled context, got nil")
		}
		if res.err != context.Canceled {
			t.Fatalf("expected context.Canceled, got: %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Prompt to return after cancel")
	}
}

// TestRPCConnEOF verifies that closing the stdout pipe causes readLoop to exit
// and IsAlive to become false.
func TestRPCConnEOF(t *testing.T) {
	stdoutRead, stdoutWrite, _ := os.Pipe()
	stdinRead, stdinWrite, _ := os.Pipe()

	_, cancel := context.WithCancel(context.Background())
	conn := &RPCConn{
		stdin:    stdinWrite,
		stdout:   stdoutRead,
		cancel:   cancel,
		done:     make(chan struct{}),
		pending:  make(map[string]chan *rpcResponseOrEvent),
		eventsCh: make(chan json.RawMessage, 256),
	}
	conn.alive.Store(true)
	go conn.readLoop()

	defer stdinRead.Close()

	if !conn.IsAlive() {
		t.Fatal("expected conn to be alive initially")
	}

	// Close the write end — readLoop should see EOF and exit.
	stdoutWrite.Close()

	select {
	case <-conn.done:
		// readLoop exited.
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for readLoop to exit")
	}

	if conn.IsAlive() {
		t.Fatal("expected IsAlive=false after EOF")
	}
}

// TestRPCConnPromptAgentError tests that an error event during the event phase
// causes Prompt to return an error.
func TestRPCConnPromptAgentError(t *testing.T) {
	stdoutRead, stdoutWrite, _ := os.Pipe()
	stdinRead, stdinWrite, _ := os.Pipe()

	_, cancel := context.WithCancel(context.Background())
	conn := &RPCConn{
		stdin:    stdinWrite,
		stdout:   stdoutRead,
		cancel:   cancel,
		done:     make(chan struct{}),
		pending:  make(map[string]chan *rpcResponseOrEvent),
		eventsCh: make(chan json.RawMessage, 256),
	}
	conn.alive.Store(true)
	go conn.readLoop()

	defer stdoutWrite.Close()
	defer stdinRead.Close()

	// Capture command ID.
	cmdCh := make(chan rpc.RPCCommand, 1)
	go func() {
		scanner := bufio.NewScanner(stdinRead)
		if scanner.Scan() {
			var cmd rpc.RPCCommand
			json.Unmarshal(scanner.Bytes(), &cmd)
			cmdCh <- cmd
		}
	}()

	promptCtx, promptCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer promptCancel()

	resultCh := make(chan promptResult, 1)
	go func() {
		text, err := conn.Prompt(promptCtx, "trigger error")
		resultCh <- promptResult{text: text, err: err}
	}()

	var reqCmd rpc.RPCCommand
	select {
	case reqCmd = <-cmdCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Prompt command")
	}

	// Send success response, then an error event.
	writeLine(stdoutWrite, makeResponse(reqCmd.ID, true, ""))
	writeLine(stdoutWrite, makeErrorEvent("model rate limit exceeded"))

	select {
	case res := <-resultCh:
		if res.err == nil {
			t.Fatal("expected error from agent error event")
		}
		if !strings.Contains(res.err.Error(), "rate limit") {
			t.Fatalf("expected error containing 'rate limit', got: %v", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Prompt to return")
	}
}

// TestRPCConnMultipleTurnEnds tests that multiple turn_end events are
// concatenated with newlines.
func TestRPCConnMultipleTurnEnds(t *testing.T) {
	stdoutRead, stdoutWrite, _ := os.Pipe()
	stdinRead, stdinWrite, _ := os.Pipe()

	_, cancel := context.WithCancel(context.Background())
	conn := &RPCConn{
		stdin:    stdinWrite,
		stdout:   stdoutRead,
		cancel:   cancel,
		done:     make(chan struct{}),
		pending:  make(map[string]chan *rpcResponseOrEvent),
		eventsCh: make(chan json.RawMessage, 256),
	}
	conn.alive.Store(true)
	go conn.readLoop()

	defer stdoutWrite.Close()
	defer stdinRead.Close()

	cmdCh := make(chan rpc.RPCCommand, 1)
	go func() {
		scanner := bufio.NewScanner(stdinRead)
		if scanner.Scan() {
			var cmd rpc.RPCCommand
			json.Unmarshal(scanner.Bytes(), &cmd)
			cmdCh <- cmd
		}
	}()

	promptCtx, promptCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer promptCancel()

	resultCh := make(chan promptResult, 1)
	go func() {
		text, err := conn.Prompt(promptCtx, "multi-turn")
		resultCh <- promptResult{text: text, err: err}
	}()

	var reqCmd rpc.RPCCommand
	select {
	case reqCmd = <-cmdCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Prompt command")
	}

	writeLine(stdoutWrite, makeResponse(reqCmd.ID, true, ""))
	writeLine(stdoutWrite, makeTurnEndEvent("First part"))
	writeLine(stdoutWrite, makeTurnEndEvent("Second part"))
	writeLine(stdoutWrite, makeAgentEndEvent())

	select {
	case res := <-resultCh:
		if res.err != nil {
			t.Fatalf("unexpected error: %v", res.err)
		}
		expected := "First part\nSecond part"
		if res.text != expected {
			t.Fatalf("expected %q, got %q", expected, res.text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Prompt to return")
	}
}

// TestSafeSessionKey verifies that unsafe characters are replaced.
func TestSafeSessionKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello/world", "hello-world"},
		{"hello:world", "hello-world"},
		{"a/b:c", "a-b-c"},
		{"plain", "plain"},
		{"/leading", "-leading"},
		{"trailing/", "trailing-"},
		{"multi///slash", "multi---slash"},
		{"", ""},
	}

	for _, tt := range tests {
		got := safeSessionKey(tt.input)
		if got != tt.expected {
			t.Errorf("safeSessionKey(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// TestRPCConnResponseWithNoPending tests that a response for a non-existent
// pending ID is safely ignored (no panic, no deadlock).
func TestRPCConnResponseWithNoPending(t *testing.T) {
	conn, serverWrite, serverRead := newTestRPCConn()
	defer closeTestConn(serverWrite, serverRead)

	// Write a response for an ID that was never registered.
	writeLine(serverWrite, makeResponse("orphan-id", true, ""))

	// Give readLoop time to process.
	time.Sleep(100 * time.Millisecond)

	// Connection should still be alive.
	if !conn.IsAlive() {
		t.Fatal("expected conn to still be alive after orphan response")
	}
}

// TestRPCConnUnparseableLine tests that an invalid JSON line is skipped.
func TestRPCConnUnparseableLine(t *testing.T) {
	conn, serverWrite, serverRead := newTestRPCConn()
	defer closeTestConn(serverWrite, serverRead)

	// Write garbage.
	serverWrite.Write([]byte("not json at all\n"))

	// Write a valid event after.
	writeLine(serverWrite, makeTurnEndEvent("after garbage"))

	select {
	case raw := <-conn.eventsCh:
		var peek struct {
			Type string `json:"type"`
		}
		json.Unmarshal(raw, &peek)
		if peek.Type != "turn_end" {
			t.Fatalf("expected turn_end after garbage, got %q", peek.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out — valid event after garbage not received")
	}
}

// TestExtractTextFromTurnEnd tests the helper function directly.
func TestExtractTextFromTurnEnd(t *testing.T) {
	msg := &agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "Hello "},
			agentctx.TextContent{Type: "text", Text: "World"},
		},
	}
	evt := agent.AgentEvent{
		Type:    agent.EventTurnEnd,
		Message: msg,
	}
	raw, _ := json.Marshal(evt)

	got := extractTextFromTurnEnd(raw)
	if got != "Hello World" {
		t.Fatalf("expected %q, got %q", "Hello World", got)
	}
}

// TestExtractTextFromTurnEnd_NilMessage tests that nil message returns empty.
func TestExtractTextFromTurnEnd_NilMessage(t *testing.T) {
	evt := agent.AgentEvent{
		Type:    agent.EventTurnEnd,
		Message: nil,
	}
	raw, _ := json.Marshal(evt)
	got := extractTextFromTurnEnd(raw)
	if got != "" {
		t.Fatalf("expected empty string for nil message, got %q", got)
	}
}

// TestExtractErrorFromEvent tests the error extraction helper.
func TestExtractErrorFromEvent(t *testing.T) {
	evt := agent.AgentEvent{
		Type:  agent.EventError,
		Error: "something broke",
	}
	raw, _ := json.Marshal(evt)
	got := extractErrorFromEvent(raw)
	if got != "something broke" {
		t.Fatalf("expected %q, got %q", "something broke", got)
	}
}

// TestParseEventType tests the event type parser.
func TestParseEventType(t *testing.T) {
	tests := []struct {
		json     string
		expected string
		hasError bool
	}{
		{`{"type":"turn_end"}`, "turn_end", false},
		{`{"type":"agent_end"}`, "agent_end", false},
		{`{"type":"error"}`, "error", false},
		{`{}`, "", false},
		{`not json`, "", true},
	}

	for _, tt := range tests {
		got, err := parseEventType(json.RawMessage(tt.json))
		if tt.hasError && err == nil {
			t.Errorf("parseEventType(%q): expected error, got nil", tt.json)
		}
		if !tt.hasError && err != nil {
			t.Errorf("parseEventType(%q): unexpected error: %v", tt.json, err)
		}
		if got != tt.expected {
			t.Errorf("parseEventType(%q) = %q, want %q", tt.json, got, tt.expected)
		}
	}
}

// TestRPCConnEventsChFull tests that events are dropped when eventsCh is full.
func TestRPCConnEventsChFull(t *testing.T) {
	stdoutRead, stdoutWrite, _ := os.Pipe()
	stdinRead, stdinWrite, _ := os.Pipe()

	_, cancel := context.WithCancel(context.Background())
	conn := &RPCConn{
		stdin:    stdinWrite,
		stdout:   stdoutRead,
		cancel:   cancel,
		done:     make(chan struct{}),
		pending:  make(map[string]chan *rpcResponseOrEvent),
		eventsCh: make(chan json.RawMessage, 2), // very small buffer
	}
	conn.alive.Store(true)
	go conn.readLoop()

	defer stdoutWrite.Close()
	defer stdinRead.Close()

	// Fill the channel.
	for i := 0; i < 2; i++ {
		writeLine(stdoutWrite, makeTurnEndEvent(fmt.Sprintf("filler-%d", i)))
	}

	// Give readLoop time to process.
	time.Sleep(100 * time.Millisecond)

	// Write one more — should be dropped (no deadlock).
	writeLine(stdoutWrite, makeTurnEndEvent("overflow"))

	// Connection should still be alive.
	time.Sleep(100 * time.Millisecond)
	if !conn.IsAlive() {
		t.Fatal("expected conn alive after eventsCh full")
	}
}

// TestRPCConnPendingCleanup tests that unregisterPending removes the channel.
func TestRPCConnPendingCleanup(t *testing.T) {
	conn, serverWrite, serverRead := newTestRPCConn()
	defer closeTestConn(serverWrite, serverRead)

	const id = "cleanup-test"
	ch := conn.registerPending(id)

	conn.mu.Lock()
	_, ok := conn.pending[id]
	conn.mu.Unlock()
	if !ok {
		t.Fatal("expected pending entry after register")
	}

	conn.unregisterPending(id)

	// The channel should still be valid (closed by GC later).
	select {
	case <-ch:
		// Channel is open but nobody will send — this is fine.
	default:
	}

	conn.mu.Lock()
	_, ok = conn.pending[id]
	conn.mu.Unlock()
	if ok {
		t.Fatal("expected pending entry to be removed after unregister")
	}
}

// TestRPCConnSendCommandNotAlive tests that sendCommand fails when conn is not alive.
func TestRPCConnSendCommandNotAlive(t *testing.T) {
	conn, serverWrite, serverRead := newTestRPCConn()
	defer closeTestConn(serverWrite, serverRead)

	conn.alive.Store(false)
	err := conn.sendCommand(map[string]string{"type": "quit"})
	if err == nil {
		t.Fatal("expected error when sending command on dead conn")
	}
	if !strings.Contains(err.Error(), "not alive") {
		t.Fatalf("expected 'not alive' error, got: %v", err)
	}
}

// TestRPCConnConcurrentDispatch tests that concurrent response dispatches
// don't cause data races.
func TestRPCConnConcurrentDispatch(t *testing.T) {
	conn, serverWrite, serverRead := newTestRPCConn()
	defer closeTestConn(serverWrite, serverRead)

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)

	channels := make([]<-chan *rpcResponseOrEvent, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("concurrent-%d", i)
		channels[i] = conn.registerPending(id)
	}

	// Send all responses.
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			writeLine(serverWrite, makeResponse(fmt.Sprintf("concurrent-%d", idx), true, ""))
		}(i)
	}
	wg.Wait()

	// Verify all channels receive their response.
	for i := 0; i < n; i++ {
		select {
		case msg := <-channels[i]:
			if msg.resp == nil {
				t.Fatalf("channel %d: expected non-nil resp", i)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("channel %d: timed out", i)
		}
	}
}
