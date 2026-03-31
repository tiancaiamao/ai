package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"sync"
	"time"

	"log/slog"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
)

func runRPC(sessionPath string, debugAddr string, input io.Reader, output io.Writer) error {
	// Load configuration
	configPath, err := config.GetDefaultConfigPath()
	if err != nil {
		return fmt.Errorf("failed to get config path: %w", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Warn("Failed to load config", "path", configPath, "error", err)
		cfg, _ = config.LoadConfig(configPath)
	}

	// Initialize logger from config
	log, err := cfg.Log.CreateLogger()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	slog.SetDefault(log)

	// Convert config to llm.Model
	model := cfg.GetLLMModel()
	apiKey, err := config.ResolveAPIKey(model.Provider)
	if err != nil {
		return fmt.Errorf("missing API key: %w", err)
	}

	// Log model info
	slog.Info("Model", "id", model.ID, "provider", model.Provider, "baseURL", model.BaseURL)

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	cfg.Workspace = cwd

	sessionPath, err = normalizeSessionPath(sessionPath)
	if err != nil {
		return fmt.Errorf("failed to normalize session path: %w", err)
	}

	// Initialize session manager
	sessionsDir, err := session.GetDefaultSessionsDir(cwd)
	if err != nil {
		return fmt.Errorf("failed to get sessions path: %w", err)
	}

	if sessionPath != "" {
		sessionsDir = filepath.Dir(sessionPath)
	}
	sessionMgr := session.NewSessionManager(sessionsDir)

	// Load current session
	var sess *session.Session
	sessionID := ""
	if sessionPath != "" {
		opts := session.DefaultLoadOptions()
		sess, err = session.LoadSessionLazy(sessionPath, opts)
		if err != nil {
			return fmt.Errorf("failed to load session from %s: %w", sessionPath, err)
		}
		sessionID = sess.GetID()
		_ = sessionMgr.SetCurrent(sessionID)
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		slog.Info("Loaded session", "path", sessionPath, "count", len(sess.GetMessages()))
	} else {
		sess, sessionID, err = sessionMgr.LoadCurrent()
		if err != nil {
			name := time.Now().Format("20060102-150405")
			sess, err = sessionMgr.CreateSession(name, name)
			if err != nil {
				return fmt.Errorf("failed to create new session: %w", err)
			}
			sessionID = sess.GetID()
			if err := sessionMgr.SetCurrent(sessionID); err != nil {
				slog.Info("Failed to set current session:", "value", err)
			}
			if err := sessionMgr.SaveCurrent(); err != nil {
				slog.Info("Failed to update session metadata:", "value", err)
			}
			slog.Info("Created new session", "id", sessionID)
		} else {
			slog.Info("Restored previous session", "id", sessionID, "count", len(sess.GetMessages()))
		}
	}

	// Create tool registry and register tools
	ws, err := tools.NewWorkspace(cwd)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	registry := tools.NewRegistry()
	readTool := tools.NewReadTool(ws)
	editTool := tools.NewEditTool(ws)

	// Apply hashline configuration if enabled
	if cfg.ToolOutput != nil && cfg.ToolOutput.HashLines {
		readTool.SetHashLines(true)
	}
	if cfg.Edit != nil && cfg.Edit.Mode == "hashline" {
		editTool.SetEditMode(tools.EditModeHashline)
	}

	registry.Register(readTool)
	registry.Register(tools.NewBashTool(ws))
	registry.Register(tools.NewWriteTool(ws))
	registry.Register(tools.NewGrepTool(ws))
	registry.Register(editTool)
	registry.Register(tools.NewChangeWorkspaceTool(ws))

	slog.Info("Registered tools: read, bash, write, grep, edit", "count", len(registry.All()))

	// Load skills
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	agentDir := filepath.Join(homeDir, ".ai")

	skillLoader := skill.NewLoader(agentDir)
	skillResult := skillLoader.Load(&skill.LoadOptions{
		CWD:             cwd,
		AgentDir:        agentDir,
		SkillPaths:      nil,
		IncludeDefaults: true,
	})

	// Log skill diagnostics
	if len(skillResult.Diagnostics) > 0 {
		slog.Info("Skill loading diagnostics", "count", len(skillResult.Diagnostics))
		for _, diag := range skillResult.Diagnostics {
			if diag.Type == "error" {
				slog.Error("Skill error", "type", diag.Type, "path", diag.Path, "message", diag.Message)
			} else {
				slog.Warn("Skill warning", "type", diag.Type, "path", diag.Path, "message", diag.Message)
			}
		}
	}

	slog.Info("Loaded skills", "count", len(skillResult.Skills))

	// Build system prompt
	promptBuilder := prompt.NewBuilderWithWorkspace("", ws)
	promptBuilder.SetTools(registry.All()).SetSkills(skillResult.Skills)
	systemPrompt := promptBuilder.Build()

	_ = systemPrompt // Used by AgentNew internally

	// Start debug server if requested
	if debugAddr != "" {
		go func() {
			slog.Info("Starting debug server", "addr", debugAddr)
			if err := http.ListenAndServe(debugAddr, nil); err != nil {
				slog.Error("Debug server failed", "error", err)
			}
		}()
	}

	// Create AgentNew server (new architecture)
	sessionDir := sess.GetDir()
	agentServer, err := NewAgentNewServer(
		sessionDir,
		sessionID,
		&model,
		apiKey,
		sessionMgr,
		sess,
		registry,
		convertSkillsToPtrs(skillResult.Skills),
		ws,
	)
	if err != nil {
		return fmt.Errorf("failed to create agent server: %w", err)
	}
	defer agentServer.Close()

	// Create RPC server
	server := rpc.NewServer()
	server.SetOutput(output)

	// Set up handlers using AgentNew
	SetupAgentNewHandlers(server, agentServer)

	// Emit server start event
	server.EmitEvent(map[string]any{
		"type":       "server_start",
		"session_id": sessionID,
		"model":      model.ID,
	})

	slog.Info("[AgentNew] RPC server started", "session_id", sessionID)

	// Run RPC server
	return server.RunWithIO(input, output)
}


// AgentNewServer wraps AgentNew with RPC-compatible methods.
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

// GetSnapshot returns the current snapshot from the agent.
func (s *AgentNewServer) GetSnapshot() *agentctx.ContextSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agent.GetSnapshot()
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

// convertSkillsToPtrs converts []skill.Skill to []*skill.Skill.
func convertSkillsToPtrs(skills []skill.Skill) []*skill.Skill {
	result := make([]*skill.Skill, len(skills))
	for i := range skills {
		result[i] = &skills[i]
	}
	return result
}
