package transport

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestClient dials the test socket and returns a newline-framed Conn for it.
func newTestClient(t *testing.T, path string) Conn {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return NewStdio(c, c)
}

// readJSON reads one message from a conn, failing the test on error.
func readJSON(t *testing.T, c Conn) json.RawMessage {
	t.Helper()
	m, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return m
}

// readWithTimeout reads one message or reports false if none arrives within d.
func readWithTimeout(t *testing.T, c Conn, d time.Duration) (json.RawMessage, bool) {
	t.Helper()
	type out struct {
		m   json.RawMessage
		err error
	}
	ch := make(chan out, 1)
	go func() {
		m, err := c.ReadMessage()
		ch <- out{m, err}
	}()
	select {
	case o := <-ch:
		if o.err != nil {
			t.Fatalf("read: %v", o.err)
		}
		return o.m, true
	case <-time.After(d):
		return nil, false
	}
}

// msgField returns a raw field of a JSON message.
func msgField(t *testing.T, m json.RawMessage, field string) json.RawMessage {
	t.Helper()
	mm, err := parseObject(m)
	if err != nil {
		t.Fatalf("parse %s: %v", field, err)
	}
	return mm[field]
}

// TestHubResponseRouting verifies that responses are routed back to the owning
// conn with the client's original id restored — even when two clients use the
// same id (the collision the hub must disambiguate via internal remapping).
func TestHubResponseRouting(t *testing.T) {
	sock, path := newTestSocket(t)
	defer sock.Close()
	hub := NewHub()
	defer hub.Close()

	attach := func() Conn {
		client := newTestClient(t, path)
		sc, err := sock.Accept()
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		hub.AddConn(sc)
		return client
	}
	clientA := attach()
	clientB := attach()

	// A sends id=1; the hub assigns it a unique internal id.
	if err := clientA.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"doit","params":{"who":"A"}}`)); err != nil {
		t.Fatalf("A write: %v", err)
	}
	mA, err := hub.ReadMessage()
	if err != nil {
		t.Fatalf("read A: %v", err)
	}
	hubIDa, _ := extractRequestID(mA)

	// B sends the SAME id (1); the hub must assign a different internal id.
	if err := clientB.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"doit","params":{"who":"B"}}`)); err != nil {
		t.Fatalf("B write: %v", err)
	}
	mB, err := hub.ReadMessage()
	if err != nil {
		t.Fatalf("read B: %v", err)
	}
	hubIDb, _ := extractRequestID(mB)
	if string(hubIDa) == string(hubIDb) {
		t.Fatalf("hub failed to remap colliding client ids: both %s", hubIDa)
	}

	// Respond to each internal id. Each must land on its owner with id=1 back.
	if err := hub.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":` + string(hubIDa) + `,"result":"toA"}`)); err != nil {
		t.Fatalf("respond A: %v", err)
	}
	if err := hub.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":` + string(hubIDb) + `,"result":"toB"}`)); err != nil {
		t.Fatalf("respond B: %v", err)
	}

	gotA := readJSON(t, clientA)
	if id := msgField(t, gotA, "id"); string(id) != "1" {
		t.Fatalf("A response id = %s, want 1 (original restored)", id)
	}
	if r := msgField(t, gotA, "result"); string(r) != `"toA"` {
		t.Fatalf("A result = %s, want toA", r)
	}
	gotB := readJSON(t, clientB)
	if id := msgField(t, gotB, "id"); string(id) != "1" {
		t.Fatalf("B response id = %s, want 1 (original restored)", id)
	}
	if r := msgField(t, gotB, "result"); string(r) != `"toB"` {
		t.Fatalf("B result = %s, want toB", r)
	}

	// No cross-talk: each client got exactly its own response.
	if _, ok := readWithTimeout(t, clientA, 100*time.Millisecond); ok {
		t.Fatal("A received a stray extra response (broadcast instead of route)")
	}
	if _, ok := readWithTimeout(t, clientB, 100*time.Millisecond); ok {
		t.Fatal("B received a stray extra response (broadcast instead of route)")
	}
}

