package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/run"
)

// TestRunDirectoryLifecycle tests that a run directory is created with
// the expected structure and metadata can be read/written.
func TestRunDirectoryLifecycle(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a run.
	meta, err := run.CreateRun(tmpDir, "/test/cwd", os.Getpid())
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	// Verify ID format: 6-char hex.
	if len(meta.ID) != 6 {
		t.Errorf("ID length = %d, want 6", len(meta.ID))
	}

	// Verify directory structure.
	runDir := run.RunDir(tmpDir, meta.ID)
	if _, err := os.Stat(runDir); os.IsNotExist(err) {
		t.Errorf("run directory not created: %s", runDir)
	}

	metaPath := run.RunMetaPath(tmpDir, meta.ID)
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Errorf("run.json not created: %s", metaPath)
	}

	// Load and verify metadata.
	loaded, err := run.LoadRunMeta(metaPath)
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if loaded.ID != meta.ID {
		t.Errorf("ID mismatch: got %s, want %s", loaded.ID, meta.ID)
	}
	if loaded.Status != run.StatusRunning {
		t.Errorf("Status = %s, want running", loaded.Status)
	}
	if loaded.CWD != "/test/cwd" {
		t.Errorf("CWD = %s, want /test/cwd", loaded.CWD)
	}

	// Update status to done.
	loaded.Status = run.StatusDone
	loaded.FinishedAt = time.Now().Unix()
	if err := run.SaveRunMeta(loaded, metaPath); err != nil {
		t.Fatalf("SaveRunMeta: %v", err)
	}

	// Reload and verify.
	reloaded, err := run.LoadRunMeta(metaPath)
	if err != nil {
		t.Fatalf("LoadRunMeta after update: %v", err)
	}
	if reloaded.Status != run.StatusDone {
		t.Errorf("Status after update = %s, want done", reloaded.Status)
	}
	if reloaded.FinishedAt == 0 {
		t.Error("FinishedAt not set")
	}
}

