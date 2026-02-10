package rpc

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// TestRPCServerCommands tests RPC command handling.
func TestRPCServerCommands(t *testing.T) {
	server := NewServer()

	commandCount := 0
	promptCalled := false
	steerCalled := false
	followUpCalled := false
	abortCalled := false
	clearCalled := false
	getStateCalled := false
	getMessagesCalled := false
	compactCalled := false

	// Set up handlers
	server.SetPromptHandler(func(message string) error {
		promptCalled = true
		commandCount++
		return nil
	})

	server.SetSteerHandler(func(message string) error {
		steerCalled = true
		commandCount++
		return nil
	})

	server.SetFollowUpHandler(func(message string) error {
		followUpCalled = true
		commandCount++
		return nil
	})

	server.SetAbortHandler(func() error {
		abortCalled = true
		commandCount++
		return nil
	})

	server.SetClearSessionHandler(func() error {
		clearCalled = true
		commandCount++
		return nil
	})

	server.SetGetStateHandler(func() (*SessionState, error) {
		getStateCalled = true
		return &SessionState{
			MessageCount: 42,
		}, nil
	})

	server.SetGetMessagesHandler(func() ([]any, error) {
		getMessagesCalled = true
		return []any{
			agent.NewUserMessage("test"),
		}, nil
	})

	server.SetCompactHandler(func() (*CompactResult, error) {
		compactCalled = true
		commandCount++
		return &CompactResult{TokensBefore: 1}, nil
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

	// Test clear_session command
	cmd = RPCCommand{Type: CommandClearSession}
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Clear session command failed: %s", resp.Error)
	}
	if !clearCalled {
		t.Error("Clear session handler was not called")
	}

	// Test get_state command
	cmd = RPCCommand{Type: CommandGetState}
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Get state command failed: %s", resp.Error)
	}
	if !getStateCalled {
		t.Error("Get state handler was not called")
	}

	// Verify response data
	stateData, ok := resp.Data.(*SessionState)
	if !ok {
		t.Fatalf("Expected *SessionState for state data, got %T", resp.Data)
	}
	if stateData.MessageCount != 42 {
		t.Errorf("Expected message count 42, got %d", stateData.MessageCount)
	}

	// Test get_messages command
	cmd = RPCCommand{Type: CommandGetMessages}
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Get messages command failed: %s", resp.Error)
	}
	if !getMessagesCalled {
		t.Error("Get messages handler was not called")
	}

	// Test compact command
	cmd = RPCCommand{Type: CommandCompact}
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Compact command failed: %s", resp.Error)
	}
	if !compactCalled {
		t.Error("Compact handler was not called")
	}

	// Verify total command count
	if commandCount != 6 {
		t.Errorf("Expected 6 commands to be called, got %d", commandCount)
	}
}

// TestRPCCommandParsing tests parsing various command formats.
func TestRPCCommandParsing(t *testing.T) {
	// Test command with direct message field
	cmdJSON := `{"type": "prompt", "message": "Direct message", "id": "test-1"}`
	var cmd RPCCommand
	err := json.Unmarshal([]byte(cmdJSON), &cmd)
	if err != nil {
		t.Fatalf("Failed to parse command: %v", err)
	}

	if cmd.Type != CommandPrompt {
		t.Errorf("Expected type 'prompt', got '%s'", cmd.Type)
	}
	if cmd.Message != "Direct message" {
		t.Errorf("Expected message 'Direct message', got '%s'", cmd.Message)
	}
	if cmd.ID != "test-1" {
		t.Errorf("Expected id 'test-1', got '%s'", cmd.ID)
	}

	// Test command with data field
	cmdJSON = `{"type": "steer", "data": {"message": "Data message"}}`
	err = json.Unmarshal([]byte(cmdJSON), &cmd)
	if err != nil {
		t.Fatalf("Failed to parse command with data: %v", err)
	}

	if cmd.Type != CommandSteer {
		t.Errorf("Expected type 'steer', got '%s'", cmd.Type)
	}

	var data struct {
		Message string `json:"message"`
	}
	err = json.Unmarshal(cmd.Data, &data)
	if err != nil {
		t.Fatalf("Failed to parse data: %v", err)
	}
	if data.Message != "Data message" {
		t.Errorf("Expected data message 'Data message', got '%s'", data.Message)
	}
}

// TestEmitEvent tests event emission.
func TestEmitEvent(t *testing.T) {
	server := NewServer()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	server.SetOutput(w)

	// Emit event
	server.EmitEvent(map[string]any{
		"type":  "test_event",
		"value": "test_value",
	})

	// Restore stdout
	w.Close()
	os.Stdout = oldStdout

	// Read output
	output, _ := io.ReadAll(r)
	var emitted map[string]any
	err := json.Unmarshal(output, &emitted)
	if err != nil {
		t.Fatalf("Failed to parse emitted event: %v", err)
	}

	if emitted["type"] != "test_event" {
		t.Errorf("Expected event type 'test_event', got '%v'", emitted["type"])
	}
}

// TestUnknownCommand tests handling of unknown commands.
func TestUnknownCommand(t *testing.T) {
	server := NewServer()

	cmd := RPCCommand{Type: "unknown_command"}
	resp := server.handleCommand(cmd)

	if resp.Success {
		t.Error("Expected error for unknown command")
	}
	if !strings.Contains(resp.Error, "Unknown command") {
		t.Errorf("Expected error message to contain 'Unknown command', got '%s'", resp.Error)
	}
}

// TestMissingHandler tests commands without registered handlers.
func TestMissingHandler(t *testing.T) {
	server := NewServer()

	// Don't set any handlers, test error messages
	cmd := RPCCommand{Type: CommandPrompt}
	resp := server.handleCommand(cmd)

	if resp.Success {
		t.Error("Expected error when handler not registered")
	}
	if !strings.Contains(resp.Error, "No prompt handler registered") {
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

	// Close server (which should cancel context)
	// Note: We can't directly call Close(), but the context is available
}

// TestResponseFormatting tests response format.
func TestResponseFormatting(t *testing.T) {
	server := NewServer()

	// Create a response
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
	server.SetPromptHandler(func(message string) error {
		promptCount++
		time.Sleep(10 * time.Millisecond) // Simulate work
		return nil
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

// TestCommandWithDataField tests commands using data field.
func TestCommandWithDataField(t *testing.T) {
	server := NewServer()

	var receivedMessage string
	server.SetPromptHandler(func(message string) error {
		receivedMessage = message
		return nil
	})

	// Test with message field (should be preferred)
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

	// Test with only data field
	cmd = RPCCommand{
		Type: CommandPrompt,
		Data: json.RawMessage(`{"message": "data message"}`),
	}
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Command failed: %s", resp.Error)
	}
	if receivedMessage != "data message" {
		t.Errorf("Expected 'data message', got '%s'", receivedMessage)
	}
}

// TestServerContextCancel tests server context cancellation.
func TestServerContextCancel(t *testing.T) {
	server := NewServer()
	ctx := server.Context()
	_ = ctx // Use ctx to avoid unused variable warning

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

// TestErrorHandlingInHandlers tests error handling in command handlers.
func TestErrorHandlingInHandlers(t *testing.T) {
	server := NewServer()

	// Handler that returns an error
	server.SetPromptHandler(func(message string) error {
		return context.DeadlineExceeded
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
