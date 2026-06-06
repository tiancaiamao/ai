package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRegisterSlash verifies that a slash command can be registered and invoked
// both via GetSlashHandler and via JSON-RPC command dispatch.
func TestRegisterSlash(t *testing.T) {
	server := NewServer()

	called := false
	server.RegisterSlash("greet", "say hi", func(args string) (any, error) {
		called = true
		return "hi " + args, nil
	})

	// Lookup via GetSlashHandler
	h, ok := server.GetSlashHandler("greet")
	if !ok {
		t.Fatal("expected slash handler to be registered")
	}
	res, err := h("world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "hi world" {
		t.Errorf("expected 'hi world', got %v", res)
	}
	if !called {
		t.Error("handler was not called")
	}

	// Dispatch via handleCommand — falls back to slash handler when no RPC handler
	cmd := RPCCommand{Type: "greet", Message: "rpc"}
	resp := server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("slash fallback dispatch failed: %s", resp.Error)
	}
	if resp.Data != "hi rpc" {
		t.Errorf("expected 'hi rpc', got %v", resp.Data)
	}
}

// TestRegisterHiddenSlash verifies that hidden slash commands are registered
// and callable, but excluded from ListSlashCommands.
func TestRegisterHiddenSlash(t *testing.T) {
	server := NewServer()
	server.RegisterSlash("visible", "a visible command", func(args string) (any, error) {
		return "v", nil
	})
	server.RegisterHiddenSlash("secret", "a hidden command", func(args string) (any, error) {
		return "s", nil
	})

	// Hidden is callable via GetSlashHandler
	h, ok := server.GetSlashHandler("secret")
	if !ok {
		t.Fatal("expected hidden handler to be registered")
	}
	res, err := h("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "s" {
		t.Errorf("expected 's', got %v", res)
	}

	// Hidden is callable via handleCommand fallback
	resp := server.handleCommand(RPCCommand{Type: "secret"})
	if !resp.Success {
		t.Errorf("secret dispatch failed: %s", resp.Error)
	}

	// Hidden is excluded from ListSlashCommands
	names := server.ListSlashCommands()
	for _, info := range names {
		if info.Name == "secret" {
			t.Errorf("hidden command should not appear in ListSlashCommands: %+v", info)
		}
		if info.Name == "visible" {
			return
		}
	}
	t.Errorf("expected 'visible' in ListSlashCommands, got: %+v", names)
}

// TestListSlashCommandsEmpty ensures an empty list works fine.
func TestListSlashCommandsEmpty(t *testing.T) {
	server := NewServer()
	got := server.ListSlashCommands()
	if len(got) != 0 {
		t.Errorf("expected empty list, got %+v", got)
	}
}

// TestCommandsAccessor verifies that Commands returns the underlying registry.
func TestCommandsAccessor(t *testing.T) {
	server := NewServer()
	reg := server.Commands()
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	server.RegisterSlash("foo", "foo command", func(args string) (any, error) {
		return nil, nil
	})
	// Verify the same registry is exposed
	if _, ok := reg.Get("foo"); !ok {
		t.Error("expected 'foo' to be visible via Commands()")
	}
}

// TestHasHandler exercises both branches of HasHandler.
func TestHasHandler(t *testing.T) {
	server := NewServer()
	// ping is pre-registered
	if !server.HasHandler(CommandPing) {
		t.Errorf("expected handler for %s", CommandPing)
	}
	if server.HasHandler("nonexistent_command") {
		t.Error("expected no handler for nonexistent command")
	}

	// Registering a new handler
	server.Register("custom", func(cmd RPCCommand) (any, error) {
		return nil, nil
	})
	if !server.HasHandler("custom") {
		t.Error("expected handler for 'custom' after Register")
	}
}

// TestExtractSlashArgs covers the three branches: Message, Data, and empty.
func TestExtractSlashArgs(t *testing.T) {
	server := NewServer()

	// Message wins over Data
	cmd := RPCCommand{Message: "hello", Data: json.RawMessage(`{"a":1}`)}
	if got := server.extractSlashArgs(cmd); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}

	// Empty message, data present -> raw JSON string
	cmd = RPCCommand{Data: json.RawMessage(`{"a":1}`)}
	if got := server.extractSlashArgs(cmd); got != `{"a":1}` {
		t.Errorf("expected raw JSON, got %q", got)
	}

	// Both empty -> ""
	cmd = RPCCommand{}
	if got := server.extractSlashArgs(cmd); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// TestSendResponseAndErrorCapture captures stdout writes for responses and errors.