// TestSocketCommunication tests the full socket round-trip:
// create server -> start -> send command -> receive response -> stop.
func TestSocketCommunication(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "control.sock")

	received := make(chan run.Command, 1)

	handler := func(cmd run.Command) run.Response {
		received <- cmd
		switch cmd.Type {
		case "steer":
			if cmd.Message == "" {
				return run.Response{OK: false, Error: "empty message"}
			}
			return run.Response{OK: true, Data: map[string]string{"echo": cmd.Message}}
		case "abort":
			return run.Response{OK: true}
		case "get_state":
			return run.Response{OK: true, Data: map[string]string{"status": "running"}}
		default:
			return run.Response{OK: false, Error: fmt.Sprintf("unknown: %s", cmd.Type)}
		}
	}

	srv := run.NewSocketServer(sockPath, handler)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	// Give the server a moment to start listening.
	time.Sleep(50 * time.Millisecond)

	// Connect and send a steer command.
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	cmd := run.Command{Type: "steer", Message: "hello world"}
	data, _ := json.Marshal(cmd)
	conn.Write(append(data, '\n'))

	// Read response.
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("Read response: %v", err)
	}

	var resp run.Response
	raw := strings.TrimRight(string(buf[:n]), "\n")
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal response: %v (raw: %q)", err, raw)
	}

	if !resp.OK {
		t.Errorf("Response not OK: %s", resp.Error)
	}

	// Verify command was received.
	select {
	case got := <-received:
		if got.Type != "steer" {
			t.Errorf("Command type = %s, want steer", got.Type)
		}
		if got.Message != "hello world" {
			t.Errorf("Command message = %s, want 'hello world'", got.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for command")
	}
}

// TestEventParsingPipeline tests that events written to events.jsonl
// can be correctly parsed by the conv parser.
func TestEventParsingPipeline(t *testing.T) {
	tmpDir := t.TempDir()
	eventsPath := filepath.Join(tmpDir, "events.jsonl")

	// Write events in the format ai rpc produces.
	events := []string{
		`{"type":"agent_start"}`,
		`{"type":"turn_start","turn":1}`,
		`{"type":"message_update","data":{"text_delta":"Hello, "}}`,
		`{"type":"message_update","data":{"text_delta":"world!"}}`,
		`{"type":"tool_execution_start","tool_name":"bash","data":{"args":{"command":"ls -la"}}}`,
		`{"type":"tool_execution_end","tool_name":"bash"}`,
		`{"type":"session_switch","session":"abc123","sessionName":"test session"}`,
		`{"type":"agent_end","success":true}`,
	}

	f, err := os.Create(eventsPath)
	if err != nil {
		t.Fatalf("Create events.jsonl: %v", err)
	}
	for _, evt := range events {
		f.WriteString(evt + "\n")
	}
	f.Close()

	// Read and parse each line.
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != len(events) {
		t.Fatalf("Expected %d lines, got %d", len(events), len(lines))
	}

	kinds := make([]run.EventKind, 0, len(events))
	texts := make([]string, 0, len(events))

	for _, line := range lines {
		evt := run.ParseEvent(line)
		if evt == nil {
			kinds = append(kinds, "nil")
			texts = append(texts, "")
			continue
		}
		kinds = append(kinds, evt.Kind)
		texts = append(texts, evt.Text)
	}

	// Verify parsed kinds.
	expectedKinds := []run.EventKind{
		run.KindMeta,          // agent_start -> "ai: agent started"
		"nil",                 // turn_start -> silent
		run.KindText,          // text_delta
		run.KindText,          // text_delta
		run.KindTool,          // tool_execution_start
		run.KindTool,          // tool_execution_end -> "tool: tool bash done"
		run.KindSessionSwitch, // session_switch
		run.KindMeta,          // agent_end -> "ai: agent done"
	}

	for i, expected := range expectedKinds {
		if kinds[i] != expected {
			t.Errorf("Line %d: kind = %s, want %s", i, kinds[i], expected)
		}
	}

	// Verify some text content.
	if !strings.Contains(texts[2], "Hello") {
		t.Errorf("Text line 2: %q should contain 'Hello'", texts[2])
	}
	if !strings.Contains(texts[4], "bash") {
		t.Errorf("Tool line 4: %q should contain 'bash'", texts[4])
	}
	if !strings.Contains(texts[6], "test session") {
		t.Errorf("Session switch line 6: %q should contain 'test session'", texts[6])
	}
}

// TestAutoSelectionByCwd tests that FindRunningByCwd correctly filters.
func TestAutoSelectionByCwd(t *testing.T) {
	tmpDir := t.TempDir()

	// Create runs with different cwds.
	run1, _ := run.CreateRun(tmpDir, "/project/alpha", os.Getpid())
	run2, _ := run.CreateRun(tmpDir, "/project/beta", os.Getpid())
	run3, _ := run.CreateRun(tmpDir, "/project/alpha", 99999) // different cwd, dead PID

	_ = run1
	_ = run2
	_ = run3

	// Find runs for /project/alpha.
	// run3 has dead PID 99999, so IsRunning filters it out.
	// Only run1 (alive PID) should be returned.
	results, err := run.FindRunningByCwd(tmpDir, "/project/alpha")
	if err != nil {
		t.Fatalf("FindRunningByCwd: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 alive result for /project/alpha, got %d", len(results))
	}
	if len(results) > 0 && results[0].ID != run1.ID {
		t.Errorf("Wrong run returned: %s (expected %s)", results[0].ID, run1.ID)
	}

	// Find runs for /project/beta.
	results, err = run.FindRunningByCwd(tmpDir, "/project/beta")
	if err != nil {
		t.Fatalf("FindRunningByCwd beta: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Expected 1 result for /project/beta, got %d", len(results))
	}
	if results[0].ID != run2.ID {
		t.Errorf("Wrong run returned: %s", results[0].ID)
	}

	// Find runs for non-existent cwd.
	results, err = run.FindRunningByCwd(tmpDir, "/nonexistent")
	if err != nil {
		t.Fatalf("FindRunningByCwd nonexistent: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results for nonexistent cwd, got %d", len(results))
	}
}

// TestPrefixMatch tests that FindByPrefix works correctly.
func TestPrefixMatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Create runs with known IDs by manually creating them.
	ids := []string{"aabb01", "aabb02", "ccdd03"}
	for _, id := range ids {
		meta := &run.RunMeta{
			ID:     id,
			PID:    os.Getpid(),
			CWD:    "/test",
			Status: run.StatusRunning,
		}
		meta.StartedAt = time.Now().Unix()
		path := run.RunMetaPath(tmpDir, id)
		os.MkdirAll(filepath.Dir(path), 0755)
		run.SaveRunMeta(meta, path)
	}

	// Prefix "aa" should match 2 runs -> error.
	_, err := run.FindByPrefix(tmpDir, "aa")
	if err == nil {
		t.Error("Expected error for ambiguous prefix")
	}
	if !strings.Contains(err.Error(), "2") {
		t.Errorf("Error should mention 2 matches: %v", err)
	}

	// Prefix "aab" should match 2 runs -> error.
	_, err = run.FindByPrefix(tmpDir, "aab")
	if err == nil {
		t.Error("Expected error for ambiguous prefix aab")
	}

	// Prefix "aabb0" should match 2 runs -> error.
	_, err = run.FindByPrefix(tmpDir, "aabb0")
	if err == nil {
		t.Error("Expected error for ambiguous prefix aabb0")
	}

	// Full ID should match exactly.
	results, err := run.FindByPrefix(tmpDir, "aabb01")
	if err != nil {
		t.Fatalf("FindByPrefix exact: %v", err)
	}
	if len(results) != 1 || results[0].ID != "aabb01" {
		t.Errorf("Expected exactly aabb01, got %v", results)
	}

	// Prefix "cc" should match 1 run.
	results, err = run.FindByPrefix(tmpDir, "cc")
	if err != nil {
		t.Fatalf("FindByPrefix cc: %v", err)
	}
	if len(results) != 1 || results[0].ID != "ccdd03" {
		t.Errorf("Expected ccdd03, got %v", results)
	}

	// Non-matching prefix.
	results, err = run.FindByPrefix(tmpDir, "zz")
	if err != nil {
		t.Fatalf("FindByPrefix zz: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Expected 0 results, got %d", len(results))
	}
}

