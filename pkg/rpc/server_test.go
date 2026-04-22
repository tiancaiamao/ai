package rpc

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestRPCServerCommands tests RPC command handling with the new Register API.
func TestRPCServerCommands(t *testing.T) {
	server := NewServer()

	promptCalled := false
	steerCalled := false
	followUpCalled := false
	abortCalled := false

	// Register protocol command handlers
	server.Register(CommandPrompt, func(cmd RPCCommand) (any, error) {
		promptCalled = true
		return nil, nil
	})

	server.Register(CommandSteer, func(cmd RPCCommand) (any, error) {
		steerCalled = true
		return nil, nil
	})

	server.Register(CommandFollowUp, func(cmd RPCCommand) (any, error) {
		followUpCalled = true
		return nil, nil
	})

	server.Register(CommandAbort, func(cmd RPCCommand) (any, error) {
		abortCalled = true
		return nil, nil
	})

	// Test prompt command
	cmd := RPCCommand{Type: CommandPrompt, Message: "Test message"}
	resp := server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Prompt command failed: %s", resp.Error)
	}
	if !promptCalled {
		t.Error("Prompt handler was not called")
	}

	// Test steer command
	cmd = RPCCommand{Type: CommandSteer}
	cmd.Data, _ = json.Marshal(map[string]string{"message": "Steer message"})
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Steer command failed: %s", resp.Error)
	}
	if !steerCalled {
		t.Error("Steer handler was not called")
	}

	// Test follow_up command
	cmd = RPCCommand{Type: CommandFollowUp}
	cmd.Data, _ = json.Marshal(map[string]string{"message": "Follow-up message"})
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Follow-up command failed: %s", resp.Error)
	}
	if !followUpCalled {
		t.Error("Follow-up handler was not called")
	}

	// Test abort command
	cmd = RPCCommand{Type: CommandAbort}
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Abort command failed: %s", resp.Error)
	}
	if !abortCalled {
		t.Error("Abort handler was not called")
	}
}

// TestRPCServerPing tests that ping is pre-registered.
func TestRPCServerPing(t *testing.T) {
	server := NewServer()

	cmd := RPCCommand{Type: CommandPing}
	resp := server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Ping command failed: %s", resp.Error)
	}
}

// TestRPCServerReturnData tests that handlers can return data.
func TestRPCServerReturnData(t *testing.T) {
	server := NewServer()

	server.Register(CommandPrompt, func(cmd RPCCommand) (any, error) {
		return map[string]string{"echo": cmd.Message}, nil
	})

	cmd := RPCCommand{Type: CommandPrompt, Message: "hello"}
	resp := server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Command failed: %s", resp.Error)
	}

	data, ok := resp.Data.(map[string]string)
	if !ok {
		t.Fatalf("Expected map[string]string, got %T", resp.Data)
	}
	if data["echo"] != "hello" {
		t.Errorf("Expected echo 'hello', got '%s'", data["echo"])
	}
}

// TestRPCServerHandlerError tests error propagation from handlers.
func TestRPCServerHandlerError(t *testing.T) {
	server := NewServer()

	server.Register(CommandPrompt, func(cmd RPCCommand) (any, error) {
		return nil, context.DeadlineExceeded
	})

	cmd := RPCCommand{Type: CommandPrompt, Message: "test"}
	resp := server.handleCommand(cmd)
	if resp.Success {
		t.Error("Expected error response when handler fails")
	}
	if resp.Error == "" {
		t.Error("Expected error message to be set")
	}
}

// TestMissingHandler tests commands without registered handlers.
func TestMissingHandler(t *testing.T) {
	server := NewServer()

	cmd := RPCCommand{Type: CommandPrompt}
	resp := server.handleCommand(cmd)

	if resp.Success {
		t.Error("Expected error when handler not registered")
	}
		if !strings.Contains(strings.ToLower(resp.Error), "handler registered") {
		t.Errorf("Expected error about missing handler, got '%s'", resp.Error)
	}
}

// TestServerContext tests server context lifecycle.
func TestServerContext(t *testing.T) {
	server := NewServer()

	ctx := server.Context()
	if ctx == nil {
		t.Fatal("Context should not be nil")
	}

	// Context should be initially active
	select {
	case <-ctx.Done():
		t.Error("Context should not be cancelled yet")
	default:
		// Expected
	}
}

