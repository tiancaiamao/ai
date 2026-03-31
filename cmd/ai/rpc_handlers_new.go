package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
	"log/slog"
)

// AgentNewServer wraps AgentNew with RPC-compatible methods.
// This bridges the new AgentNew implementation with the existing RPC interface.
type AgentNewServer struct {
	// Core state
	mu         sync.RWMutex
	agent      *agent.AgentNew
	sessionDir string
	sessionID  string

	// Configuration
	model          *llm.Model
	apiKey         string
	sessionMgr     *session.SessionManager
	currentSession *session.Session

	// Event emission
	eventEmitter agent.EventEmitter

	// Tools
	registry *tools.Registry

	// Skills
	skills []*skill.Skill

	// Workspace
	workspace *tools.Workspace

	// State tracking
	isStreaming    bool
	isCompacting   bool
	pendingSteer   bool
	steeringMode   string
	followUpMode   string
}

// NewAgentNewServer creates a new server wrapping AgentNew.
func NewAgentNewServer(
	sessionDir string,
	sessionID string,
	model *llm.Model,
	apiKey string,
	sessionMgr *session.SessionManager,
	sess *session.Session,
	registry *tools.Registry,
	skills []*skill.Skill,
	workspace *tools.Workspace,
) (*AgentNewServer, error) {
	// Create event emitter that wraps server.EmitEvent
	eventEmitter := &rpcEventEmitterAdapter{}

	// Create new agent
	ag, err := agent.NewAgentNew(sessionDir, sessionID, model, apiKey, eventEmitter)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	return &AgentNewServer{
		agent:          ag,
		sessionDir:     sessionDir,
		sessionID:      sessionID,
		model:          model,
		apiKey:         apiKey,
		sessionMgr:     sessionMgr,
		currentSession: sess,
		eventEmitter:   eventEmitter,
		registry:       registry,
		skills:         skills,
		workspace:      workspace,
		steeringMode:   "all",
		followUpMode:   "all",
	}, nil
}

// SetEventEmitter sets the event emitter (typically the RPC server).
func (s *AgentNewServer) SetEventEmitter(emitter interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if adapter, ok := s.eventEmitter.(*rpcEventEmitterAdapter); ok {
		adapter.server = emitter
	}
}

// Prompt handles the prompt command using AgentNew.
func (s *AgentNewServer) Prompt(ctx context.Context, message string) error {
	s.mu.Lock()
	if s.isStreaming {
		s.mu.Unlock()
		return fmt.Errorf("agent is busy")
	}
	s.isStreaming = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isStreaming = false
		s.pendingSteer = false
		s.mu.Unlock()
	}()

	// Execute normal mode
	if err := s.agent.ExecuteNormalMode(ctx, message); err != nil {
		return fmt.Errorf("failed to execute normal mode: %w", err)
	}

	return nil
}

// Steer handles the steer command using AgentNew.
func (s *AgentNewServer) Steer(ctx context.Context, message string) error {
	s.mu.Lock()
	mode := s.steeringMode
	pending := s.pendingSteer
	streaming := s.isStreaming
	s.mu.Unlock()

	if mode == "one-at-a-time" && pending {
		return fmt.Errorf("steer already pending")
	}

	s.mu.Lock()
	s.pendingSteer = true
	s.mu.Unlock()

	// If not streaming, execute immediately
	if !streaming {
		defer func() {
			s.mu.Lock()
			s.pendingSteer = false
			s.mu.Unlock()
		}()
		return s.agent.ExecuteNormalMode(ctx, message)
	}

	// If streaming, the agent should handle the steer internally
	// For now, we'll cancel and restart
	slog.Info("[AgentNew] Steer during streaming - restart execution", "message", message)
	return s.agent.ExecuteNormalMode(ctx, message)
}

// FollowUp handles the follow_up command using AgentNew.
func (s *AgentNewServer) FollowUp(ctx context.Context, message string) error {
	s.mu.Lock()
	mode := s.followUpMode
	s.mu.Unlock()

	if mode == "one-at-a-time" {
		// Check if there's already a pending follow-up
		// This would need to be tracked in AgentNew
	}

	return s.agent.ExecuteNormalMode(ctx, message)
}

// Abort stops the current execution.
func (s *AgentNewServer) Abort() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	slog.Info("[AgentNew] Abort called")
	if s.agent != nil {
		return s.agent.Close()
	}
	return nil
}

// GetMessages returns the current messages from the agent.
func (s *AgentNewServer) GetMessages() []any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return []any{}
	}

	result := make([]any, len(snapshot.RecentMessages))
	for i, msg := range snapshot.RecentMessages {
		result[i] = msg
	}
	return result
}

// GetState returns the current agent state.
func (s *AgentNewServer) GetState() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return map[string]any{}
	}

	return map[string]any{
		"sessionDir":   s.sessionDir,
		"sessionID":    s.sessionID,
		"totalTurns":   snapshot.AgentState.TotalTurns,
		"tokensUsed":   snapshot.AgentState.TokensUsed,
		"tokensLimit":  snapshot.AgentState.TokensLimit,
		"isStreaming":  s.isStreaming,
		"isCompacting": s.isCompacting,
	}
}

