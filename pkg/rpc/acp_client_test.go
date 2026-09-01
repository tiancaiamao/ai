package rpc

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/transport"
)

func TestACPClientInitializeTimesOut(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	client := NewACPClient(transport.NewNetConn(clientConn))
	defer client.Close()

	go func() {
		serverConn := transport.NewNetConn(server)
		_, _ = serverConn.ReadMessage()
	}()

	start := time.Now()
	err := client.initializeWithTimeout(20 * time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for initialize response") {
		t.Fatalf("initialize error = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("initialize timeout took too long: %v", elapsed)
	}
}

func TestACPClientLoadSessionSignalsReplayCompletionAfterUpdates(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	client := NewACPClient(transport.NewNetConn(clientConn))
	defer client.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn := transport.NewNetConn(server)
		request, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(request, &envelope) != nil {
			return
		}
		if err := conn.WriteMessage([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"replayed"}}}}`)); err != nil {
			return
		}
		response := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{}}`, envelope.ID)
		_ = conn.WriteMessage([]byte(response))
	}()

	if err := client.LoadSession("sess"); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	updates := client.Updates()
	select {
	case update := <-updates:
		if update.SessionUpdate != "agent_message_chunk" {
			t.Fatalf("expected replay update first, got %q", update.SessionUpdate)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replay update")
	}
	select {
	case update := <-updates:
		if update.SessionUpdate != ACPUpdateSessionLoadEnd {
			t.Fatalf("expected replay completion marker, got %q", update.SessionUpdate)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replay completion")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server did not finish")
	}
}

func TestACPClientLoadSessionDoesNotSignalReplayCompletionOnError(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	client := NewACPClient(transport.NewNetConn(clientConn))
	defer client.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn := transport.NewNetConn(server)
		request, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(request, &envelope) != nil {
			return
		}
		response := fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"not found"}}`, envelope.ID)
		_ = conn.WriteMessage([]byte(response))
	}()

	if err := client.LoadSession("sess"); err == nil {
		t.Fatal("LoadSession unexpectedly succeeded")
	}

	select {
	case update := <-client.Updates():
		if update.SessionUpdate == ACPUpdateSessionLoadEnd {
			t.Fatal("session/load error emitted replay completion marker")
		}
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server did not finish")
	}
}

func TestACPClientPromptAsyncReportsServerError(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	client := NewACPClient(transport.NewNetConn(clientConn))
	defer client.Close()

	go func() {
		defer server.Close()
		conn := transport.NewNetConn(server)
		request, err := conn.ReadMessage()
		if err != nil {
			return
		}

		if !strings.Contains(string(request), `"method":"session/prompt"`) {
			return
		}
		_ = conn.WriteMessage([]byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32600,"message":"busy"}}`))
	}()

	if err := client.PromptAsync("sess", "hello"); err != nil {
		t.Fatalf("PromptAsync: %v", err)
	}

	select {
	case update := <-client.Updates():
		if update.SessionUpdate != ACPUpdateRequestError {
			t.Fatalf("update = %q, want %q", update.SessionUpdate, ACPUpdateRequestError)
		}
		err, ok := update.Meta.(ACPUpdateError)
		if !ok || err.Method != "session/prompt" || err.Code != -32600 || err.Message != "busy" {
			t.Fatalf("error metadata = %#v", update.Meta)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for async request error")
	}
}

func TestACPClientResponseNotBlockedByFullUpdatesChannel(t *testing.T) {
	server, clientConn := net.Pipe()
	defer server.Close()
	client := NewACPClient(transport.NewNetConn(clientConn))
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn := transport.NewNetConn(server)
		if _, err := conn.ReadMessage(); err != nil {
			return
		}
		for i := 0; i < 1100; i++ {
			_ = conn.WriteMessage([]byte(fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"%d"}}}}`, i)))
		}
		_ = conn.WriteMessage([]byte(`{"jsonrpc":"2.0","id":2,"result":{}}`))
	}()

	if err := client.LoadSession("sess"); err != nil {
		t.Fatalf("LoadSession blocked behind updates: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("server was blocked writing updates or response")
	}
}