func TestSendResponseAndErrorCapture(t *testing.T) {
	server := NewServer()
	var buf bytes.Buffer
	server.SetOutput(&buf)

	resp := server.successResponse("id1", "ping", map[string]string{"k": "v"})
	server.sendResponse(resp)
	server.sendError("id2", "boom")

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d (%q)", len(lines), out)
	}

	var first RPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("failed to unmarshal first response: %v", err)
	}
	if !first.Success || first.Command != "ping" {
		t.Errorf("unexpected first response: %+v", first)
	}

	var second RPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("failed to unmarshal second response: %v", err)
	}
	if second.Success {
		t.Errorf("expected error response, got %+v", second)
	}
	if second.ID != "id2" || second.Error != "boom" {
		t.Errorf("unexpected error response: %+v", second)
	}
}

// TestEmitEvent ensures EmitEvent marshals and writes JSON to output.
func TestEmitEvent(t *testing.T) {
	server := NewServer()
	var buf bytes.Buffer
	server.SetOutput(&buf)

	server.EmitEvent(map[string]string{"event": "ping"})

	out := buf.String()
	if !strings.Contains(out, `"event":"ping"`) {
		t.Errorf("expected JSON containing event:ping, got %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("expected trailing newline, got %q", out)
	}
}

// TestSetOutputNil verifies that setting a nil output is allowed and
// subsequent writes are dropped silently.
func TestSetOutputNil(t *testing.T) {
	server := NewServer()
	server.SetOutput(nil)
	// These should not panic.
	server.EmitEvent(map[string]string{"event": "drop"})
	server.sendResponse(server.successResponse("x", "y", nil))
	server.sendError("z", "err")
}

// TestRunWithIOEndToEnd drives RunWithIO through a single command lifecycle
// (parsing + dispatch + response write) and ensures it returns on EOF.
func TestRunWithIOEndToEnd(t *testing.T) {
	server := NewServer()
	server.Register("echo", func(cmd RPCCommand) (any, error) {
		return map[string]string{"echo": cmd.Message}, nil
	})

	input := `{"id":"1","type":"echo","message":"hi"}` + "\n"
	var out bytes.Buffer

	err := server.RunWithIO(strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("RunWithIO returned error: %v", err)
	}

	var resp RPCResponse
	if err := json.Unmarshal(bytes.TrimRight(out.Bytes(), "\n"), &resp); err != nil {
		t.Fatalf("could not parse output: %v (out=%q)", err, out.String())
	}
	if !resp.Success || resp.Command != "echo" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// TestRunWithIOParseError ensures malformed JSON produces an error response
// but does not terminate the loop.
func TestRunWithIOParseError(t *testing.T) {
	server := NewServer()
	server.Register("noop", func(cmd RPCCommand) (any, error) { return nil, nil })

	// First line: invalid JSON. Second line: valid command. Then EOF.
	input := "not-json\n" + `{"id":"2","type":"noop"}` + "\n"
	var out bytes.Buffer

	err := server.RunWithIO(strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("RunWithIO returned error: %v", err)
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d (%q)", len(lines), out.String())
	}

	var errResp RPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &errResp); err != nil {
		t.Fatalf("could not parse error response: %v", err)
	}
	if errResp.Success {
		t.Errorf("expected error response for invalid JSON, got %+v", errResp)
	}
	if !strings.Contains(errResp.Error, "Failed to parse command") {
		t.Errorf("expected parse error message, got %q", errResp.Error)
	}

	var okResp RPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &okResp); err != nil {
		t.Fatalf("could not parse ok response: %v", err)
	}
	if !okResp.Success || okResp.Command != "noop" {
		t.Errorf("unexpected response: %+v", okResp)
	}
}

