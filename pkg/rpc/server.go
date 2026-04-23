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
)

// Handler is the function signature for handling RPC protocol commands.
// It receives the raw RPCCommand and returns an arbitrary result (nil for no data)
// or an error. Parameter parsing is the handler's responsibility.
type Handler func(cmd RPCCommand) (any, error)

// SlashHandler is the function signature for slash commands invoked via the prompt channel.
// It receives the text arguments after the command name and returns an arbitrary result
// or an error.
type SlashHandler func(args string) (any, error)

// Server handles RPC communication via stdin/stdout.
// Protocol commands (prompt, steer, follow_up, abort, ping) use Register.
// Slash commands use RegisterSlash — they can be invoked both via prompt
// interception ("/command args") and directly via JSON-RPC command type.
type Server struct {
	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	writer       sync.Mutex
	output       *bufio.Writer
	handlers     map[string]Handler
	slashHandlers map[string]SlashHandler
}

// NewServer creates a new RPC server with ping pre-registered.
func NewServer() *Server {
	ctx, cancel := context.WithCancel(context.Background())
		s := &Server{
		ctx:           ctx,
		cancel:        cancel,
		handlers:      make(map[string]Handler),
		slashHandlers: make(map[string]SlashHandler),
	}
	s.Register(CommandPing, func(cmd RPCCommand) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	return s
}

// Register registers a handler for the given protocol command type.
func (s *Server) Register(cmdType string, handler Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[cmdType] = handler
}

// RegisterSlash registers a slash command handler. Slash commands can be invoked
// via prompt interception ("/command args") and directly as JSON-RPC command types.
// The handler receives the text arguments after the command name.
func (s *Server) RegisterSlash(name string, handler SlashHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.slashHandlers[name] = handler
}

// GetSlashHandler returns the slash command handler for the given name, if any.
func (s *Server) GetSlashHandler(name string) (SlashHandler, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.slashHandlers[name]
	return h, ok
}

// HasHandler reports whether a handler is registered for the given command type.
func (s *Server) HasHandler(cmdType string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.handlers[cmdType]
	return ok
}

// Run starts the RPC server reading from stdin and writing to stdout.
// This method blocks until an error occurs or stdin is closed.
func (s *Server) Run() error {
	return s.RunWithIO(os.Stdin, os.Stdout)
}

// RunWithIO starts the RPC server using the provided reader and writer.
// This method blocks until an error occurs or the reader is closed.
func (s *Server) RunWithIO(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	// Set larger buffer: 4MB initial, 16MB max to handle large JSON commands (>64KB)
	buf := make([]byte, 0, 4*1024*1024) // 4MB
	scanner.Buffer(buf, 16*1024*1024)   // 16MB max
	s.setOutput(writer)

	for scanner.Scan() {
		line := scanner.Bytes()

		var cmd RPCCommand
		if err := json.Unmarshal(line, &cmd); err != nil {
			s.sendError("", fmt.Sprintf("Failed to parse command: %v", err))
			continue
		}

		resp := s.handleCommand(cmd)
		s.sendResponse(resp)
	}

	// Wait for all background tasks to complete
	slog.Info("[RPC] Waiting for agent to complete...")
	s.wg.Wait()
	slog.Info("[RPC] All tasks completed")

	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}

	return nil
}

// handleCommand processes a single RPC command.
func (s *Server) handleCommand(cmd RPCCommand) RPCResponse {
	s.mu.Lock()
	handler, ok := s.handlers[cmd.Type]
	s.mu.Unlock()

	if !ok {
		// Try slash command handlers as fallback for backward compatibility.
		// This allows clients to send {"type": "get_state"} directly
		// while the same command can also be invoked via "/get_state".
		if slashHandler, slashOK := s.GetSlashHandler(cmd.Type); slashOK {
			args := s.extractSlashArgs(cmd)
			result, err := slashHandler(args)
			if err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, err.Error())
			}
			return s.successResponse(cmd.ID, cmd.Type, result)
		}
		return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("No %s handler registered", cmd.Type))
	}

	result, err := handler(cmd)
	if err != nil {
		return s.errorResponse(cmd.ID, cmd.Type, err.Error())
	}
	return s.successResponse(cmd.ID, cmd.Type, result)
}

// extractSlashArgs converts an RPCCommand's data to a text args string
// suitable for slash command handlers. It passes the raw JSON data as-is
// so that individual handlers can parse structured fields properly.
// Handlers that receive structured JSON data should json.Unmarshal the args string.
func (s *Server) extractSlashArgs(cmd RPCCommand) string {
	if cmd.Message != "" {
		return cmd.Message
	}
	if len(cmd.Data) == 0 {
		return ""
	}
	// Pass raw JSON data as string — handlers know their own schema
	return string(cmd.Data)
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

// sendResponse writes a response to stdout.
func (s *Server) sendResponse(resp RPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	s.writeJSON(data)
}

// sendError writes an error response to stdout.
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
	if writer == nil {
		s.output = nil
		return
	}
	s.output = bufio.NewWriter(writer)
}

func (s *Server) writeJSON(data []byte) {
	s.writer.Lock()
	defer s.writer.Unlock()
	if s.output == nil {
		slog.Info("[RPC] writeJSON with nil output", "data", string(data))
		return
	}

	// Write to output (will be discarded if output is io.Discard)
	_, _ = s.output.Write(data)
	_ = s.output.WriteByte('\n')
	_ = s.output.Flush()
}

// Context returns the server's context.
func (s *Server) Context() context.Context {
	return s.ctx
}