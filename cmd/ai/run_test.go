package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/run"
)

// --- sendRPCCommandWithTimeout tests ---

func TestSendRPCCommandWithTimeout_DeadPipe(t *testing.T) {
	pr, pw := io.Pipe()
	pr.Close() // no reader — Write should fail immediately

	err := sendRPCCommandWithTimeout(pw, "prompt", "hello", 2*time.Second)
	if err == nil {
		t.Fatal("expected error when writing to dead pipe, got nil")
	}
	t.Logf("got expected error: %v", err)
}

func TestSendRPCCommandWithTimeout_HappyPath(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	go func() {
		buf := make([]byte, 4096)
		pr.Read(buf)
	}()

	err := sendRPCCommandWithTimeout(pw, "prompt", "hello", 5*time.Second)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

// TestSendRPCCommandWithTimeout_BlockedWrite verifies the timeout fires when
// the pipe exists but nobody reads from it (the core bug scenario).
func TestSendRPCCommandWithTimeout_BlockedWrite(t *testing.T) {
	_, pw := io.Pipe()
	// No reader at all — Write will hang forever without the timeout.

	start := time.Now()
	err := sendRPCCommandWithTimeout(pw, "prompt", "hello", 1*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("took %v, should have timed out in ~1s", elapsed)
	}
	t.Logf("got expected error after %v: %v", elapsed, err)
}

// --- runSocketHandler tests ---

// TestRunSocketHandler_DeadSubprocess verifies that runSocketHandler returns an
// immediate error when the child process is no longer alive.
func TestRunSocketHandler_DeadSubprocess(t *testing.T) {
	cmd := exec.Command("true")
	cmd.Start()
	cmd.Wait()

	handler := runSocketHandler(
		&run.RunMeta{ID: "test"},
		"/dev/null",
		cmd.Process,
		nil,
	)

	resp := handler(run.Command{Type: "prompt", Message: "hello"})
	if resp.OK {
		t.Fatal("expected OK=false for dead subprocess")
	}
	t.Logf("got error (expected): %s", resp.Error)
}

// TestRunSocketHandler_PromptBlockedByDeadPipe is an integration test for the
// full dead-pipe scenario: process alive but pipe reader gone.
// Takes ~10s (the handler's write timeout).
func TestRunSocketHandler_PromptBlockedByDeadPipe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10s integration test in short mode")
	}

	proc := os.Process{Pid: os.Getpid()}
	_, pw := io.Pipe()

	handler := runSocketHandler(
		&run.RunMeta{ID: "test"},
		"/dev/null",
		&proc,
		pw,
	)

	start := time.Now()
	resp := handler(run.Command{Type: "prompt", Message: "hello"})
	elapsed := time.Since(start)

	if resp.OK {
		t.Fatal("expected OK=false when pipe reader is dead")
	}
	if elapsed > 12*time.Second {
		t.Fatalf("response took %v, should have timed out within ~10s", elapsed)
	}
	t.Logf("got error after %v (expected): %s", elapsed, resp.Error)
}

// --- acceptLoop concurrency test ---

// TestSocketAcceptLoopConcurrent verifies that a slow handler on one connection
// does not block other connections from being served promptly.
func TestSocketAcceptLoopConcurrent(t *testing.T) {
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("socktest-concurrent-%d.sock", time.Now().UnixNano()))
	os.Remove(sockPath)
	t.Cleanup(func() { os.Remove(sockPath) })

	handler := func(cmd run.Command) run.Response {
		if cmd.Message == "slow" {
			time.Sleep(2 * time.Second)
		}
		return run.Response{OK: true, Data: cmd.Message}
	}

	srv := run.NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Start a slow request in the background.
	slowDone := make(chan error, 1)
	go func() {
		conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
		if err != nil {
			slowDone <- err
			return
		}
		defer conn.Close()
		cmd := run.Command{Type: "test", Message: "slow"}
		data, _ := json.Marshal(cmd)
		conn.Write(append(data, '\n'))
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		var buf [4096]byte
		_, err = conn.Read(buf[:])
		slowDone <- err
	}()

	// Give the slow request time to be accepted and start sleeping.
	time.Sleep(200 * time.Millisecond)

	// Send a fast request — must not be blocked by the slow one.
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("fast dial: %v", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))
	cmd := run.Command{Type: "test", Message: "fast"}
	data, _ := json.Marshal(cmd)
	conn.Write(append(data, '\n'))

	var buf [4096]byte
	n, err := conn.Read(buf[:])
	if err != nil {
		t.Fatalf("fast read: %v", err)
	}

	var resp run.Response
	raw := buf[:n]
	if raw[len(raw)-1] == '\n' {
		raw = raw[:len(raw)-1]
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.OK {
		t.Fatalf("fast response not OK: %s", resp.Error)
	}

	// Wait for the slow request to finish.
	select {
	case err := <-slowDone:
		if err != nil {
			t.Logf("slow request ended with: %v (acceptable)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("slow request did not complete in time")
	}

	srv.Stop()
	srv.Wait()
}
