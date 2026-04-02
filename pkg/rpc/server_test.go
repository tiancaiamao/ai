package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/agent"
)

// TestRPCServerCommands tests RPC command handling via the registry.
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

	reg := server.Registry()

	reg.Register(CommandPrompt, func(ctx context.Context, cmd agent.Command) (any, error) {
		promptCalled = true
		commandCount++
		return nil, nil
	}, agent.CommandMeta{Name: CommandPrompt})

	reg.Register(CommandSteer, func(ctx context.Context, cmd agent.Command) (any, error) {
		steerCalled = true
		commandCount++
		return nil, nil
	}, agent.CommandMeta{Name: CommandSteer})

	reg.Register(CommandFollowUp, func(ctx context.Context, cmd agent.Command) (any, error) {
		followUpCalled = true
		commandCount++
		return nil, nil
	}, agent.CommandMeta{Name: CommandFollowUp})

	reg.Register(CommandAbort, func(ctx context.Context, cmd agent.Command) (any, error) {
		abortCalled = true
		commandCount++
		return nil, nil
	}, agent.CommandMeta{Name: CommandAbort})

	reg.Register(CommandClearSession, func(ctx context.Context, cmd agent.Command) (any, error) {
		clearCalled = true
		commandCount++
		return nil, nil
	}, agent.CommandMeta{Name: CommandClearSession})

	reg.Register(CommandGetState, func(ctx context.Context, cmd agent.Command) (any, error) {
		getStateCalled = true
		return &SessionState{MessageCount: 42}, nil
	}, agent.CommandMeta{Name: CommandGetState})

	reg.Register(CommandGetMessages, func(ctx context.Context, cmd agent.Command) (any, error) {
		getMessagesCalled = true
		return []any{agentctx.NewUserMessage("test")}, nil
	}, agent.CommandMeta{Name: CommandGetMessages})

	reg.Register(CommandCompact, func(ctx context.Context, cmd agent.Command) (any, error) {
		compactCalled = true
		commandCount++
		return &CompactResult{TokensBefore: 1}, nil
	}, agent.CommandMeta{Name: CommandCompact})

	reg.Register(CommandSetToolCallCutoff, func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Cutoff int `json:"cutoff"`
		}
		json.Unmarshal(cmd.Payload, &payload)
		setToolCallCutoffCalled = payload.Cutoff == 7
		commandCount++
		return nil, nil
	}, agent.CommandMeta{Name: CommandSetToolCallCutoff})

	reg.Register(CommandSetToolSummaryStrategy, func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Strategy string `json:"strategy"`
		}
		json.Unmarshal(cmd.Payload, &payload)
		setToolSummaryStrategyCalled = payload.Strategy == "heuristic"
		commandCount++
		return nil, nil
	}, agent.CommandMeta{Name: CommandSetToolSummaryStrategy})

	reg.Register(CommandSetToolSummaryAutomation, func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Mode string `json:"mode"`
		}
		json.Unmarshal(cmd.Payload, &payload)
		setToolSummaryAutomationCalled = payload.Mode == "fallback"
		commandCount++
		return nil, nil
	}, agent.CommandMeta{Name: CommandSetToolSummaryAutomation})

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

	if commandCount != 9 {
		t.Errorf("Expected 9 commands to be handled, got %d", commandCount)
	}
}

// TestRPCCommandParsing tests that commands are properly parsed.
func TestRPCCommandParsing(t *testing.T) {
	server := NewServer()

	// Ping is handled specially in handleCommand
	cmd := RPCCommand{Type: CommandPing}
	resp := server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Ping command failed: %s", resp.Error)
	}
}

// TestEmitEvent tests event emission.
func TestEmitEvent(t *testing.T) {
	server := NewServer()

	pr, pw := io.Pipe()
	defer pr.Close()
	defer pw.Close()

	server.SetOutput(pw)

	go func() {
		server.EmitEvent(map[string]any{"type": "test", "data": 42})
		pw.Close()
	}()

	var output strings.Builder
	io.Copy(&output, pr)

	var event map[string]any
	if err := json.Unmarshal([]byte(output.String()), &event); err != nil {
		t.Fatalf("Failed to parse event: %v", err)
	}
	if event["type"] != "test" {
		t.Errorf("Expected event type 'test', got %v", event["type"])
	}
}

// TestUnknownCommand tests handling of unknown commands.
func TestUnknownCommand(t *testing.T) {
	server := NewServer()

	cmd := RPCCommand{Type: "unknown_command", ID: "1"}
	resp := server.handleCommand(cmd)
	if resp.Success {
		t.Error("Expected failure for unknown command")
	}
	if resp.Error == "" {
		t.Error("Expected error message for unknown command")
	}
}

