package rpc

import (
	"bufio"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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
	setToolCallCutoffCalled := false
	setToolSummaryStrategyCalled := false
	setToolSummaryAutomationCalled := false

	// Set up handlers
	server.SetPromptHandler(func(req PromptRequest) error {
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
			agentctx.NewUserMessage("test"),
		}, nil
	})

	server.SetCompactHandler(func() (*CompactResult, error) {
		compactCalled = true
		commandCount++
		return &CompactResult{TokensBefore: 1}, nil
	})

	server.SetSetToolCallCutoffHandler(func(cutoff int) error {
		setToolCallCutoffCalled = cutoff == 7
		commandCount++
		return nil
	})

	server.SetSetToolSummaryStrategyHandler(func(strategy string) error {
		setToolSummaryStrategyCalled = strategy == "heuristic"
		commandCount++
		return nil
	})

	server.SetSetToolSummaryAutomationHandler(func(mode string) error {
		setToolSummaryAutomationCalled = mode == "fallback"
		commandCount++
		return nil
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

	// Test set_tool_call_cutoff command
	cmd = RPCCommand{Type: CommandSetToolCallCutoff}
	cmd.Data, _ = json.Marshal(map[string]int{"cutoff": 7})
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("set_tool_call_cutoff command failed: %s", resp.Error)
	}
	if !setToolCallCutoffCalled {
		t.Error("set_tool_call_cutoff handler was not called with expected value")
	}

	// Test set_tool_summary_strategy command
	cmd = RPCCommand{Type: CommandSetToolSummaryStrategy}
	cmd.Data, _ = json.Marshal(map[string]string{"strategy": "heuristic"})
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("set_tool_summary_strategy command failed: %s", resp.Error)
	}
	if !setToolSummaryStrategyCalled {
		t.Error("set_tool_summary_strategy handler was not called with expected value")
	}

	// Test set_tool_summary_automation command
	cmd = RPCCommand{Type: CommandSetToolSummaryAutomation}
	cmd.Data, _ = json.Marshal(map[string]string{"mode": "fallback"})
	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("set_tool_summary_automation command failed: %s", resp.Error)
	}
	if !setToolSummaryAutomationCalled {
		t.Error("set_tool_summary_automation handler was not called with expected value")
	}

	// Verify total command count
	if commandCount != 9 {
		t.Errorf("Expected 9 commands to be called, got %d", commandCount)
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

	var promptCount atomic.Int32
	server.SetPromptHandler(func(req PromptRequest) error {
		promptCount.Add(1)
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

	if got := promptCount.Load(); got != 10 {
		t.Errorf("Expected 10 prompts, got %d", got)
	}
}

// TestCommandWithDataField tests commands using data field.
func TestCommandWithDataField(t *testing.T) {
	server := NewServer()

	var receivedMessage string
	server.SetPromptHandler(func(req PromptRequest) error {
		receivedMessage = req.Message
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
	server.SetPromptHandler(func(req PromptRequest) error {
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

// TestAsyncDispatchDuringPrompt verifies that the scanner loop is NOT blocked
// while a long-running prompt command is being processed. This is the core fix
// for the "RPC server single-threaded blocking" issue.
func TestAsyncDispatchDuringPrompt(t *testing.T) {
	promptStarted := make(chan struct{})
	promptDone := make(chan struct{})

	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	defer inWriter.Close()
	defer outReader.Close()

	server := NewServer()
	server.SetPromptHandler(func(req PromptRequest) error {
		close(promptStarted)
		// Simulate long-running LLM call
		select {
		case <-time.After(2 * time.Second):
		case <-promptDone:
		}
		return nil
	})
	server.SetSteerHandler(func(message string) error {
		return nil
	})

	// Start the server in a goroutine
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.RunWithIO(inReader, outWriter)
	}()

	// Send a prompt command
	promptCmd := `{"type":"prompt","message":"hello","id":"p1"}` + "\n"
	inWriter.Write([]byte(promptCmd))

	// Wait for prompt to start processing
	select {
	case <-promptStarted:
		// Prompt is running in a goroutine — scanner loop should be free
	case <-time.After(1 * time.Second):
		t.Fatal("Prompt never started")
	}

	// Now send a steer command while prompt is still running.
	// Before the fix, this would block on io.Pipe because the scanner
	// loop was stuck in handleCommand. After the fix, it should succeed.
	steerCmd := `{"type":"steer","data":{"message":"steer msg"},"id":"s1"}` + "\n"
	inWriter.Write([]byte(steerCmd))

	// Read responses from the server. We should see the steer response
	// arrive before (or around the same time as) the prompt response,
	// proving the scanner loop was not blocked.
	outScanner := bufio.NewScanner(outReader)
	outScanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)

	gotSteer := false
	gotPrompt := false
	deadline := time.After(3 * time.Second)

	for (!gotSteer || !gotPrompt) && outScanner.Scan() {
		line := outScanner.Text()
		var resp RPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.Command == CommandSteer {
			gotSteer = true
		}
		if resp.Command == CommandPrompt {
			gotPrompt = true
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for responses (steer=%v, prompt=%v)", gotSteer, gotPrompt)
		default:
		}
	}

	if !gotSteer {
		t.Error("Steer response never received — scanner loop was blocked!")
	}
	if !gotPrompt {
		t.Error("Prompt response never received")
	}

	// Clean up
	close(promptDone)
	inWriter.Close()
	outWriter.Close()
}

// TestSyncCommandsNotAsync verifies that quick commands (get_state, ping, etc.)
// are still handled synchronously and return responses immediately.
func TestSyncCommandsNotAsync(t *testing.T) {
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	defer inReader.Close()
	defer outReader.Close()

	server := NewServer()
	server.SetGetStateHandler(func() (*SessionState, error) {
		return &SessionState{MessageCount: 5}, nil
	})

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.RunWithIO(inReader, outWriter)
	}()

	// Send get_state (sync command)
	cmd := `{"type":"get_state","id":"gs1"}` + "\n"
	inWriter.Write([]byte(cmd))

	outScanner := bufio.NewScanner(outReader)
	outScanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)

	if !outScanner.Scan() {
		t.Fatal("Expected response from get_state")
	}

	var resp RPCResponse
	if err := json.Unmarshal([]byte(outScanner.Text()), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if resp.Command != CommandGetState {
		t.Errorf("Expected get_state response, got %s", resp.Command)
	}
	if !resp.Success {
		t.Errorf("Expected success, got error: %s", resp.Error)
	}

	inWriter.Close()
	outWriter.Close()
}

// TestIsAsyncCommandClassification tests the isAsyncCommand helper.
func TestIsAsyncCommandClassification(t *testing.T) {
	async := map[string]bool{
		CommandPrompt:   true,
		CommandSteer:    true,
		CommandFollowUp: true,
		CommandBash:     true,
		CommandCompact:  true,
	}
	for cmd, expected := range async {
		if isAsyncCommand(cmd) != expected {
			t.Errorf("isAsyncCommand(%q) = %v, want %v", cmd, !expected, expected)
		}
	}

	sync := []string{
		CommandAbort, CommandPing, CommandGetState, CommandGetMessages,
		CommandSetModel, CommandNewSession, CommandSetSteeringMode,
	}
	for _, cmd := range sync {
		if isAsyncCommand(cmd) {
			t.Errorf("isAsyncCommand(%q) = true, want false", cmd)
		}
	}
}