func TestHubLoadRouting(t *testing.T) {
	sock, path := newTestSocket(t)
	defer sock.Close()
	hub := NewHub()
	defer hub.Close()

	attach := func() Conn {
		client := newTestClient(t, path)
		sc, err := sock.Accept()
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		hub.AddConn(sc)
		return client
	}
	clientA := attach()
	clientB := attach()

	if err := clientA.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"session/load","params":{}}`)); err != nil {
		t.Fatalf("load write: %v", err)
	}
	loadA, err := hub.ReadMessage()
	if err != nil {
		t.Fatalf("read load A: %v", err)
	}
	loadIDA, _ := extractRequestID(loadA)
	if err := clientB.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"session/load","params":{}}`)); err != nil {
		t.Fatalf("load write B: %v", err)
	}

	noteA := json.RawMessage(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"a","update":{"sessionUpdate":"user_message_chunk"}}}`)
	if err := hub.WriteMessage(noteA); err != nil {
		t.Fatalf("replay A write: %v", err)
	}
	if err := hub.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":` + string(loadIDA) + `,"result":{}}`)); err != nil {
		t.Fatalf("load A response: %v", err)
	}
	gotAUpdate := readJSON(t, clientA)
	if string(msgField(t, gotAUpdate, "method")) != `"session/update"` {
		t.Fatalf("A did not receive replay update: %s", gotAUpdate)
	}
	_ = readJSON(t, clientA)

	loadB, err := hub.ReadMessage()
	if err != nil {
		t.Fatalf("read load B: %v", err)
	}
	loadIDB, _ := extractRequestID(loadB)
	noteB := json.RawMessage(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"b","update":{"sessionUpdate":"user_message_chunk"}}}`)
	if err := hub.WriteMessage(noteB); err != nil {
		t.Fatalf("replay B write: %v", err)
	}
	if err := hub.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":` + string(loadIDB) + `,"result":{}}`)); err != nil {
		t.Fatalf("load B response: %v", err)
	}
	gotBUpdate := readJSON(t, clientB)
	if string(msgField(t, gotBUpdate, "method")) != `"session/update"` {
		t.Fatalf("B did not receive replay update: %s", gotBUpdate)
	}
	_ = readJSON(t, clientB)

}

func TestHubLoadFailureBroadcastsBufferedUpdates(t *testing.T) {
	sock, path := newShortSocket(t)
	defer sock.Close()
	hub := NewHub()
	defer hub.Close()

	attach := func() Conn {
		client := newTestClient(t, path)
		sc, err := sock.Accept()
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		hub.AddConn(sc)
		return client
	}
	clientA := attach()
	clientB := attach()
	if err := clientA.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"session/load","params":{}}`)); err != nil {
		t.Fatalf("load write: %v", err)
	}
	load, err := hub.ReadMessage()
	if err != nil {
		t.Fatalf("read load: %v", err)
	}
	loadID, _ := extractRequestID(load)
	note := json.RawMessage(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk"}}}`)
	if err := hub.WriteMessage(note); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := hub.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":` + string(loadID) + `,"error":{"code":-32600,"message":"busy"}}`)); err != nil {
		t.Fatalf("load error: %v", err)
	}
	for name, c := range map[string]Conn{"A": clientA, "B": clientB} {
		got := readJSON(t, c)
		if string(msgField(t, got, "method")) != `"session/update"` {
			t.Fatalf("%s did not receive buffered update: %s", name, got)
		}
	}
	if got := readJSON(t, clientA); len(msgField(t, got, "error")) == 0 {
		t.Fatalf("A did not receive load error: %s", got)
	}
}

