package rpc

import (
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/transport"
)

func TestACPClientLoadSessionSignalsReplayCompletionAfterUpdates(t *testing.T) {
	input := bytes.NewBufferString(
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"replayed"}}}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"result":{}}` + "\n",
	)
	client := NewACPClient(transport.NewStdio(input, &bytes.Buffer{}))
	defer client.Close()

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
}

func TestACPClientLoadSessionDoesNotSignalReplayCompletionOnError(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"not found"}}` + "\n")
	client := NewACPClient(transport.NewStdio(input, &bytes.Buffer{}))
	defer client.Close()

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
