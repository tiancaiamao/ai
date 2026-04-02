package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// Server handles RPC communication via stdin/stdout.
type Server struct {
	mu       sync.Mutex // protects server-wide state (context, wait group)
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	writer   sync.Mutex
	output   *bufio.Writer
	registry *agent.CommandRegistry
}

// NewServer creates a new RPC server with its own command registry.
func NewServer() *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:      ctx,
		cancel:   cancel,
		registry: agent.NewCommandRegistry(),
	}
}

// NewServerWithRegistry creates a new RPC server using an existing command registry.
func NewServerWithRegistry(registry *agent.CommandRegistry) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:      ctx,
		cancel:   cancel,
		registry: registry,
	}
}

// Registry returns the command registry for registering handlers.
func (s *Server) Registry() *agent.CommandRegistry {
	return s.registry
}

// SetRegistry replaces the command registry (used to share a registry between components).
func (s *Server) SetRegistry(registry *agent.CommandRegistry) {
	s.registry = registry
}

// Context returns the server's context.
func (s *Server) Context() context.Context {
	return s.ctx
}

// Cancel cancels the server context.
func (s *Server) Cancel() {
	s.cancel()
}

// Run starts the RPC server, reading commands from stdin and writing responses to stdout.
func (s *Server) Run() error {
	return s.RunWithIO(os.Stdin, os.Stdout)
}

// RunWithIO starts the RPC server using the provided reader and writer.
func (s *Server) RunWithIO(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 4*1024*1024)
	scanner.Buffer(buf, 16*1024*1024)
	s.setOutput(writer)

	for scanner.Scan() {
		line := scanner.Bytes()

		var cmd RPCCommand
		if err := json.Unmarshal(line, &cmd); err != nil {
			s.sendError("", fmt.Sprintf("Failed to parse command: %v", err))
			continue
		}

		if isAsyncCommand(cmd.Type) {
			s.dispatchAsync(cmd)
		} else {
			resp := s.handleCommand(cmd)
			s.sendResponse(resp)
		}
	}

	slog.Info("[RPC] Waiting for agent to complete...")
	s.wg.Wait()
	slog.Info("[RPC] All tasks completed")

	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// isAsyncCommand returns true for commands whose handlers run for a long time.
func isAsyncCommand(cmdType string) bool {
	switch cmdType {
	case CommandPrompt, CommandSteer, CommandFollowUp, CommandBash, CommandCompact:
		return true
	}
	return false
}

// dispatchAsync runs a long-running command in a goroutine.
func (s *Server) dispatchAsync(cmd RPCCommand) {
	s.mu.Lock()
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.wg.Done()
			s.mu.Unlock()
		}()
		resp := s.handleCommand(cmd)
		s.sendResponse(resp)
	}()
}

// handleCommand processes a single command via the registry.
func (s *Server) handleCommand(cmd RPCCommand) RPCResponse {
	// Special case: ping is handled locally (no registry lookup)
	if cmd.Type == CommandPing {
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"ok": true})
	}

	// Build agent.Command from RPCCommand.
	// Merge cmd.Message into payload for commands that support it.
	payload := s.buildPayload(cmd)

	agentCmd := agent.Command{
		Name:    cmd.Type,
		Payload: payload,
	}

	result, err := s.registry.Handle(s.ctx, agentCmd)
	if err != nil {
		return s.errorResponse(cmd.ID, cmd.Type, err.Error())
	}
	return s.successResponse(cmd.ID, cmd.Type, result)
}

// buildPayload merges cmd.Message and cmd.Data into a single json.RawMessage.
// For commands that use cmd.Message (prompt, steer, follow_up), the message
// field is injected into the data object.
func (s *Server) buildPayload(cmd RPCCommand) json.RawMessage {
	if cmd.Message == "" {
		return cmd.Data
	}

	// Parse existing data as map, inject message, re-serialize
	data := make(map[string]any)
	if len(cmd.Data) > 0 {
		json.Unmarshal(cmd.Data, &data)
	}
	data["message"] = cmd.Message
	payload, _ := json.Marshal(data)
	return payload
}

// successResponse creates a successful response.
func (s *Server) successResponse(id, command string, data any) RPCResponse {
	return RPCResponse{
		ID:      id,
		Type:    "response",
		Command: command,
		Success: true,
		Data:    data,
	}
}

// errorResponse creates an error response.
func (s *Server) errorResponse(id, command, errMsg string) RPCResponse {
	return RPCResponse{
		ID:      id,
		Type:    "response",
		Command: command,
		Success: false,
		Error:   errMsg,
	}
}

func (s *Server) sendResponse(resp RPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	s.writeJSON(data)
}

func (s *Server) sendError(cmdID, errMsg string) {
	resp := s.errorResponse(cmdID, "", errMsg)
	s.sendResponse(resp)
}

// EmitEvent emits an event to stdout as JSON.
func (s *Server) EmitEvent(event any) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	s.writeJSON(data)
}

// SetOutput sets the writer used for responses and events.
func (s *Server) SetOutput(writer io.Writer) {
	s.setOutput(writer)
}

func (s *Server) setOutput(writer io.Writer) {
	s.writer.Lock()
	defer s.writer.Unlock()
	if writer != nil {
		s.output = bufio.NewWriter(writer)
	} else {
		s.output = nil
	}
}

func (s *Server) writeJSON(data []byte) {
	s.writer.Lock()
	defer s.writer.Unlock()
	if s.output == nil {
		return
	}
	s.output.Write(data)
	s.output.WriteByte('\n')
	s.output.Flush()
}
