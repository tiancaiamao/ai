package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

// Server handles RPC communication via stdin/stdout.
type Server struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Callbacks for handling commands
	onPrompt               func(message string) error
	onSteer                func(message string) error
	onFollowUp             func(message string) error
	onAbort                func() error
	onNewSession           func(name, title string) (string, error)
	onClearSession         func() error
	onListSessions         func() ([]any, error)
	onSwitchSession        func(id string) error
	onDeleteSession        func(id string) error
	onGetState             func() (*SessionState, error)
	onGetMessages          func() ([]any, error)
	onCompact              func() (*CompactResult, error)
	onGetAvailableModels   func() ([]ModelInfo, error)
	onSetModel             func(provider, modelID string) (*ModelInfo, error)
	onGetCommands          func() ([]SlashCommand, error)
	onGetSessionStats      func() (*SessionStats, error)
	onSetAutoCompaction    func(enabled bool) error
	onSetThinkingLevel     func(level string) (string, error)
	onCycleThinkingLevel   func() (string, error)
	onGetLastAssistantText func() (string, error)
	onGetForkMessages      func() ([]ForkMessage, error)
	onFork                 func(entryID string) (*ForkResult, error)
}

// NewServer creates a new RPC server.
func NewServer() *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:    ctx,
		cancel: cancel,
	}
}