// TestMissingHandler tests handling of commands with no registered handler.
func TestMissingHandler(t *testing.T) {
	server := NewServer()

	// Don't register a handler for "prompt"
	cmd := RPCCommand{Type: CommandPrompt, Message: "test"}
	resp := server.handleCommand(cmd)
	if resp.Success {
		t.Error("Expected failure for missing handler")
	}
	if resp.Error == "" {
		t.Error("Expected error message for missing handler")
	}
}

// TestServerContext tests the context functionality.
func TestServerContext(t *testing.T) {
	server := NewServer()

	ctx := server.Context()
	if ctx == nil {
		t.Error("Expected non-nil context")
	}

	server.Cancel()

	select {
	case <-ctx.Done():
		// Expected
	case <-time.After(time.Second):
		t.Error("Expected context to be cancelled")
	}
}

// TestResponseFormatting tests response formatting.
func TestResponseFormatting(t *testing.T) {
	server := NewServer()

	resp := server.successResponse("1", "test", map[string]any{"key": "value"})
	if resp.ID != "1" {
		t.Errorf("Expected ID '1', got %s", resp.ID)
	}
	if resp.Command != "test" {
		t.Errorf("Expected command 'test', got %s", resp.Command)
	}
	if !resp.Success {
		t.Error("Expected success")
	}
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("Expected map data, got %T", resp.Data)
	}
	if data["key"] != "value" {
		t.Errorf("Expected key='value', got %v", data["key"])
	}
}

// TestErrorResponse tests error response formatting.
func TestErrorResponse(t *testing.T) {
	server := NewServer()

	resp := server.errorResponse("2", "test_cmd", "something went wrong")
	if resp.ID != "2" {
		t.Errorf("Expected ID '2', got %s", resp.ID)
	}
	if resp.Command != "test_cmd" {
		t.Errorf("Expected command 'test_cmd', got %s", resp.Command)
	}
	if resp.Success {
		t.Error("Expected failure")
	}
	if resp.Error != "something went wrong" {
		t.Errorf("Expected error 'something went wrong', got %s", resp.Error)
	}
}

// TestConcurrentCommands tests concurrent command handling.
func TestConcurrentCommands(t *testing.T) {
	server := NewServer()

	var count atomic.Int32
	server.Registry().Register(CommandGetState, func(ctx context.Context, cmd agent.Command) (any, error) {
		count.Add(1)
		return &SessionState{MessageCount: int(count.Load())}, nil
	}, agent.CommandMeta{Name: CommandGetState})

	// Handle many concurrent commands
	done := make(chan bool)
	for i := 0; i < 100; i++ {
		go func() {
			cmd := RPCCommand{Type: CommandGetState}
			resp := server.handleCommand(cmd)
			if !resp.Success {
				t.Errorf("Concurrent command failed: %s", resp.Error)
			}
			done <- true
		}()
	}

	for i := 0; i < 100; i++ {
		<-done
	}

	if count.Load() != 100 {
		t.Errorf("Expected 100 invocations, got %d", count.Load())
	}
}

// TestCommandWithDataField tests that data from cmd.Data is passed through.
func TestCommandWithDataField(t *testing.T) {
	server := NewServer()

	var received string
	server.Registry().Register("test_data", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Key string `json:"key"`
		}
		json.Unmarshal(cmd.Payload, &payload)
		received = payload.Key
		return nil, nil
	}, agent.CommandMeta{Name: "test_data"})

	cmd := RPCCommand{
		Type: "test_data",
		Data: json.RawMessage(`{"key": "value123"}`),
	}
	resp := server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Command with data failed: %s", resp.Error)
	}
	if received != "value123" {
		t.Errorf("Expected 'value123', got %q", received)
	}
}

// TestServerContextCancel tests context cancellation propagation.
func TestServerContextCancel(t *testing.T) {
	server := NewServer()
	ctx := server.Context()

	server.Cancel()

	select {
	case <-ctx.Done():
		// Expected
	case <-time.After(time.Second):
		t.Fatal("Context should have been cancelled")
	}
}

