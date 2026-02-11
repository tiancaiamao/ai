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

// Server handles RPC communication via stdin/stdout.
type Server struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	writer sync.Mutex
	output *bufio.Writer

	// Callbacks for handling commands
	onPrompt                 func(req PromptRequest) error
	onSteer                  func(message string) error
	onFollowUp               func(message string) error
	onAbort                  func() error
	onNewSession             func(name, title string) (string, error)
	onClearSession           func() error
	onListSessions           func() ([]any, error)
	onSwitchSession          func(id string) error
	onDeleteSession          func(id string) error
	onGetState               func() (*SessionState, error)
	onGetMessages            func() ([]any, error)
	onCompact                func() (*CompactResult, error)
	onGetAvailableModels     func() ([]ModelInfo, error)
	onSetModel               func(provider, modelID string) (*ModelInfo, error)
	onCycleModel             func() (*CycleModelResult, error)
	onGetCommands            func() ([]SlashCommand, error)
	onGetSessionStats        func() (*SessionStats, error)
	onSetAutoCompaction      func(enabled bool) error
	onSetToolCallCutoff      func(cutoff int) error
	onSetToolSummaryStrategy func(strategy string) error
	onSetThinkingLevel       func(level string) (string, error)
	onCycleThinkingLevel     func() (string, error)
	onGetLastAssistantText   func() (string, error)
	onGetForkMessages        func() ([]ForkMessage, error)
	onFork                   func(entryID string) (*ForkResult, error)
	onGetTree                func() ([]TreeEntry, error)
	onResumeOnBranch         func(entryID string) error
	onSetSteeringMode        func(mode string) error
	onSetFollowUpMode        func(mode string) error
	onSetSessionName         func(name string) error
	onSetAutoRetry           func(enabled bool) error
	onAbortRetry             func() error
	onBash                   func(command string) (*BashResult, error)
	onAbortBash              func() error
	onExportHTML             func(path string) (string, error)
}

var logStreamEvents = os.Getenv("AI_LOG_STREAM_EVENTS") == "1"

// NewServer creates a new RPC server.
func NewServer() *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		ctx:    ctx,
		cancel: cancel,
	}
}

