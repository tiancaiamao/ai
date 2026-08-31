package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/tiancaiamao/ai/pkg/command"
	"log/slog"
)

// Handler is the function signature for handling RPC protocol commands.
// It receives the raw RPCCommand and returns an arbitrary result (nil for no data)
// or an error. Parameter parsing is the handler's responsibility.
type Handler func(cmd RPCCommand) (any, error)

// SlashHandler is the function signature for slash commands invoked via the prompt channel.
// It receives the text arguments after the command name and returns an arbitrary result
// or an error.
// Deprecated: Use command.Handler directly. This alias is kept for backward compatibility.
type SlashHandler = command.Handler

// SlashCommandInfo describes a registered slash command.
// Deprecated: Use command.CommandInfo directly.
type SlashCommandInfo = command.CommandInfo

// Server handles RPC communication via stdin/stdout.
// Only the "prompt" command is handled as a protocol-level command.
// All other operations use slash commands registered via RegisterSlash.
type Server struct {
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	writer        sync.Mutex
	output        *bufio.Writer
	promptHandler Handler
	commands      *command.Registry
}

// NewServer creates a new RPC server.
func NewServer() *Server {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		ctx:      ctx,
		cancel:   cancel,
		commands: command.New(),
	}
	return s
}

// SetPromptHandler sets the handler for the "prompt" protocol command.
// This is the only protocol-level command — all other commands go through
// slash command dispatch via the prompt channel.
func (s *Server) SetPromptHandler(handler Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptHandler = handler
}

// RegisterSlash registers a slash command handler. Slash commands can be invoked
// via prompt interception ("/command args") and directly as JSON-RPC command types.
// The handler receives the text arguments after the command name.
func (s *Server) RegisterSlash(name, description string, handler SlashHandler) {
	s.commands.Register(name, description, handler)
}

// RegisterHiddenSlash registers a slash command that is callable but hidden from /help.
func (s *Server) RegisterHiddenSlash(name, description string, handler SlashHandler) {
	s.commands.RegisterHidden(name, description, handler)
}

// GetSlashHandler returns the slash command handler for the given name, if any.
func (s *Server) GetSlashHandler(name string) (SlashHandler, bool) {
	return s.commands.Get(name)
}

// ListSlashCommands returns user-visible slash commands (excludes hidden), sorted by name.
func (s *Server) ListSlashCommands() []SlashCommandInfo {
	return s.commands.ListCommands()
}

// Commands returns the underlying command.Registry for direct access.
func (s *Server) Commands() *command.Registry {
	return s.commands
}

// handleCommand processes a single RPC command.
// Only the "prompt" command is a protocol-level command.
// All other command types fall back to slash command dispatch.
func (s *Server) handleCommand(cmd RPCCommand) RPCResponse {
	if cmd.Type == "prompt" {
		s.mu.Lock()
		handler := s.promptHandler
		s.mu.Unlock()

		if handler == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "prompt handler not set")
		}

		result, err := handler(cmd)
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, result)
	}

	// Try slash command handlers as fallback for backward compatibility.
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

// RunWithIO starts the RPC server using the provided reader and writer.
// This method blocks until an error occurs or the reader is closed.
func (s *Server) RunWithIO(reader io.Reader, writer io.Writer) error {
	// Wrap reader so reads are cancellable via server context.
	// Without this, scanner.Scan() blocks forever and timeout watchdog
	// cannot cause the server to exit.
	cr := &contextReader{reader: reader, ctx: s.ctx}
	scanner := bufio.NewScanner(cr)
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

// Cancel shuts down the server by canceling its context.
// This causes RunWithIO to unblock and return, allowing the process to exit.
func (s *Server) Cancel() {
	s.cancel()
}

// contextReader wraps an io.Reader so that Read calls are cancellable via context.
// When the context is canceled, ongoing and future Read calls return an error,
// unblocking any scanner that is waiting for input.
type contextReader struct {
	reader io.Reader
	ctx    context.Context
}

func (cr *contextReader) Read(p []byte) (int, error) {
	// Fast path: context already done.
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	// If the underlying reader is a file/pipe, we can't interrupt a blocking
	// Read directly. Use a goroutine with a race between the read and context
	// cancellation.
	done := make(chan readResult, 1)
	go func() {
		n, err := cr.reader.Read(p)
		done <- readResult{n: n, err: err}
	}()
	select {
	case r := <-done:
		return r.n, r.err
	case <-cr.ctx.Done():
		return 0, cr.ctx.Err()
	}
}

type readResult struct {
	n   int
	err error
}
