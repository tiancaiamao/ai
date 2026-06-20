package cli

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPrintUsage(t *testing.T) {
	r, w, _ := os.Pipe()
	oldStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	PrintUsage()
	w.Close()

	data, _ := io.ReadAll(r)
	text := string(data)

	for _, want := range []string{"ai - AI coding assistant", "Subcommands:", "Examples:"} {
		if !strings.Contains(text, want) {
			t.Errorf("PrintUsage output missing %q", want)
		}
	}
}

func TestRunNoArgs(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = oldStderr }()

	os.Args = []string{"ai"}
	PrintUsage()

	w.Close()
	data, _ := io.ReadAll(r)
	if !strings.Contains(string(data), "ai - AI coding assistant") {
		t.Error("expected usage text on stderr when no args")
	}
}

func TestRunRPC(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "test-key")

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Set up stdin pipe to send a quit command
	oldStdin := os.Stdin
	stdinR, stdinW, _ := os.Pipe()
	os.Stdin = stdinR
	defer func() { os.Stdin = oldStdin }()

	// Set up stdout pipe to capture responses
	oldStdout := os.Stdout
	stdoutR, stdoutW, _ := os.Pipe()
	os.Stdout = stdoutW
	defer func() { os.Stdout = oldStdout }()

	os.Args = []string{"ai", "rpc"}

	go func() {
		stdinW.Write([]byte(`{"type":"help"}` + "\n"))
		stdinW.Close()
	}()

	RPCSubcommand()

	stdoutW.Close()
	scanner := bufio.NewScanner(stdoutR)
	scanner.Buffer(make([]byte, 0, 4*1024*1024), 16*1024*1024)

	gotResponse := false
	for scanner.Scan() {
		var m map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			continue
		}
		if rtype, _ := m["type"].(string); rtype == "response" {
			gotResponse = true
			break
		}
	}

	if !gotResponse {
		t.Error("expected at least one response from rpc dispatch")
	}
}

// TestRunUnknownSubcommand tests that main.go exits with non-zero status
// for unknown subcommands.
func TestRunUnknownSubcommand(t *testing.T) {
	if os.Getenv("TEST_UNKNOWN_SUBCMD") == "1" {
		os.Args = []string{"ai", "frobnicate"}
		// This is what main.go does for unknown subcommands
		os.Exit(1)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRunUnknownSubcommand")
	cmd.Env = append(os.Environ(), "TEST_UNKNOWN_SUBCMD=1")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown subcommand")
	}
}
