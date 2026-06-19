package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// --- os.Pipe subprocess integration test ---

// TestSubprocessPipeIntegration verifies that os.Pipe-based stdin/stdout wiring
// correctly passes data between the parent process and the "ai rpc" subprocess.
// This is the exact pipe configuration used by serveSubcommand/runSubcommand.
//
// Regression test for the bug where io.Pipe was used instead of os.Pipe:
// io.Pipe requires Go's os/exec to spawn internal goroutines for copying data
// between the pipe and the child's file descriptors. This is unreliable — data
// written to the PipeWriter may never reach the subprocess. os.Pipe provides
// kernel-buffered OS-level pipes that the child reads directly.
func TestSubprocessPipeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("skipping on non-unix")
	}

	// Build the binary.
	bin := filepath.Join(t.TempDir(), "ai-pipe-test")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/tiancaiamao/ai/cmd/ai")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	// Create OS-level pipes for stdin and stdout, mirroring the
	// serveSubcommand pipe wiring exactly.
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin os.Pipe: %v", err)
	}
	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout os.Pipe: %v", err)
	}

	subCmd := exec.Command(bin, "rpc")
	subCmd.Stdin = stdinReader
	subCmd.Stdout = pipeWriter
	subCmd.Stderr = os.Stderr // forward for debug visibility

	if err := subCmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}

	// Close parent's copies of child-side FDs — same as serveSubcommand.
	// The child has inherited these FDs via fork/exec; keeping them open
	// in the parent would prevent EOF detection.
	stdinReader.Close()
	pipeWriter.Close()

	// Read subprocess stdout events in background.
	type jsonLine struct {
		line string
		err  error
	}
	lines := make(chan jsonLine, 32)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(pipeReader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			lines <- jsonLine{line: scanner.Text()}
		}
		if err := scanner.Err(); err != nil {
			lines <- jsonLine{err: err}
		}
	}()

	// Wait for server_start event (proves the subprocess is alive and
	// writing to the os.Pipe stdout).
	// If the subprocess exits without writing anything (e.g. CI has no API
	// key), skip the test gracefully.
	gotServerStart := false
	select {
	case jl, ok := <-lines:
		if !ok {
			// stdout pipe closed without data — subprocess likely failed to start.
			subCmd.Process.Kill()
			subCmd.Wait()
			t.Skip("subprocess exited without writing server_start (likely no API key configured)")
		}
		if jl.err != nil {
			t.Fatalf("error reading server_start: %v", jl.err)
		}
		if jl.line == "" {
			// Empty line from subprocess — also indicates startup failure.
			subCmd.Process.Kill()
			subCmd.Wait()
			t.Skip("subprocess wrote empty line (likely no API key configured)")
		}
		var evt map[string]any
		if err := json.Unmarshal([]byte(jl.line), &evt); err != nil {
			t.Fatalf("parse server_start: %v\nline: %s", err, jl.line)
		}
		if evt["type"] != "server_start" {
			t.Fatalf("expected server_start, got: %v", evt["type"])
		}
		gotServerStart = true
		t.Logf("server_start OK: model=%v, tools=%d", evt["model"], len(evt["tools"].([]any)))
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for server_start event — os.Pipe data flow broken?")
	}
	if !gotServerStart {
		t.Fatal("no server_start received")
	}

	// Send a prompt command through the stdin pipe.
	if err := sendRPCCommand(stdinWriter, "prompt", "Say hello in exactly 3 words"); err != nil {
		t.Fatalf("sendRPCCommand: %v", err)
	}

	// Read events until we see agent_end — proves the full round-trip:
	// stdinWriter → os.Pipe → subprocess reads stdin → subprocess writes
	// response → os.Pipe → parent reads stdout.
	gotResponse := false
	gotAgentEnd := false
	timeout := time.After(30 * time.Second)
	for !gotAgentEnd {
		select {
		case jl, ok := <-lines:
			if !ok {
				if !gotAgentEnd {
					t.Fatal("stdout pipe closed before agent_end")
				}
				return
			}
			if jl.err != nil {
				t.Fatalf("read error: %v", jl.err)
			}
			var evt map[string]any
			if err := json.Unmarshal([]byte(jl.line), &evt); err != nil {
				t.Logf("skipping non-json line: %s", jl.line)
				continue
			}
			switch evt["type"] {
			case "response":
				gotResponse = true
				t.Logf("response event: success=%v", evt["success"])
			case "agent_end":
				gotAgentEnd = true
				msgs, _ := evt["messages"].([]any)
				t.Logf("agent_end: %d messages", len(msgs))
			}
		case <-timeout:
			t.Fatalf("timed out waiting for agent_end (response=%v)", gotResponse)
		}
	}

	if !gotResponse {
		t.Error("never received 'response' event — prompt may not have reached subprocess")
	}

	// Cleanup.
	stdinWriter.Close()
	pipeReader.Close()
	subCmd.Process.Kill()
	subCmd.Wait()
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