// SetPromptHandler sets the handler for prompt commands.
func (s *Server) SetPromptHandler(handler func(req PromptRequest) error) {
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

// SetCycleModelHandler sets the handler for cycle_model commands.
func (s *Server) SetCycleModelHandler(handler func() (*CycleModelResult, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onCycleModel = handler
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

// SetSetToolCallCutoffHandler sets the handler for set_tool_call_cutoff commands.
func (s *Server) SetSetToolCallCutoffHandler(handler func(cutoff int) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSetToolCallCutoff = handler
}

// SetSetToolSummaryStrategyHandler sets the handler for set_tool_summary_strategy commands.
func (s *Server) SetSetToolSummaryStrategyHandler(handler func(strategy string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSetToolSummaryStrategy = handler
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

// SetGetTreeHandler sets the handler for get_tree commands.
func (s *Server) SetGetTreeHandler(handler func() ([]TreeEntry, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onGetTree = handler
}

// SetResumeOnBranchHandler sets the handler for resume_on_branch commands.
func (s *Server) SetResumeOnBranchHandler(handler func(entryID string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onResumeOnBranch = handler
}

// SetSetSteeringModeHandler sets the handler for set_steering_mode commands.
func (s *Server) SetSetSteeringModeHandler(handler func(mode string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSetSteeringMode = handler
}

// SetSetFollowUpModeHandler sets the handler for set_follow_up_mode commands.
func (s *Server) SetSetFollowUpModeHandler(handler func(mode string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSetFollowUpMode = handler
}

// SetSetSessionNameHandler sets the handler for set_session_name commands.
func (s *Server) SetSetSessionNameHandler(handler func(name string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSetSessionName = handler
}

// SetSetAutoRetryHandler sets the handler for set_auto_retry commands.
func (s *Server) SetSetAutoRetryHandler(handler func(enabled bool) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onSetAutoRetry = handler
}

// SetAbortRetryHandler sets the handler for abort_retry commands.
func (s *Server) SetAbortRetryHandler(handler func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAbortRetry = handler
}

// SetBashHandler sets the handler for bash commands.
func (s *Server) SetBashHandler(handler func(command string) (*BashResult, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onBash = handler
}

// SetAbortBashHandler sets the handler for abort_bash commands.
func (s *Server) SetAbortBashHandler(handler func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onAbortBash = handler
}

// SetExportHTMLHandler sets the handler for export_html commands.
func (s *Server) SetExportHTMLHandler(handler func(path string) (string, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onExportHTML = handler
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
	return s.RunWithIO(os.Stdin, os.Stdout)
}

// RunWithIO starts the RPC server using the provided reader and writer.
// This method blocks until an error occurs or the reader is closed.
func (s *Server) RunWithIO(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
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

// handleCommand processes a single command.
func (s *Server) handleCommand(cmd RPCCommand) RPCResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch cmd.Type {
	case CommandPrompt:
		if s.onPrompt == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No prompt handler registered")
		}

		// Prefer direct message field, fall back to data object.
		var data struct {
			Message           string            `json:"message"`
			StreamingBehavior string            `json:"streamingBehavior"`
			Images            []json.RawMessage `json:"images"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		message := cmd.Message
		if message == "" {
			message = data.Message
		}

		req := PromptRequest{
			Message:           message,
			StreamingBehavior: data.StreamingBehavior,
			Images:            data.Images,
		}

		// Execute prompt (already async in Agent)
		if err := s.onPrompt(req); err != nil {
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

	case CommandCycleModel:
		if s.onCycleModel == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No cycle_model handler registered")
		}
		result, err := s.onCycleModel()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		if result == nil {
			return s.successResponse(cmd.ID, cmd.Type, nil)
		}
		return s.successResponse(cmd.ID, cmd.Type, result)

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

	case CommandSetToolCallCutoff:
		if s.onSetToolCallCutoff == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No set_tool_call_cutoff handler registered")
		}
		var data struct {
			Cutoff int `json:"cutoff"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		if err := s.onSetToolCallCutoff(data.Cutoff); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"cutoff": data.Cutoff})

	case CommandSetToolSummaryStrategy:
		if s.onSetToolSummaryStrategy == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No set_tool_summary_strategy handler registered")
		}
		var data struct {
			Strategy string `json:"strategy"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		if err := s.onSetToolSummaryStrategy(data.Strategy); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"strategy": data.Strategy})

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

	case CommandSetSteeringMode:
		if s.onSetSteeringMode == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No set_steering_mode handler registered")
		}
		var data struct {
			Mode string `json:"mode"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		if err := s.onSetSteeringMode(data.Mode); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandSetFollowUpMode:
		if s.onSetFollowUpMode == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No set_follow_up_mode handler registered")
		}
		var data struct {
			Mode string `json:"mode"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		if err := s.onSetFollowUpMode(data.Mode); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

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

	case CommandSetSessionName:
		if s.onSetSessionName == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No set_session_name handler registered")
		}
		var data struct {
			Name string `json:"name"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		if err := s.onSetSessionName(data.Name); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandGetTree:
		if s.onGetTree == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No get_tree handler registered")
		}
		entries, err := s.onGetTree()
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"entries": entries})

	case CommandResumeOnBranch:
		if s.onResumeOnBranch == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No resume_on_branch handler registered")
		}
		var data struct {
			EntryID string `json:"entryId"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		if err := s.onResumeOnBranch(data.EntryID); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"switched": true})

	case CommandSetAutoRetry:
		if s.onSetAutoRetry == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No set_auto_retry handler registered")
		}
		var data struct {
			Enabled bool `json:"enabled"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		if err := s.onSetAutoRetry(data.Enabled); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandAbortRetry:
		if s.onAbortRetry == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No abort_retry handler registered")
		}
		if err := s.onAbortRetry(); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandBash:
		if s.onBash == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No bash handler registered")
		}
		var data struct {
			Command string `json:"command"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		result, err := s.onBash(data.Command)
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, result)

	case CommandAbortBash:
		if s.onAbortBash == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No abort_bash handler registered")
		}
		if err := s.onAbortBash(); err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, nil)

	case CommandExportHTML:
		if s.onExportHTML == nil {
			return s.errorResponse(cmd.ID, cmd.Type, "No export_html handler registered")
		}
		var data struct {
			OutputPath string `json:"outputPath"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return s.errorResponse(cmd.ID, cmd.Type, fmt.Sprintf("Invalid data: %v", err))
			}
		}
		path, err := s.onExportHTML(data.OutputPath)
		if err != nil {
			return s.errorResponse(cmd.ID, cmd.Type, err.Error())
		}
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"path": path})

	case CommandPing:
		return s.successResponse(cmd.ID, cmd.Type, map[string]any{"ok": true})

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
		slog.Debug("[RPC] writeJSON with nil output", "data", string(data))
		return
	}

	// Write to output (will be discarded if output is io.Discard)
	_, _ = s.output.Write(data)
	_ = s.output.WriteByte('\n')
	_ = s.output.Flush()

	if shouldLogRPCJSON(data) {
		slog.Debug("[RPC] Response", "json", string(data))
	}
}

// Context returns the server's context.
func (s *Server) Context() context.Context {
	return s.ctx
}

func shouldLogRPCJSON(data []byte) bool {
	if logStreamEvents {
		return true
	}
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return true
	}
	switch env.Type {
	case "message_update", "text_delta", "thinking_delta", "tool_call_delta":
		return false
	default:
		return true
	}
}