// TestErrorHandlingInHandlers tests that handler errors are properly returned.
func TestErrorHandlingInHandlers(t *testing.T) {
	server := NewServer()

	server.Registry().Register(CommandPrompt, func(ctx context.Context, cmd agent.Command) (any, error) {
		return nil, context.DeadlineExceeded
	}, agent.CommandMeta{Name: CommandPrompt})

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
// while a long-running prompt command is being processed.
func TestAsyncDispatchDuringPrompt(t *testing.T) {
	promptStarted := make(chan struct{})
	promptDone := make(chan struct{})

	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	defer inWriter.Close()
	defer outReader.Close()

	server := NewServer()
	server.Registry().Register(CommandPrompt, func(ctx context.Context, cmd agent.Command) (any, error) {
		close(promptStarted)
		select {
		case <-time.After(2 * time.Second):
		case <-promptDone:
		}
		return nil, nil
	}, agent.CommandMeta{Name: CommandPrompt})

	server.Registry().Register(CommandSteer, func(ctx context.Context, cmd agent.Command) (any, error) {
		return nil, nil
	}, agent.CommandMeta{Name: CommandSteer})

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
	case <-time.After(1 * time.Second):
		t.Fatal("Prompt never started")
	}

	// Send steer while prompt is running
	steerCmd := `{"type":"steer","data":{"message":"steer msg"},"id":"s1"}` + "\n"
	inWriter.Write([]byte(steerCmd))

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
			t.Fatalf("timeout (steer=%v, prompt=%v)", gotSteer, gotPrompt)
		default:
		}
	}

	if !gotSteer {
		t.Error("Steer response never received — scanner loop was blocked!")
	}
	if !gotPrompt {
		t.Error("Prompt response never received")
	}

	close(promptDone)
	inWriter.Close()
	outWriter.Close()
}

// TestSyncCommandsNotAsync verifies that quick commands return immediately.
func TestSyncCommandsNotAsync(t *testing.T) {
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	defer inReader.Close()
	defer outReader.Close()

	server := NewServer()
	server.Registry().Register(CommandGetState, func(ctx context.Context, cmd agent.Command) (any, error) {
		return &SessionState{MessageCount: 5}, nil
	}, agent.CommandMeta{Name: CommandGetState})

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.RunWithIO(inReader, outWriter)
	}()

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

// TestBuildPayload tests that cmd.Message is merged into the payload.
func TestBuildPayload(t *testing.T) {
	server := NewServer()

	// Test with message and no data
	cmd := RPCCommand{Message: "hello"}
	payload := server.buildPayload(cmd)
	var result map[string]any
	json.Unmarshal(payload, &result)
	if result["message"] != "hello" {
		t.Errorf("Expected message 'hello', got %v", result["message"])
	}

	// Test with message and existing data
	cmd = RPCCommand{
		Message: "world",
		Data:    json.RawMessage(`{"key": "value"}`),
	}
	payload = server.buildPayload(cmd)
	json.Unmarshal(payload, &result)
	if result["message"] != "world" {
		t.Errorf("Expected message 'world', got %v", result["message"])
	}
	if result["key"] != "value" {
		t.Errorf("Expected key 'value', got %v", result["key"])
	}

	// Test with no message, only data
	cmd = RPCCommand{
		Data: json.RawMessage(`{"key": "value"}`),
	}
	payload = server.buildPayload(cmd)
	json.Unmarshal(payload, &result)
	if result["key"] != "value" {
		t.Errorf("Expected key 'value', got %v", result["key"])
	}
}

// TestNewServerWithRegistry tests sharing a registry.
func TestNewServerWithRegistry(t *testing.T) {
	reg := agent.NewCommandRegistry()
	reg.Register("custom", func(ctx context.Context, cmd agent.Command) (any, error) {
		return "custom result", nil
	}, agent.CommandMeta{Name: "custom"})

	server := NewServerWithRegistry(reg)

	cmd := RPCCommand{Type: "custom"}
	resp := server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Custom command failed: %s", resp.Error)
	}
	if resp.Data != "custom result" {
		t.Errorf("Expected 'custom result', got %v", resp.Data)
	}
}

// TestSetRegistry tests replacing the registry at runtime.
func TestSetRegistry(t *testing.T) {
	server := NewServer()

	// Initially no handlers
	cmd := RPCCommand{Type: "test"}
	resp := server.handleCommand(cmd)
	if resp.Success {
		t.Error("Expected failure with no registry")
	}

	// Set a new registry with a handler
	reg := agent.NewCommandRegistry()
	reg.Register("test", func(ctx context.Context, cmd agent.Command) (any, error) {
		return "works", nil
	}, agent.CommandMeta{Name: "test"})
	server.SetRegistry(reg)

	resp = server.handleCommand(cmd)
	if !resp.Success {
		t.Errorf("Expected success after SetRegistry: %s", resp.Error)
	}
	if resp.Data != "works" {
		t.Errorf("Expected 'works', got %v", resp.Data)
	}
}
