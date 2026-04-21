package bridge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/genius/ag/internal/backend"
)

func TestRunRawReader(t *testing.T) {
	input := "line 1\nline 2\nline 3\n"
	stdout := strings.NewReader(input)

	dir := t.TempDir()
	aw := NewActivityWriter(dir)
	sw, err := NewStreamWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sw.Close()

	if err := runRawReader(stdout, aw, sw); err != nil {
		t.Fatalf("runRawReader: %v", err)
	}

	// Flush stream writer
	sw.Flush()

	// Read activity.json directly
	aw.Close()
	time.Sleep(50 * time.Millisecond)

	data, err := os.ReadFile(filepath.Join(dir, "activity.json"))
	if err != nil {
		t.Fatalf("read activity.json: %v", err)
	}
	var act AgentActivity
	if err := json.Unmarshal(data, &act); err != nil {
		t.Fatalf("parse activity.json: %v", err)
	}
	if act.Turns != 3 {
		t.Errorf("Turns = %d, want 3", act.Turns)
	}
	if act.LastText != "line 3" {
		t.Errorf("LastText = %q, want %q", act.LastText, "line 3")
	}
}

func TestRunRawReader_Empty(t *testing.T) {
	stdout := strings.NewReader("")

	dir := t.TempDir()
	aw := NewActivityWriter(dir)
	sw, err := NewStreamWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sw.Close()

	if err := runRawReader(stdout, aw, sw); err != nil {
		t.Fatalf("runRawReader: %v", err)
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly 10", 10, "exactly 10"},
		{"this is a longer string", 10, "this is..."},
		{"ab", 2, "ab"},
	}
	for _, tt := range tests {
		got := truncateStr(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestHandleCommand_RawBackendUnsupported(t *testing.T) {
	be := &backend.BackendConfig{
		Name:    "codex",
		Command: "codex",
		Protocol: backend.ProtocolRaw,
		Supports: backend.Supports{Steer: false, Abort: false, Prompt: false},
	}

	var buf bytes.Buffer

	resp := handleCommand(BridgeCommand{Type: CmdSteer, Message: "hello"}, &buf, be)
	if resp.OK {
		t.Error("steer should fail for raw backend")
	}
	if !strings.Contains(resp.Error, "does not support steer") {
		t.Errorf("unexpected error: %s", resp.Error)
	}

	resp = handleCommand(BridgeCommand{Type: CmdAbort}, &buf, be)
	if resp.OK {
		t.Error("abort should fail for raw backend")
	}

	resp = handleCommand(BridgeCommand{Type: CmdPrompt, Message: "hi"}, &buf, be)
	if resp.OK {
		t.Error("prompt should fail for raw backend")
	}
}

func TestHandleCommand_JsonRpcBackend(t *testing.T) {
	be := &backend.BackendConfig{
		Name:    "ai",
		Command: "ai",
		Protocol: backend.ProtocolJSONRPC,
		Supports: backend.Supports{Steer: true, Abort: true, Prompt: true},
	}

	var buf bytes.Buffer

	resp := handleCommand(BridgeCommand{Type: CmdSteer, Message: "hello"}, &buf, be)
	if !resp.OK {
		t.Errorf("steer should succeed: %s", resp.Error)
	}

		// Check the JSON written to stdin
	line := buf.String()
	if !strings.Contains(line, `"type":"steer"`) {
		t.Errorf("expected steer JSON in stdin, got: %s", line)
	}
}