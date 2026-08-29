package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStdioRoundTrip(t *testing.T) {
	// In-memory input so the test is deterministic and never blocks on a pipe.
	in := strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"pong"}` + "\n")
	var out bytes.Buffer
	conn := NewStdio(in, &out)

	// WriteMessage frames with a trailing newline.
	msg := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err := conn.WriteMessage(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := out.String(); got != string(msg)+"\n" {
		t.Fatalf("output = %q, want %q", got, string(msg)+"\n")
	}

	// ReadMessage returns the next newline-delimited line.
	got, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != `{"jsonrpc":"2.0","id":2,"method":"pong"}` {
		t.Fatalf("read = %s, want pong", got)
	}

	// io.EOF once the input is drained.
	if _, err := conn.ReadMessage(); err != io.EOF {
		t.Fatalf("read after drain = %v, want io.EOF", err)
	}
}

func TestUnixSocketRoundTrip(t *testing.T) {
	sock, path := newTestSocket(t)
	defer sock.Close()

	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	serverConn, err := sock.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	defer serverConn.Close()

	// The client uses the same newline framing over its half of the connection
	// (a net.Conn is both reader and writer).
	clientConn := NewStdio(client, client)

	// client -> server.
	req := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err := clientConn.WriteMessage(req); err != nil {
		t.Fatalf("client write: %v", err)
	}
	got, err := serverConn.ReadMessage()
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	if string(got) != string(req) {
		t.Fatalf("server got %s, want %s", got, req)
	}

	// server -> client.
	resp := json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)
	if err := serverConn.WriteMessage(resp); err != nil {
		t.Fatalf("server write: %v", err)
	}
	gotResp, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(gotResp) != string(resp) {
		t.Fatalf("client got %s, want %s", gotResp, resp)
	}
}

func TestUnixSocketConcurrent(t *testing.T) {
	sock, path := newTestSocket(t)
	defer sock.Close()

	const n = 3
	type pair struct {
		client net.Conn
		server Conn
	}
	pairs := make([]pair, n)
	for i := range pairs {
		c, err := net.Dial("unix", path)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}
		sc, err := sock.Accept()
		if err != nil {
			t.Fatalf("accept %d: %v", i, err)
		}
		pairs[i] = pair{client: c, server: sc}
	}

	// Exercise every connection concurrently: each server sends a ping the
	// client reads back, then each client replies the server reads back.
	var wg sync.WaitGroup
	for i, p := range pairs {
		wg.Add(1)
		go func(i int, p pair) {
			defer wg.Done()
			client := NewStdio(p.client, p.client)

			req := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
			if err := p.server.WriteMessage(req); err != nil {
				t.Errorf("conn %d server write: %v", i, err)
				return
			}
			if got, err := client.ReadMessage(); err != nil {
				t.Errorf("conn %d client read: %v", i, err)
			} else if string(got) != string(req) {
				t.Errorf("conn %d client got %s", i, got)
			}

			resp := json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)
			if err := client.WriteMessage(resp); err != nil {
				t.Errorf("conn %d client write: %v", i, err)
				return
			}
			if got, err := p.server.ReadMessage(); err != nil {
				t.Errorf("conn %d server read: %v", i, err)
			} else if string(got) != string(resp) {
				t.Errorf("conn %d server got %s", i, got)
			}
		}(i, p)
	}
	wg.Wait()

	for _, p := range pairs {
		p.client.Close()
		p.server.Close()
	}
}

// newTestSocket starts a UnixSocket in a temp dir and returns it with its path.
func newTestSocket(t *testing.T) (*UnixSocket, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sock")
	sock, err := NewUnixSocket(path)
	if err != nil {
		t.Fatalf("new socket: %v", err)
	}
	return sock, path
}
