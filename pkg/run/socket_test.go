package run

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSocketServerBasicCommand(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	handler := func(cmd Command) Response {
		return Response{
			OK:   true,
			Data: map[string]string{"echo": cmd.Message},
		}
	}

	srv := NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = srv.Stop()
		srv.Wait()
	}()

	// Give the server a moment to start listening.
	time.Sleep(50 * time.Millisecond)

	// Connect as a client.
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send a command.
	cmd := Command{Type: "test", Message: "hello"}
	cmdData, _ := json.Marshal(cmd)
	if _, err := conn.Write(append(cmdData, '\n')); err != nil {
		t.Fatalf("write command: %v", err)
	}

	// Read response.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var buf [4096]byte
	n, err := conn.Read(buf[:])
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	respBytes := buf[:n]
	// Trim trailing newline before unmarshaling.
	if respBytes[len(respBytes)-1] == '\n' {
		respBytes = respBytes[:len(respBytes)-1]
	}

	var resp Response
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if !resp.OK {
		t.Errorf("expected OK=true, got false, error=%s", resp.Error)
	}
	if resp.Error != "" {
		t.Errorf("expected no error, got %q", resp.Error)
	}

	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected data to be map, got %T", resp.Data)
	}
	if dataMap["echo"] != "hello" {
		t.Errorf("expected echo=hello, got %v", dataMap["echo"])
	}
}

func TestSocketServerStaleSocket(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	// Create a stale socket file.
	f, err := os.Create(sockPath)
	if err != nil {
		t.Fatalf("create stale file: %v", err)
	}
	f.Close()

	handler := func(cmd Command) Response {
		return Response{OK: true}
	}

	srv := NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start with stale socket: %v", err)
	}
	_ = srv.Stop()
	srv.Wait()
}

func TestSocketServerInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	handler := func(cmd Command) Response {
		return Response{OK: true}
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

	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send invalid JSON.
	if _, err := conn.Write([]byte("not-json\n")); err != nil {
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

	if resp.OK {
		t.Error("expected OK=false for invalid JSON")
	}
	if resp.Error == "" {
		t.Error("expected non-empty error for invalid JSON")
	}
}

func TestSocketServerMultipleCommands(t *testing.T) {
	// Use /tmp for shorter paths to avoid macOS socket path length limit (~104 chars).
	sockPath := filepath.Join(os.TempDir(), "socktest-multi.sock")
	t.Cleanup(func() { os.Remove(sockPath) })

	handler := func(cmd Command) Response {
		return Response{OK: true, Data: cmd.Type}
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

	// Send multiple commands on separate connections.
	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
		if err != nil {
			t.Fatalf("dial %d: %v", i, err)
		}

		cmd := Command{Type: "cmd", Message: "test"}
		cmdData, _ := json.Marshal(cmd)
		if _, err := conn.Write(append(cmdData, '\n')); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}

		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var buf [4096]byte
		n, err := conn.Read(buf[:])
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}

		respBytes := buf[:n]
		if respBytes[len(respBytes)-1] == '\n' {
			respBytes = respBytes[:len(respBytes)-1]
		}

		var resp Response
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			t.Fatalf("unmarshal %d: %v", i, err)
		}

		if !resp.OK {
			t.Errorf("command %d: expected OK=true", i)
		}
		conn.Close()
	}
}
