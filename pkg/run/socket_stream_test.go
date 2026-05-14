package run

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSocketServerStreaming(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	handler := func(cmd Command) Response {
		return Response{OK: true}
	}

	broadcaster := NewEventBroadcaster()
	defer broadcaster.Close()

	srv := NewSocketServer(sockPath, handler)
	srv.SetBroadcaster(broadcaster)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = srv.Stop()
		srv.Wait()
	}()

	time.Sleep(50 * time.Millisecond)

	// Connect as a streaming client.
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send stream command.
	cmd := Command{Type: "stream", FromSeq: 0}
	cmdData, _ := json.Marshal(cmd)
	if _, err := conn.Write(append(cmdData, '\n')); err != nil {
		t.Fatalf("write stream command: %v", err)
	}

	// Read initial response.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp Response
	if err := json.Unmarshal([]byte(respLine[:len(respLine)-1]), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected OK response, got error: %s", resp.Error)
	}

	// Push an event to the broadcaster.
	testEvent := []byte(`{"type":"agent_start"}`)
	broadcaster.Push(testEvent)

	// Read the event from the stream.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	eventLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	eventLine = eventLine[:len(eventLine)-1] // trim newline
	if eventLine != string(testEvent) {
		t.Errorf("expected event %s, got %s", testEvent, eventLine)
	}
}

func TestSocketServerStreamingFiltered(t *testing.T) {
	sockPath := filepath.Join(os.TempDir(), "socktest-stream-filter.sock")
	t.Cleanup(func() { os.Remove(sockPath) })

	handler := func(cmd Command) Response {
		return Response{OK: true}
	}

	broadcaster := NewEventBroadcaster()
	defer broadcaster.Close()

	srv := NewSocketServer(sockPath, handler)
	srv.SetBroadcaster(broadcaster)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = srv.Stop()
		srv.Wait()
	}()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	cmd := Command{Type: "stream", FromSeq: 0}
	cmdData, _ := json.Marshal(cmd)
	if _, err := conn.Write(append(cmdData, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}

	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp Response
	json.Unmarshal([]byte(respLine[:len(respLine)-1]), &resp)
	if !resp.OK {
		t.Fatalf("stream rejected: %s", resp.Error)
	}

	// Push empty thinking delta (should be filtered).
	emptyThinking := []byte(`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":""}}`)
	broadcaster.Push(emptyThinking)

	// Push a real event.
	realEvent := []byte(`{"type":"text_delta","data":{"text_delta":"hello"}}`)
	broadcaster.Push(realEvent)

	// Should only get the real event.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	eventLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	eventLine = eventLine[:len(eventLine)-1]
	if eventLine != string(realEvent) {
		t.Errorf("expected real event, got %s", eventLine)
	}
}

func TestSocketServerStreamNoBroadcaster(t *testing.T) {
	sockPath := filepath.Join(os.TempDir(), "socktest-no-bcast.sock")
	t.Cleanup(func() { os.Remove(sockPath) })

	handler := func(cmd Command) Response {
		return Response{OK: true}
	}

	srv := NewSocketServer(sockPath, handler)
	// No broadcaster set.

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = srv.Stop()
		srv.Wait()
	}()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	cmd := Command{Type: "stream", FromSeq: 0}
	cmdData, _ := json.Marshal(cmd)
	if _, err := conn.Write(append(cmdData, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp Response
	json.Unmarshal([]byte(respLine[:len(respLine)-1]), &resp)
	if resp.OK {
		t.Error("expected stream to fail without broadcaster")
	}
}

func TestSocketServerNormalCommandWithBroadcaster(t *testing.T) {
	sockPath := filepath.Join(os.TempDir(), "socktest-cmd-bcast.sock")
	t.Cleanup(func() { os.Remove(sockPath) })

	handler := func(cmd Command) Response {
		return Response{OK: true, Data: cmd.Message}
	}

	broadcaster := NewEventBroadcaster()
	defer broadcaster.Close()

	srv := NewSocketServer(sockPath, handler)
	srv.SetBroadcaster(broadcaster)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = srv.Stop()
		srv.Wait()
	}()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	cmd := Command{Type: "prompt", Message: "hello"}
	cmdData, _ := json.Marshal(cmd)
	if _, err := conn.Write(append(cmdData, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var buf [4096]byte
	n, err := conn.Read(buf[:])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	respBytes := buf[:n]
	if respBytes[len(respBytes)-1] == '\n' {
		respBytes = respBytes[:len(respBytes)-1]
	}
	var resp Response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Error("expected OK")
	}
	if resp.Data != "hello" {
		t.Errorf("expected data=hello, got %v", resp.Data)
	}
}

func TestSocketClientStream(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	handler := func(cmd Command) Response {
		return Response{OK: true}
	}

	broadcaster := NewEventBroadcaster()
	defer broadcaster.Close()

	srv := NewSocketServer(sockPath, handler)
	srv.SetBroadcaster(broadcaster)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = srv.Stop()
		srv.Wait()
	}()

	time.Sleep(50 * time.Millisecond)

	client := NewSocketClient(sockPath)
	conn, resp, err := client.Stream(0)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer conn.Close()

	if !resp.OK {
		t.Fatalf("stream rejected: %s", resp.Error)
	}

	// Push an event.
	testEvent := []byte(`{"type":"test"}`)
	broadcaster.Push(testEvent)

	// Read from connection.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read event: %v", err)
	}
	line = line[:len(line)-1]
	if line != string(testEvent) {
		t.Errorf("expected %s, got %s", testEvent, line)
	}
}

func TestSocketClientSendCommand(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	handler := func(cmd Command) Response {
		return Response{OK: true, Data: map[string]string{"echo": cmd.Message}}
	}

	srv := NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = srv.Stop()
		srv.Wait()
	}()

	time.Sleep(50 * time.Millisecond)

	client := NewSocketClient(sockPath)
	resp, err := client.SendCommand(Command{Type: "test", Message: "hello"})
	if err != nil {
		t.Fatalf("SendCommand: %v", err)
	}
	if !resp.OK {
		t.Errorf("expected OK, got error: %s", resp.Error)
	}
}