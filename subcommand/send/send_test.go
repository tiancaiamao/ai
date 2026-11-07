package send

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/protocol"
	"github.com/tiancaiamao/ai/pkg/transport"
)

// TestSendAndWaitTimeoutExits2 is the regression test for the 2026-07-17
// orchestrator deadlock: `send --wait` timed out but exited 0, so the
// orchestrating agent treated an incomplete (in-flight) turn as a successful
// completion. A timed-out wait must exit 2 (same convention as
// `ai watch --follow --timeout`).
func TestSendAndWaitTimeoutExits2(t *testing.T) {
	server, clientConn := net.Pipe()
	client := protocol.NewACPClient(transport.NewNetConn(clientConn))
	defer client.Close()

	keepOpen := make(chan struct{})
	defer close(keepOpen)

	go func() {
		defer server.Close()
		conn := transport.NewNetConn(server)
		// Ack the prompt but never emit _turn_end: the turn is still in flight.
		if req, err := conn.ReadMessage(); err == nil {
			if !strings.Contains(string(req), `"method":"session/prompt"`) {
				return
			}
			var env struct {
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(req, &env) != nil {
				return
			}
			_ = conn.WriteMessage([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{}}`, env.ID)))
		}
		// Keep the server pipe end open until the test ends: if this goroutine
		// exits here, the pipe closes and the client sees EOF (exit 1) before
		// the deadline can fire.
		<-keepOpen
	}()

	code := sendAndWait(client, "sess", "hello", true, 500*time.Millisecond, "testrun")
	if code != 2 {
		t.Fatalf("sendAndWait exit code = %d, want 2 (timeout)", code)
	}
}

// TestSendAndWaitTurnEndExits0 verifies the happy path still reports success.
func TestSendAndWaitTurnEndExits0(t *testing.T) {
	server, clientConn := net.Pipe()
	client := protocol.NewACPClient(transport.NewNetConn(clientConn))
	defer client.Close()

	go func() {
		defer server.Close()
		conn := transport.NewNetConn(server)
		req, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if !strings.Contains(string(req), `"method":"session/prompt"`) {
			return
		}
		var env struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(req, &env) != nil {
			return
		}
		_ = conn.WriteMessage([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess","update":{"sessionUpdate":"_turn_end"}}}`))
		_ = conn.WriteMessage([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{}}`, env.ID)))
	}()

	code := sendAndWait(client, "sess", "hello", true, 5*time.Second, "testrun")
	if code != 0 {
		t.Fatalf("sendAndWait exit code = %d, want 0 (turn completed)", code)
	}
}

// TestSendAndWaitStreamClosedExits1: the agent process died mid-turn.
func TestSendAndWaitStreamClosedExits1(t *testing.T) {
	server, clientConn := net.Pipe()
	client := protocol.NewACPClient(transport.NewNetConn(clientConn))
	defer client.Close()

	go func() {
		conn := transport.NewNetConn(server)
		if req, err := conn.ReadMessage(); err == nil {
			if strings.Contains(string(req), `"method":"session/prompt"`) {
				// Die without answering.
				_ = conn.Close()
				_ = server.Close()
				return
			}
		}
		_ = server.Close()
	}()

	code := sendAndWait(client, "sess", "hello", true, 5*time.Second, "testrun")
	if code != 1 {
		t.Fatalf("sendAndWait exit code = %d, want 1 (stream closed without turn end)", code)
	}
}
