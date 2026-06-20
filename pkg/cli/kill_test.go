package cli

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/run"
)

func TestProcessAlive(t *testing.T) {
	// Current process should be alive.
	if !processAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}

	// A PID that almost certainly doesn't exist.
	if processAlive(999999999) {
		t.Error("non-existent PID should not be alive")
	}
}

func TestTrySocketAbortReadsLineDelimitedResponse(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("skipping on non-unix")
	}

	tmpDir, err := os.MkdirTemp("/tmp", "ai-killtest-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sockPath := filepath.Join(tmpDir, "control.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)

		resp := run.Response{OK: true}
		data, _ := json.Marshal(resp)
		data = append(data, '\n')

		// Write response in two chunks to verify the client handles partial reads.
		half := len(data) / 2
		_, _ = conn.Write(data[:half])
		time.Sleep(20 * time.Millisecond)
		_, _ = conn.Write(data[half:])
	}()

	if ok := trySocketAbort(sockPath); !ok {
		t.Fatal("expected trySocketAbort to return true")
	}
	<-done
}

func TestKillRunUpdatesMetaAndKillsProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("skipping on non-unix")
	}

	cmd := exec.Command("sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	baseDir := t.TempDir()
	meta := &run.RunMeta{
		ID:        "abc123",
		PID:       cmd.Process.Pid,
		CWD:       "/tmp",
		Status:    run.StatusRunning,
		StartedAt: time.Now().Unix(),
	}
	metaPath := run.RunMetaPath(baseDir, meta.ID)
	if err := run.SaveRunMeta(meta, metaPath); err != nil {
		t.Fatalf("save run meta: %v", err)
	}

	killRun(meta, baseDir)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		t.Fatal("subprocess did not exit after killRun")
	}

	loaded, err := run.LoadRunMeta(metaPath)
	if err != nil {
		t.Fatalf("load run meta: %v", err)
	}
	if loaded.Status != run.StatusKilled {
		t.Fatalf("expected status %q, got %q", run.StatusKilled, loaded.Status)
	}
	if loaded.FinishedAt == 0 {
		t.Fatal("expected FinishedAt to be set")
	}
}

func TestWaitForExit(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("skipping on non-unix")
	}

	// Test 1: Process exits within timeout
	cmd := exec.Command("sh", "-c", "sleep 1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	defer cmd.Wait()

	waitForExit(cmd.Process.Pid, 3*time.Second)
	// Should return quickly, no assertion needed if it doesn't hang

	// Test 2: Process doesn't exit within timeout
	cmd2 := exec.Command("sh", "-c", "sleep 30")
	if err := cmd2.Start(); err != nil {
		t.Fatalf("start subprocess: %v", err)
	}
	defer cmd2.Process.Kill()

	start := time.Now()
	waitForExit(cmd2.Process.Pid, 500*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed < 450*time.Millisecond {
		t.Errorf("expected waitForExit to wait ~500ms, got %v", elapsed)
	}
	if elapsed > 600*time.Millisecond {
		t.Errorf("expected waitForExit to wait ~500ms, got %v", elapsed)
	}
}