func TestHubLoadDisconnectDropsReplay(t *testing.T) {
	sock, path := newShortSocket(t)
	defer sock.Close()
	hub := NewHub()
	defer hub.Close()

	dial := func() (net.Conn, Conn) {
		client, err := net.Dial("unix", path)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		sc, err := sock.Accept()
		if err != nil {
			client.Close()
			t.Fatalf("accept: %v", err)
		}
		hub.AddConn(sc)
		return client, NewNetConn(client)
	}
	clientAConn, clientA := dial()
	defer clientAConn.Close()
	clientBConn, clientB := dial()
	defer clientBConn.Close()
	if err := clientA.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"session/load","params":{}}`)); err != nil {
		t.Fatalf("load write: %v", err)
	}
	load, err := hub.ReadMessage()
	if err != nil {
		t.Fatalf("read load: %v", err)
	}
	loadID, _ := extractRequestID(load)
	if err := clientA.Close(); err != nil {
		t.Fatalf("close A: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		hub.mu.Lock()
		aborted := hub.activeLoad != nil && hub.activeLoad.aborted
		hub.mu.Unlock()
		if aborted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("load route was not marked aborted")
		}
		time.Sleep(time.Millisecond)
	}
	note := json.RawMessage(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk"}}}`)
	if err := hub.WriteMessage(note); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := hub.WriteMessage(json.RawMessage(`{"jsonrpc":"2.0","id":` + string(loadID) + `,"result":{}}`)); err != nil {
		t.Fatalf("load response: %v", err)
	}
	if err := clientBConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, err := clientB.ReadMessage(); err == nil {
		t.Fatal("B received replay after A disconnected")
	}
}

// TestHubBroadcastNotification verifies notifications (no id) fan out to every
// attached conn.
func TestHubBroadcastNotification(t *testing.T) {
	sock, path := newTestSocket(t)
	defer sock.Close()
	hub := NewHub()
	defer hub.Close()

	attach := func() Conn {
		client := newTestClient(t, path)
		sc, err := sock.Accept()
		if err != nil {
			t.Fatalf("accept: %v", err)
		}
		hub.AddConn(sc)
		return client
	}
	clientA := attach()
	clientB := attach()

	note := json.RawMessage(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"s","update":{"sessionUpdate":"agent_message_chunk"}}}`)
	if err := hub.WriteMessage(note); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	for name, c := range map[string]Conn{"A": clientA, "B": clientB} {
		got := readJSON(t, c)
		if m := msgField(t, got, "method"); string(m) != `"session/update"` {
			t.Fatalf("%s broadcast method = %s, want session/update", name, m)
		}
	}
}

func newShortSocket(t *testing.T) (*UnixSocket, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "a")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "s")
	sock, err := NewUnixSocket(path)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("new socket: %v", err)
	}
	t.Cleanup(func() {
		sock.Close()
		os.RemoveAll(dir)
	})
	return sock, path
}

// TestHubCloseUnblocksRead verifies Close makes a blocked ReadMessage return
// io.EOF.
func TestHubCloseUnblocksRead(t *testing.T) {
	sock, path := newTestSocket(t)
	defer sock.Close()
	hub := NewHub()
	defer hub.Close()

	client := newTestClient(t, path)
	sc, err := sock.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	hub.AddConn(sc)

	ch := make(chan error, 1)
	go func() {
		_, err := hub.ReadMessage()
		ch <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the read block

	if err := hub.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case err := <-ch:
		if err != io.EOF {
			t.Fatalf("want io.EOF after Close, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock ReadMessage")
	}
	_ = client
}

// TestStdioCloseUnblocksRead verifies NewStdio.Close unblocks a blocked read
// with io.EOF (used to shut down the stdio ACP path cleanly).
func TestStdioCloseUnblocksRead(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c2.Close()
	conn := NewStdio(c1, c2)

	ch := make(chan error, 1)
	go func() {
		_, err := conn.ReadMessage()
		ch <- err
	}()
	time.Sleep(50 * time.Millisecond) // let the read block on the empty pipe

	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case err := <-ch:
		if err != io.EOF {
			t.Fatalf("want io.EOF after Close, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock ReadMessage")
	}
}