// TestResponseFormatting tests response format.
func TestResponseFormatting(t *testing.T) {
	server := NewServer()

	resp := server.successResponse("test-id", "test_command", map[string]string{"key": "value"})

	if resp.Type != "response" {
		t.Errorf("Expected type 'response', got '%s'", resp.Type)
	}
	if resp.Command != "test_command" {
		t.Errorf("Expected command 'test_command', got '%s'", resp.Command)
	}
	if resp.ID != "test-id" {
		t.Errorf("Expected id 'test-id', got '%s'", resp.ID)
	}
	if !resp.Success {
		t.Error("Expected success to be true")
	}

	data, ok := resp.Data.(map[string]string)
	if !ok {
		t.Fatal("Expected data to be map[string]string")
	}
	if data["key"] != "value" {
		t.Errorf("Expected data key 'value', got '%s'", data["key"])
	}
}

// TestErrorResponse tests error response format.
func TestErrorResponse(t *testing.T) {
	server := NewServer()

	resp := server.errorResponse("test-id", "test_command", "test error")

	if resp.Success {
		t.Error("Expected success to be false for error response")
	}
	if resp.Error != "test error" {
		t.Errorf("Expected error message 'test error', got '%s'", resp.Error)
	}
	if resp.ID != "test-id" {
		t.Errorf("Expected id 'test-id', got '%s'", resp.ID)
	}
}

// TestConcurrentCommands tests concurrent command handling.
func TestConcurrentCommands(t *testing.T) {
	server := NewServer()

	promptCount := 0
	server.Register(CommandPrompt, func(cmd RPCCommand) (any, error) {
		promptCount++
		time.Sleep(10 * time.Millisecond) // Simulate work
		return nil, nil
	})

	// Send concurrent commands
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			cmd := RPCCommand{Type: CommandPrompt, Message: "test"}
			server.handleCommand(cmd)
			done <- true
		}()
	}

	// Wait for all commands
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for concurrent commands")
		}
	}

	if promptCount != 10 {
		t.Errorf("Expected 10 prompts, got %d", promptCount)
	}
}

// TestServerContextCancel tests server context cancellation.
func TestServerContextCancel(t *testing.T) {
	server := NewServer()
	ctx := server.Context()

	// Cancel context
	server.cancel()

	// Context should be done now
	select {
	case <-ctx.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Error("Context should be cancelled after calling cancel()")
	}
}

// TestCommandWithDataField tests commands using data field.
func TestCommandWithDataField(t *testing.T) {
	server := NewServer()

	var receivedMessage string
	server.Register(CommandPrompt, func(cmd RPCCommand) (any, error) {
		receivedMessage = cmd.Message
		return nil, nil
	})

	// Test with message field
	cmd := RPCCommand{
		Type:    CommandPrompt,
		Message: "direct message",
		Data:    json.RawMessage(`{"message": "data message"}`),
	}
	resp := server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Command failed: %s", resp.Error)
	}
	if receivedMessage != "direct message" {
		t.Errorf("Expected 'direct message', got '%s'", receivedMessage)
	}
}

// TestHandlerReturnTypes tests various return types from handlers.
func TestHandlerReturnTypes(t *testing.T) {
	server := NewServer()

		// Handler that returns a struct
	server.Register("get_state", func(cmd RPCCommand) (any, error) {
		return &SessionState{
			MessageCount: 42,
		}, nil
	})

	cmd := RPCCommand{Type: "get_state"}
	resp := server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Get state command failed: %s", resp.Error)
	}

	stateData, ok := resp.Data.(*SessionState)
	if !ok {
		t.Fatalf("Expected *SessionState, got %T", resp.Data)
	}
	if stateData.MessageCount != 42 {
		t.Errorf("Expected message count 42, got %d", stateData.MessageCount)
	}

	// Handler that returns a slice
	server.Register("get_messages", func(cmd RPCCommand) (any, error) {
		return []any{
			agentctx.NewUserMessage("test"),
		}, nil
	})

	cmd = RPCCommand{Type: "get_messages"}
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Get messages command failed: %s", resp.Error)
	}
}

// Ensure unused imports are referenced
var (
	_ io.Reader
	_ context.Context
)