// SetPromptHandler sets the handler for prompt commands.
func (s *Server) SetPromptHandler(handler func(message string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onPrompt = handler
}

// SetSteerHandler sets the handler for steer commands.
func (s *Server) SetSteerHandler(handler func(message string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSteer = handler
}

// SetFollowUpHandler sets the handler for follow_up commands.
func (s *Server) SetFollowUpHandler(handler func(message string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onFollowUp = handler
}

// SetAbortHandler sets the handler for abort commands.
func (s *Server) SetAbortHandler(handler func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAbort = handler
}

// SetClearSessionHandler sets the handler for clear_session commands.
func (s *Server) SetClearSessionHandler(handler func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onClearSession = handler
}

// SetGetStateHandler sets the handler for get_state commands.
func (s *Server) SetGetStateHandler(handler func() (*SessionState, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onGetState = handler
}

// SetGetMessagesHandler sets the handler for get_messages commands.
func (s *Server) SetGetMessagesHandler(handler func() ([]any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onGetMessages = handler
}

// SetCompactHandler sets the handler for compact commands.
func (s *Server) SetCompactHandler(handler func() (*CompactResult, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCompact = handler
}

// SetGetAvailableModelsHandler sets the handler for get_available_models commands.
func (s *Server) SetGetAvailableModelsHandler(handler func() ([]ModelInfo, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onGetAvailableModels = handler
}

// SetSetModelHandler sets the handler for set_model commands.
func (s *Server) SetSetModelHandler(handler func(provider, modelID string) (*ModelInfo, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSetModel = handler
}

// SetGetCommandsHandler sets the handler for get_commands commands.
func (s *Server) SetGetCommandsHandler(handler func() ([]SlashCommand, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onGetCommands = handler
}

// SetGetSessionStatsHandler sets the handler for get_session_stats commands.
func (s *Server) SetGetSessionStatsHandler(handler func() (*SessionStats, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onGetSessionStats = handler
}

// SetSetAutoCompactionHandler sets the handler for set_auto_compaction commands.
func (s *Server) SetSetAutoCompactionHandler(handler func(enabled bool) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSetAutoCompaction = handler
}

// SetSetThinkingLevelHandler sets the handler for set_thinking_level commands.
func (s *Server) SetSetThinkingLevelHandler(handler func(level string) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSetThinkingLevel = handler
}

// SetCycleThinkingLevelHandler sets the handler for cycle_thinking_level commands.
func (s *Server) SetCycleThinkingLevelHandler(handler func() (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCycleThinkingLevel = handler
}

// SetGetLastAssistantTextHandler sets the handler for get_last_assistant_text commands.
func (s *Server) SetGetLastAssistantTextHandler(handler func() (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onGetLastAssistantText = handler
}

// SetGetForkMessagesHandler sets the handler for get_fork_messages commands.
func (s *Server) SetGetForkMessagesHandler(handler func() ([]ForkMessage, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onGetForkMessages = handler
}

// SetForkHandler sets the handler for fork commands.
func (s *Server) SetForkHandler(handler func(entryID string) (*ForkResult, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onFork = handler
}

// SetNewSessionHandler sets the handler for new_session commands.
func (s *Server) SetNewSessionHandler(handler func(name, title string) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onNewSession = handler
}

// SetListSessionsHandler sets the handler for list_sessions commands.
func (s *Server) SetListSessionsHandler(handler func() ([]any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onListSessions = handler
}

// SetSwitchSessionHandler sets the handler for switch_session commands.
func (s *Server) SetSwitchSessionHandler(handler func(id string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSwitchSession = handler
}

// SetDeleteSessionHandler sets the handler for delete_session commands.
func (s *Server) SetDeleteSessionHandler(handler func(id string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onDeleteSession = handler
}

// Run starts the RPC server, reading commands from stdin and writing responses to stdout.
// This method blocks until an error occurs or stdin is closed.
func (s *Server) Run() error {
	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	for scanner.Scan() {
		line := scanner.Bytes()

		var cmd RPCCommand
		if err := json.Unmarshal(line, &cmd); err != nil {
			s.sendError(writer, "", fmt.Sprintf("Failed to parse command: %v", err))
			continue
		}

		resp := s.handleCommand(cmd)
		s.sendResponse(writer, resp)
	}

	// Wait for all background tasks to complete
	log.Println("[RPC] Waiting for agent to complete...")
	s.wg.Wait()
	log.Println("[RPC] All tasks completed")

	if err := scanner.Err(); err != nil && err != io.EOF {
		return err
	}

	return nil
}

// handleCommand processes a single command.
func (s *Server) handleCommand(cmd RPCCommand) RPCResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch cmd.Type {
	case CommandPrompt:
		if s.onPrompt == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No prompt handler registered")
		}

		// Prefer direct message field, fall back to data object
		message := cmd.Message
		if message == "" && len(cmd.Data) > 0 {
			var data struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
			message = data.Message
		}

		// Execute prompt (already async in Agent)
		if err := s.onPrompt(message); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}

		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandSteer:
		if s.onSteer == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No steer handler registered")
		}
		message := cmd.Message
		if message == "" && len(cmd.Data) > 0 {
			var data struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
			message = data.Message
		}
		if err := s.onSteer(message); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandFollowUp:
		if s.onFollowUp == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No follow_up handler registered")
		}
		message := cmd.Message
		if message == "" && len(cmd.Data) > 0 {
			var data struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
			message = data.Message
		}
		if err := s.onFollowUp(message); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandAbort:
		if s.onAbort == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No abort handler registered")
		}
		if err := s.onAbort(); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandClearSession:
		if s.onClearSession == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No clear_session handler registered")
		}
		if err := s.onClearSession(); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandGetState:
		if s.onGetState == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No get_state handler registered")
		}
		state, err := s.onGetState()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, state)

	case CommandGetMessages:
		if s.onGetMessages == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No get_messages handler registered")
		}
		messages, err := s.onGetMessages()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"messages": messages})

	case CommandNewSession:
		if s.onNewSession == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No new_session handler registered")
		}
		var data struct {
			Name  string `json:"name"`
			Title string `json:"title"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		sessionID, err := s.onNewSession(data.Name, data.Title)
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"sessionId": sessionID, "cancelled": false})

	case CommandListSessions:
		if s.onListSessions == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No list_sessions handler registered")
		}
		sessions, err := s.onListSessions()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"sessions": sessions})

	case CommandSwitchSession:
		if s.onSwitchSession == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No switch_session handler registered")
		}
		var data struct {
			ID          string `json:"id"`
			SessionPath string `json:"sessionPath"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		switchID := data.ID
		if data.SessionPath != "" {
			switchID = data.SessionPath
		}
		if err := s.onSwitchSession(switchID); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"switched": true, "cancelled": false})

	case CommandDeleteSession:
		if s.onDeleteSession == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No delete_session handler registered")
		}
		var data struct {
			ID string `json:"id"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		if err := s.onDeleteSession(data.ID); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"deleted": true})

	case CommandCompact:
		if s.onCompact == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No compact handler registered")
		}
		result, err := s.onCompact()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, result)

	case CommandGetAvailableModels:
		if s.onGetAvailableModels == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No get_available_models handler registered")
		}
		models, err := s.onGetAvailableModels()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"models": models})

	case CommandSetModel:
		if s.onSetModel == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No set_model handler registered")
		}
		var data struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		model, err := s.onSetModel(data.Provider, data.ModelID)
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, model)

	case CommandGetCommands:
		if s.onGetCommands == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No get_commands handler registered")
		}
		commands, err := s.onGetCommands()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"commands": commands})

	case CommandGetSessionStats:
		if s.onGetSessionStats == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No get_session_stats handler registered")
		}
		stats, err := s.onGetSessionStats()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, stats)

	case CommandSetAutoCompaction:
		if s.onSetAutoCompaction == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No set_auto_compaction handler registered")
		}
		var data struct {
			Enabled bool `json:"enabled"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		if err := s.onSetAutoCompaction(data.Enabled); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandSetThinkingLevel:
		if s.onSetThinkingLevel == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No set_thinking_level handler registered")
		}
		var data struct {
			Level string `json:"level"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		level, err := s.onSetThinkingLevel(data.Level)
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"level": level})

	case CommandCycleThinkingLevel:
		if s.onCycleThinkingLevel == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No cycle_thinking_level handler registered")
		}
		level, err := s.onCycleThinkingLevel()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"level": level})

	case CommandGetLastAssistantText:
		if s.onGetLastAssistantText == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No get_last_assistant_text handler registered")
		}
		text, err := s.onGetLastAssistantText()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"text": text})

	case CommandGetForkMessages:
		if s.onGetForkMessages == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No get_fork_messages handler registered")
		}
		messages, err := s.onGetForkMessages()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"messages": messages})

	case CommandFork:
		if s.onFork == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No fork handler registered")
		}
		var data struct {
			EntryID string `json:"entryId"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		result, err := s.onFork(data.EntryID)
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, result)

	case CommandPing:
		// Health check - always succeeds
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{
			"status":    "ok",
			"timestamp":  s.ctx,
		})

	default:
		return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Unknown command: %s", cmd.Type))
	}
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
func (s *Server) sendResponse(writer *bufio.Writer, resp RPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	writer.Write(data)
	writer.WriteByte('\n')
	writer.Flush()
}

// sendError writes an error response to stdout.
func (s *Server) sendError(writer *bufio.Writer, cmdID, errMsg string) {
	resp := s.errorResponse(cmdID, "", errMsg)
	s.sendResponse(writer, resp)
}

// EmitEvent emits an event to stdout as JSON.
func (s *Server) EmitEvent(event any) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Println(string(data))
}

// Context returns the server's context.
func (s *Server) Context() context.Context {
	return s.ctx
}