// TestRunWithIOExitOnCancel verifies that Cancel terminates RunWithIO.
func TestRunWithIOExitOnCancel(t *testing.T) {
	server := NewServer()

	// Reader that blocks forever — like an idle stdin.
	r, _ := io.Pipe()

	done := make(chan error, 1)
	go func() {
		done <- server.RunWithIO(r, io.Discard)
	}()

	// Schedule cancellation concurrently.
	go server.Cancel()

	select {
	case err := <-done:
		if err == nil {
			// Cancel-induced error is typically context.Canceled — but nil is also acceptable.
			return
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("RunWithIO did not return after Cancel")
	}
}

// TestCancelViaExposedAPI verifies the public Cancel method works without panics
// and is idempotent.
func TestCancelViaExposedAPI(t *testing.T) {
	server := NewServer()
	ctx := server.Context()
	server.Cancel()
	server.Cancel() // idempotent — must not panic

	select {
	case <-ctx.Done():
	default:
		t.Error("expected ctx to be done after Cancel()")
	}
}

// TestSlashFallbackError verifies that errors from slash handlers propagate via the fallback path.
func TestSlashFallbackError(t *testing.T) {
	server := NewServer()
	server.RegisterSlash("failcmd", "fails", func(args string) (any, error) {
		return nil, errors.New("intentional slash failure")
	})

	resp := server.handleCommand(RPCCommand{Type: "failcmd"})
	if resp.Success {
		t.Error("expected error response")
	}
	if resp.Error != "intentional slash failure" {
		t.Errorf("expected slash failure message, got %q", resp.Error)
	}
}

// TestRunWithIOSlashFallback ensures slash command fallback works end-to-end via RunWithIO.
func TestRunWithIOSlashFallback(t *testing.T) {
	server := NewServer()
	server.RegisterSlash("sayhi", "say hi", func(args string) (any, error) {
		return "hi " + args, nil
	})

	// Use structured data to cover the "data raw JSON" branch of extractSlashArgs.
	// The raw JSON `{"data":"alice"}` decodes to the string "\"alice\"" (with quotes),
	// because cmd.Data is the raw bytes of the JSON value.
	input := `{"id":"3","type":"sayhi","data":"alice"}` + "\n"
	var out bytes.Buffer
	if err := server.RunWithIO(strings.NewReader(input), &out); err != nil {
		t.Fatalf("RunWithIO returned error: %v", err)
	}

	var resp RPCResponse
	if err := json.Unmarshal(bytes.TrimRight(out.Bytes(), "\n"), &resp); err != nil {
		t.Fatalf("could not parse response: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected success, got %+v", resp)
	}
	// Data should be the string "hi \"alice\"" — round-trip is JSON, so it'll be decoded into a string.
	got, ok := resp.Data.(string)
	if !ok {
		t.Fatalf("expected string data, got %T: %+v", resp.Data, resp.Data)
	}
	if got != `hi "alice"` {
		t.Errorf("expected 'hi \"alice\"', got %q", got)
	}
}

// TestResponseJSONShape verifies that RPCResponse fields round-trip through JSON
// in the expected shape (exercises Marshal of types.go indirectly).
func TestResponseJSONShape(t *testing.T) {
	resp := RPCResponse{
		ID:      "abc",
		Type:    "response",
		Command: "test",
		Success: true,
		Data:    map[string]int{"n": 7},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	for _, k := range []string{"id", "type", "command", "success", "data"} {
		if _, ok := m[k]; !ok {
			t.Errorf("expected key %q in JSON, got %v", k, m)
		}
	}
}

// TestRPCCommandUnmarshal exercises JSON unmarshal of RPCCommand.
func TestRPCCommandUnmarshal(t *testing.T) {
	raw := `{"id":"x","type":"prompt","message":"hello","data":{"k":"v"}}`
	var cmd RPCCommand
	if err := json.Unmarshal([]byte(raw), &cmd); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if cmd.ID != "x" || cmd.Type != "prompt" || cmd.Message != "hello" {
		t.Errorf("unexpected command: %+v", cmd)
	}
	if string(cmd.Data) == "" {
		t.Error("expected non-empty data")
	}
}

// TestRegisterOverwrite ensures later Register calls overwrite earlier ones.
func TestRegisterOverwrite(t *testing.T) {
	server := NewServer()
	server.Register("custom", func(cmd RPCCommand) (any, error) {
		return "first", nil
	})
	server.Register("custom", func(cmd RPCCommand) (any, error) {
		return "second", nil
	})

	resp := server.handleCommand(RPCCommand{Type: "custom"})
	if !resp.Success {
		t.Errorf("expected success, got %+v", resp)
	}
	if resp.Data != "second" {
		t.Errorf("expected 'second', got %v", resp.Data)
	}
}

// TestRegisterConcurrent exercises the mutex around Register/HasHandler.
func TestRegisterConcurrent(t *testing.T) {
	server := NewServer()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		name := "c" + string(rune('A'+i%26))
		go func(n string) {
			defer wg.Done()
			server.Register(n, func(RPCCommand) (any, error) { return nil, nil })
			_ = server.HasHandler(n)
		}(name)
	}
	wg.Wait()
}