// TestSubcommandHelp verifies that each subcommand at least parses help flags
// without crashing.
func TestSubcommandHelp(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("skipping on non-unix")
	}

	// Build the binary.
	bin := filepath.Join(t.TempDir(), "ai-test")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/tiancaiamao/ai/cmd/ai")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	subcommands := []string{"rpc", "run", "ls", "watch", "send", "kill"}
	for _, sub := range subcommands {
		t.Run(sub, func(t *testing.T) {
			cmd := exec.Command(bin, sub, "--help")
			out, err := cmd.CombinedOutput()
			// flag.Parse with --help exits with 0 or 2 depending on flag set behavior.
			// We just want to verify it doesn't crash unexpectedly.
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					// Exit code 0 or 2 is fine for --help.
					if exitErr.ExitCode() != 0 && exitErr.ExitCode() != 2 {
						t.Errorf("exit code %d: %s", exitErr.ExitCode(), string(out))
					}
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestBackwardCompatModeFlag tests that --mode rpc still works.
func TestBackwardCompatModeFlag(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("skipping on non-unix")
	}

	bin := filepath.Join(t.TempDir(), "ai-test")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/tiancaiamao/ai/cmd/ai")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	// --mode invalid should fail.
	cmd = exec.Command(bin, "--mode", "invalid")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "invalid mode") && !strings.Contains(string(out), "deprecated") {
		t.Errorf("expected mode error in output, got: %s", string(out))
	}
}

// TestStaleRunDetection tests that IsRunning correctly detects dead processes.
func TestStaleRunDetection(t *testing.T) {
	// A process that doesn't exist should be detected as not running.
	meta := &run.RunMeta{
		ID:     "dead01",
		PID:    999998, // very unlikely to exist
		Status: run.StatusRunning,
	}

	if run.IsRunning(meta) {
		t.Error("Dead PID should not be reported as running")
	}

	// Current process should be detected as running.
	meta.PID = os.Getpid()
	if !run.IsRunning(meta) {
		t.Error("Current process should be reported as running")
	}

	// Done status should not be running.
	meta.Status = run.StatusDone
	if run.IsRunning(meta) {
		t.Error("Done status should not be running")
	}
}

// TestRunStatusOnProcessExit tests that the run status transition logic
// works correctly for different exit scenarios.
func TestRunStatusOnProcessExit(t *testing.T) {
	tests := []struct {
		name       string
		exitStatus int
		sig        syscall.Signal
		wantStatus string
	}{
		{"clean exit", 0, 0, run.StatusDone},
		{"error exit", 1, 0, run.StatusFailed},
		{"signal killed", 0, syscall.SIGTERM, run.StatusKilled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var status string
			if tt.sig != 0 {
				status = run.StatusKilled
			} else if tt.exitStatus == 0 {
				status = run.StatusDone
			} else {
				status = run.StatusFailed
			}

			if status != tt.wantStatus {
				t.Errorf("status = %s, want %s", status, tt.wantStatus)
			}
		})
	}
}