// Close closes the agent and releases resources.
func (s *AgentNewServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.agent != nil {
		return s.agent.Close()
	}
	return nil
}

// rpcEventEmitterAdapter adapts AgentNew events to RPC events.
type rpcEventEmitterAdapter struct {
	mu     sync.Mutex
	server interface{}
}

// Emit converts AgentNew events to RPC events and emits them.
func (a *rpcEventEmitterAdapter) Emit(event agent.AgentEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.server == nil {
		return
	}

	// Convert AgentNew event to RPC event format
	rpcEvent := map[string]any{
		"type": event.Type,
	}

	// Add event-specific fields
	switch event.Type {
	case "agent_start":
		// No additional fields needed for agent_start
	case "agent_end":
		rpcEvent["messages"] = event.Messages
	case "message_end":
		if event.Message != nil {
			rpcEvent["message"] = event.Message
		}
	case "tool_call_start":
		rpcEvent["toolName"] = event.ToolName
		rpcEvent["toolCallId"] = event.ToolCallID
	case "tool_execution_end":
		if event.Result != nil {
			rpcEvent["result"] = event.Result
		}
		rpcEvent["isError"] = event.IsError
	case "error":
		rpcEvent["error"] = event.Error
		if event.ErrorStack != "" {
			rpcEvent["errorStack"] = event.ErrorStack
		}
	}

	// Emit via RPC server
	if emitter, ok := a.server.(interface{ EmitEvent(any) }); ok {
		emitter.EmitEvent(rpcEvent)
	}
}

// SetupAgentNewHandlers configures RPC server handlers to use AgentNew.
// This function replaces the existing agent handlers with new agent handlers.
func SetupAgentNewHandlers(
	server *rpc.Server,
	agentNewServer *AgentNewServer,
) {
	// Set prompt handler
	server.SetPromptHandler(func(req rpc.PromptRequest) error {
		slog.Info("[AgentNew] Received prompt:", "value", req.Message)
		message := req.Message
		if message == "" {
			return fmt.Errorf("empty prompt message")
		}

		ctx := server.Context()
		return agentNewServer.Prompt(ctx, message)
	})

	// Set steer handler
	server.SetSteerHandler(func(message string) error {
		slog.Info("[AgentNew] Received steer:", "value", message)
		if message == "" {
			return fmt.Errorf("empty steer message")
		}

		ctx := server.Context()
		return agentNewServer.Steer(ctx, message)
	})

	// Set follow-up handler
	server.SetFollowUpHandler(func(message string) error {
		slog.Info("[AgentNew] Received follow_up:", "value", message)
		if message == "" {
			return fmt.Errorf("empty follow-up message")
		}

		ctx := server.Context()
		return agentNewServer.FollowUp(ctx, message)
	})

	// Set abort handler
	server.SetAbortHandler(func() error {
		slog.Info("[AgentNew] Received abort")
		return agentNewServer.Abort()
	})

	// Set get_messages handler
	server.SetGetMessagesHandler(func() ([]any, error) {
		slog.Info("[AgentNew] Received get_messages")
		return agentNewServer.GetMessages(), nil
	})

	slog.Info("[AgentNew] RPC handlers configured successfully")
}

// LoadOrNewAgentSession loads an existing session or creates a new one using AgentNew.
func LoadOrNewAgentSession(
	sessionPath string,
	sessionMgr *session.SessionManager,
	model *llm.Model,
	apiKey string,
	registry *tools.Registry,
	skills []*skill.Skill,
	workspace *tools.Workspace,
) (*AgentNewServer, *session.Session, error) {
	var sess *session.Session
	var sessionID string
	var sessionDir string
	var err error

	// Determine session directory and ID
	if sessionPath != "" {
		// Load existing session
		sessionDir, err = normalizeSessionPath(sessionPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to normalize session path: %w", err)
		}

		// If sessionPath points to messages.jsonl, extract directory
		if filepath.Base(sessionDir) == "messages.jsonl" {
			sessionDir = filepath.Dir(sessionDir)
		}

		opts := session.DefaultLoadOptions()
		sess, err = session.LoadSessionLazy(sessionDir, opts)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load session: %w", err)
		}
		sessionID = sess.GetID()
		slog.Info("[AgentNew] Loaded existing session", "id", sessionID, "dir", sessionDir)
	} else {
		// Create new session or load current
		sess, sessionID, err = sessionMgr.LoadCurrent()
		if err != nil {
			// Create new session
			name := fmt.Sprintf("sess_%d", os.Getpid())
			sess, err = sessionMgr.CreateSession(name, name)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create session: %w", err)
			}
			sessionID = sess.GetID()
			if err := sessionMgr.SetCurrent(sessionID); err != nil {
				slog.Info("Failed to set current session:", "value", err)
			}
			slog.Info("[AgentNew] Created new session", "id", sessionID)
		} else {
			slog.Info("[AgentNew] Loaded current session", "id", sessionID)
		}
		sessionDir = sess.GetDir()
	}

	// Create AgentNew server
	agentServer, err := NewAgentNewServer(
		sessionDir,
		sessionID,
		model,
		apiKey,
		sessionMgr,
		sess,
		registry,
		skills,
		workspace,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create agent server: %w", err)
	}

	return agentServer, sess, nil
}
