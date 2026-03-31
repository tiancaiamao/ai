package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/config"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
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

	traceOutputPath := ""
	_, traceOutputPath, err = initTraceFileHandler(sessionID)
	if err != nil {
		slog.Warn("Failed to create trace handler", "outputDir", traceOutputPath, "error", err)
		traceOutputPath = ""
	} else {
		slog.Info("Trace handler initialized", "output", traceOutputPath)
	}

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
		configPath,
		cfg,
		traceOutputPath,
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
	agentServer.SetEventEmitter(server)

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
	model           *llm.Model
	apiKey          string
	configPath      string
	cfg             *config.Config
	traceOutputPath string
	sessionMgr      *session.SessionManager
	currentSession  *session.Session

	// Event emission
	eventEmitter agent.EventEmitter

	// Tools
	registry *tools.Registry

	// Skills
	skills []*skill.Skill

	// Workspace
	workspace *tools.Workspace

	// State tracking
	isStreaming           bool
	isCompacting          bool
	pendingSteer          bool
	steeringMode          string
	followUpMode          string
	thinkingLevel         string
	autoRetry             bool
	autoCompactionEnabled bool
}

// NewAgentNewServer creates a new server wrapping AgentNew.
func NewAgentNewServer(
	sessionDir string,
	sessionID string,
	model *llm.Model,
	apiKey string,
	configPath string,
	cfg *config.Config,
	traceOutputPath string,
	sessionMgr *session.SessionManager,
	sess *session.Session,
	registry *tools.Registry,
	skills []*skill.Skill,
	workspace *tools.Workspace,
) (*AgentNewServer, error) {
	// Create event emitter that wraps server.EmitEvent
	eventEmitter := &rpcEventEmitterAdapter{}

	// Load existing snapshot/checkpoints when present.
	ag, err := agent.LoadSession(context.Background(), sessionDir, model, apiKey, eventEmitter)
	if err != nil {
		return nil, fmt.Errorf("failed to create agent: %w", err)
	}

	autoCompactionEnabled := true
	if cfg != nil && cfg.Compactor != nil {
		autoCompactionEnabled = cfg.Compactor.AutoCompact
	}

	return &AgentNewServer{
		agent:                 ag,
		sessionDir:            sessionDir,
		sessionID:             sessionID,
		model:                 model,
		apiKey:                apiKey,
		configPath:            configPath,
		cfg:                   cfg,
		traceOutputPath:       traceOutputPath,
		sessionMgr:            sessionMgr,
		currentSession:        sess,
		eventEmitter:          eventEmitter,
		registry:              registry,
		skills:                skills,
		workspace:             workspace,
		steeringMode:          "all",
		followUpMode:          "all",
		thinkingLevel:         "medium",
		autoCompactionEnabled: autoCompactionEnabled,
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

	beforeCount := 0
	if snapshot := s.agent.GetSnapshot(); snapshot != nil {
		beforeCount = len(snapshot.RecentMessages)
	}

	ctx, finalizeTrace := s.beginTurnTrace(ctx)
	defer finalizeTrace()

	s.emitEvent(agent.NewAgentStartEvent())
	s.emitEvent(agent.NewTurnStartEvent())

	// Execute one full turn (includes automatic context management flow)
	if err := s.agent.ExecuteTurn(ctx, message); err != nil {
		s.emitEvent(agent.NewErrorEvent(err))
		s.emitEvent(agent.NewAgentEndEvent(nil))
		return fmt.Errorf("failed to execute turn: %w", err)
	}
	s.mu.Lock()
	if err := s.syncSessionFromSnapshotLocked(); err != nil {
		slog.Warn("[AgentNew] Failed to sync session after prompt", "error", err)
	}
	s.mu.Unlock()

	assistantMessage, toolResults := s.emitPostTurnEvents(beforeCount)
	s.emitEvent(agent.NewTurnEndEvent(assistantMessage, toolResults))
	s.emitEvent(agent.NewAgentEndEvent(nil))

	return nil
}

func (s *AgentNewServer) emitEvent(event agent.AgentEvent) {
	if s.eventEmitter == nil {
		return
	}
	s.eventEmitter.Emit(event)
}

func (s *AgentNewServer) emitPostTurnEvents(beforeCount int) (*agentctx.AgentMessage, []agentctx.AgentMessage) {
	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return nil, nil
	}

	if beforeCount < 0 || beforeCount > len(snapshot.RecentMessages) {
		beforeCount = len(snapshot.RecentMessages)
	}

	newMessages := snapshot.RecentMessages[beforeCount:]
	var lastAssistant *agentctx.AgentMessage
	toolResults := make([]agentctx.AgentMessage, 0, len(newMessages))

	for _, msg := range newMessages {
		switch msg.Role {
		case "assistant":
			s.emitEvent(agent.NewMessageStartEvent(msg))
			if text := msg.ExtractText(); text != "" {
				s.emitEvent(agent.NewMessageUpdateEvent(msg, agent.AssistantMessageEvent{
					Type:  "text_delta",
					Delta: text,
				}))
			}
			s.emitEvent(agent.NewMessageEndEvent(msg))

			msgCopy := msg
			lastAssistant = &msgCopy
		case "toolResult":
			msgCopy := msg
			toolResults = append(toolResults, msgCopy)
			s.emitEvent(agent.NewToolExecutionEndEvent(
				msgCopy.ToolCallID,
				msgCopy.ToolName,
				&msgCopy,
				msgCopy.IsError,
			))
		}
	}

	return lastAssistant, toolResults
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

	ctx, finalizeTrace := s.beginTurnTrace(ctx)
	defer finalizeTrace()

	// If not streaming, execute immediately
	if !streaming {
		defer func() {
			s.mu.Lock()
			s.pendingSteer = false
			s.mu.Unlock()
		}()
		if err := s.agent.ExecuteTurn(ctx, message); err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.syncSessionFromSnapshotLocked(); err != nil {
			slog.Warn("[AgentNew] Failed to sync session after steer", "error", err)
		}
		return nil
	}

	// If streaming, the agent should handle the steer internally
	// For now, we'll cancel and restart
	slog.Info("[AgentNew] Steer during streaming - restart execution", "message", message)
	if err := s.agent.ExecuteTurn(ctx, message); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.syncSessionFromSnapshotLocked(); err != nil {
		slog.Warn("[AgentNew] Failed to sync session after streaming steer", "error", err)
	}
	return nil
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

	ctx, finalizeTrace := s.beginTurnTrace(ctx)
	defer finalizeTrace()

	if err := s.agent.ExecuteTurn(ctx, message); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.syncSessionFromSnapshotLocked(); err != nil {
		slog.Warn("[AgentNew] Failed to sync session after follow-up", "error", err)
	}
	return nil
}

func (s *AgentNewServer) beginTurnTrace(ctx context.Context) (context.Context, func()) {
	tb := traceevent.NewTraceBuf()
	seq := 0
	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			seq = fh.IncrementPromptCount()
		}
	}
	tb.SetTraceID(traceevent.GenerateTraceID("session", seq))

	traceCtx := traceevent.WithTraceBuf(ctx, tb)
	traceevent.SetActiveTraceBuf(tb)

	return traceCtx, func() {
		if err := tb.DiscardOrFlush(traceCtx); err != nil {
			slog.Warn("[AgentNew] Failed to flush trace buffer", "error", err)
		}
		traceevent.ClearActiveTraceBuf(tb)
	}
}

// Abort stops the current execution.
func (s *AgentNewServer) Abort() error {
	slog.Info("[AgentNew] Abort called")
	// AgentNew currently has no cancellable in-flight turn API.
	// Keep this command as a no-op to preserve command compatibility.
	return nil
}

func (s *AgentNewServer) syncSessionFromSnapshotLocked() error {
	if s.currentSession == nil || s.agent == nil {
		return nil
	}
	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return nil
	}
	if err := s.currentSession.SaveMessages(snapshot.RecentMessages); err != nil {
		return err
	}
	if s.sessionMgr != nil {
		if err := s.sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	}
	return nil
}

func (s *AgentNewServer) reloadAgentLocked(ctx context.Context) error {
	if s.agent != nil {
		if err := s.agent.Close(); err != nil {
			slog.Warn("[AgentNew] Failed to close previous agent", "error", err)
		}
	}
	ag, err := agent.LoadSession(ctx, s.sessionDir, s.model, s.apiKey, s.eventEmitter)
	if err != nil {
		return err
	}
	s.agent = ag
	return nil
}

func (s *AgentNewServer) applySessionMessagesToSnapshotLocked() {
	if s.currentSession == nil || s.agent == nil {
		return
	}
	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return
	}
	snapshot.RecentMessages = s.currentSession.GetMessages()
	snapshot.AgentState.SessionID = s.sessionID
	if s.model != nil && s.model.ContextWindow > 0 {
		snapshot.AgentState.TokensLimit = s.model.ContextWindow
	}
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
func (s *AgentNewServer) GetState() (*rpc.SessionState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return &rpc.SessionState{}, nil
	}

	var modelInfo *rpc.ModelInfo
	if s.model != nil {
		info := modelInfoFromModel(*s.model)
		modelInfo = &info
	}

	sessionName := resolveSessionName(s.sessionMgr, s.sessionID)
	if sessionName == "" && s.currentSession != nil {
		sessionName = s.currentSession.GetSessionName()
	}

	sessionFile := ""
	if s.currentSession != nil {
		sessionFile = s.currentSession.GetPath()
	}

	aiLogPath := s.traceOutputPath
	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			aiLogPath = fh.TraceFilePath("")
		}
	}

	startupPath := ""
	currentWorkdir := ""
	if s.workspace != nil {
		startupPath = s.workspace.GetGitRoot()
		currentWorkdir = s.workspace.GetCWD()
	}

	pendingCount := 0
	if s.pendingSteer {
		pendingCount = 1
	}

	var compactionState *rpc.CompactionState
	if s.cfg != nil && s.cfg.Compactor != nil {
		contextWindow := 0
		if s.model != nil {
			contextWindow = s.model.ContextWindow
		}
		tokenLimit := s.cfg.Compactor.MaxTokens
		tokenLimitSource := "max_tokens"
		if tokenLimit <= 0 && contextWindow > 0 {
			tokenLimit = contextWindow
			tokenLimitSource = "context_window"
		}
		if tokenLimit <= 0 {
			tokenLimitSource = ""
		}

		compactionState = &rpc.CompactionState{
			MaxMessages:           s.cfg.Compactor.MaxMessages,
			MaxTokens:             s.cfg.Compactor.MaxTokens,
			KeepRecent:            s.cfg.Compactor.KeepRecent,
			KeepRecentTokens:      s.cfg.Compactor.KeepRecentTokens,
			ReserveTokens:         s.cfg.Compactor.ReserveTokens,
			ToolCallCutoff:        s.cfg.Compactor.ToolCallCutoff,
			ToolSummaryStrategy:   s.cfg.Compactor.ToolSummaryStrategy,
			ToolSummaryAutomation: s.cfg.Compactor.ToolSummaryAutomation,
			ContextWindow:         contextWindow,
			TokenLimit:            tokenLimit,
			TokenLimitSource:      tokenLimitSource,
		}
	}

	return &rpc.SessionState{
		Model:                 modelInfo,
		ThinkingLevel:         s.thinkingLevel,
		IsStreaming:           s.isStreaming,
		IsCompacting:          s.isCompacting,
		SteeringMode:          s.steeringMode,
		FollowUpMode:          s.followUpMode,
		SessionFile:           sessionFile,
		SessionID:             s.sessionID,
		SessionName:           sessionName,
		AIPid:                 os.Getpid(),
		AILogPath:             aiLogPath,
		AIWorkingDir:          currentWorkdir,
		AIStartupPath:         startupPath,
		AutoCompactionEnabled: s.autoCompactionEnabled,
		MessageCount:          len(snapshot.RecentMessages),
		PendingMessageCount:   pendingCount,
		Compaction:            compactionState,
	}, nil
}

func (s *AgentNewServer) ensureConfigLocked() error {
	if s.cfg != nil {
		return nil
	}

	configPath := s.configPath
	if configPath == "" {
		path, err := config.GetDefaultConfigPath()
		if err != nil {
			return err
		}
		configPath = path
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return err
	}

	s.cfg = cfg
	s.configPath = configPath
	return nil
}

func (s *AgentNewServer) withCompactorConfigLocked() error {
	if err := s.ensureConfigLocked(); err != nil {
		return err
	}
	if s.cfg.Compactor == nil {
		s.cfg.Compactor = config.DefaultConfig().Compactor
	}
	return nil
}

func (s *AgentNewServer) persistConfigLocked() {
	if s.cfg == nil || s.configPath == "" {
		return
	}
	if err := config.SaveConfig(s.cfg, s.configPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}
}

func (s *AgentNewServer) GetSessionStats() (*rpc.SessionStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return &rpc.SessionStats{}, nil
	}

	userCount, assistantCount, toolCalls, toolResults, tokens, cost := collectSessionUsage(snapshot.RecentMessages)
	tokens.ActiveWindowTokens = snapshot.EstimateTokens()

	sessionFile := ""
	compactionCount := 0
	if s.currentSession != nil {
		sessionFile = s.currentSession.GetPath()
		compactionCount = s.currentSession.GetCompactionCount()
	}

	workspace := ""
	currentWorkdir := ""
	if s.workspace != nil {
		workspace = s.workspace.GetGitRoot()
		currentWorkdir = s.workspace.GetCWD()
	}

	return &rpc.SessionStats{
		SessionFile:       sessionFile,
		SessionID:         s.sessionID,
		UserMessages:      userCount,
		AssistantMessages: assistantCount,
		ToolCalls:         toolCalls,
		ToolResults:       toolResults,
		TotalMessages:     len(snapshot.RecentMessages),
		CompactionCount:   compactionCount,
		Tokens:            tokens,
		Cost:              cost,
		Workspace:         workspace,
		CurrentWorkdir:    currentWorkdir,
	}, nil
}

func (s *AgentNewServer) GetCommands() ([]rpc.SlashCommand, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	skills := make([]skill.Skill, 0, len(s.skills))
	for _, sk := range s.skills {
		if sk == nil {
			continue
		}
		skills = append(skills, *sk)
	}
	return buildSkillCommands(skills), nil
}

func (s *AgentNewServer) GetAvailableModels() ([]rpc.ModelInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureConfigLocked(); err != nil {
		return nil, err
	}
	specs, modelsPath, err := loadModelSpecs(s.cfg)
	if err != nil {
		return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	specs = filterModelSpecsWithKeys(specs)
	models := make([]rpc.ModelInfo, 0, len(specs))
	for _, spec := range specs {
		models = append(models, modelInfoFromSpec(spec))
	}
	return models, nil
}

func (s *AgentNewServer) applyModelSpecLocked(spec config.ModelSpec) (*rpc.ModelInfo, error) {
	newAPIKey, err := config.ResolveAPIKey(spec.Provider)
	if err != nil {
		return nil, err
	}

	if s.model == nil {
		s.model = &llm.Model{}
	}
	s.model.ID = spec.ID
	s.model.Provider = spec.Provider
	s.model.BaseURL = spec.BaseURL
	s.model.API = spec.API
	s.model.ContextWindow = spec.ContextWindow
	s.model.MaxTokens = spec.MaxTokens
	s.apiKey = newAPIKey

	if err := s.ensureConfigLocked(); err == nil {
		s.cfg.Model.ID = spec.ID
		s.cfg.Model.Provider = spec.Provider
		s.cfg.Model.BaseURL = spec.BaseURL
		s.cfg.Model.API = spec.API
		s.cfg.Model.MaxTokens = spec.MaxTokens
		s.persistConfigLocked()
	}

	if err := s.reloadAgentLocked(context.Background()); err != nil {
		return nil, err
	}
	s.applySessionMessagesToSnapshotLocked()

	info := modelInfoFromSpec(spec)
	return &info, nil
}

func (s *AgentNewServer) SetModel(provider, modelID string) (*rpc.ModelInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	if provider == "" || modelID == "" {
		return nil, fmt.Errorf("provider and modelId are required")
	}
	if err := s.ensureConfigLocked(); err != nil {
		return nil, err
	}

	specs, modelsPath, err := loadModelSpecs(s.cfg)
	if err != nil {
		return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	filtered := filterModelSpecsWithKeys(specs)
	spec, ok := findModelSpec(filtered, provider, modelID)
	if !ok {
		return nil, fmt.Errorf("model not found: %s/%s", provider, modelID)
	}
	return s.applyModelSpecLocked(spec)
}

func (s *AgentNewServer) CycleModel() (*rpc.CycleModelResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureConfigLocked(); err != nil {
		return nil, err
	}

	specs, modelsPath, err := loadModelSpecs(s.cfg)
	if err != nil {
		return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	filtered := filterModelSpecsWithKeys(specs)
	if len(filtered) <= 1 {
		return nil, nil
	}

	curProvider := ""
	curID := ""
	if s.model != nil {
		curProvider = s.model.Provider
		curID = s.model.ID
	}

	index := -1
	for i, spec := range filtered {
		if strings.EqualFold(spec.Provider, curProvider) && spec.ID == curID {
			index = i
			break
		}
	}
	next := filtered[0]
	if index >= 0 {
		next = filtered[(index+1)%len(filtered)]
	}

	info, err := s.applyModelSpecLocked(next)
	if err != nil {
		return nil, err
	}
	return &rpc.CycleModelResult{
		Model:         *info,
		ThinkingLevel: s.thinkingLevel,
		IsScoped:      false,
	}, nil
}

func (s *AgentNewServer) ClearSession() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentSession != nil {
		if err := s.currentSession.Clear(); err != nil {
			return err
		}
	}
	if snapshot := s.agent.GetSnapshot(); snapshot != nil {
		snapshot.RecentMessages = nil
		snapshot.AgentState.TokensUsed = 0
	}
	if s.sessionMgr != nil {
		if err := s.sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	}
	return nil
}

func (s *AgentNewServer) NewSession(name, title string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionMgr == nil {
		return "", fmt.Errorf("session manager is not available")
	}

	name = strings.TrimSpace(name)
	title = strings.TrimSpace(title)
	if name == "" {
		name = time.Now().Format("20060102-150405")
	}
	if title == "" {
		title = name
	}

	newSess, err := s.sessionMgr.CreateSession(name, title)
	if err != nil {
		return "", err
	}
	newSessionID := newSess.GetID()
	if err := s.sessionMgr.SetCurrent(newSessionID); err != nil {
		return "", err
	}
	if err := s.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	s.currentSession = newSess
	s.sessionID = newSessionID
	s.sessionDir = newSess.GetDir()

	if err := s.reloadAgentLocked(context.Background()); err != nil {
		return "", err
	}
	s.applySessionMessagesToSnapshotLocked()
	return newSessionID, nil
}

func (s *AgentNewServer) ListSessions() ([]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.sessionMgr == nil {
		return nil, fmt.Errorf("session manager is not available")
	}
	sessions, err := s.sessionMgr.ListSessions()
	if err != nil {
		return nil, err
	}

	startupPath := ""
	currentWorkdir := ""
	if s.workspace != nil {
		startupPath = s.workspace.GetGitRoot()
		currentWorkdir = s.workspace.GetCWD()
	}

	result := make([]any, len(sessions))
	for i := range sessions {
		sessions[i].Workspace = startupPath
		sessions[i].CurrentWorkdir = currentWorkdir
		result[i] = sessions[i]
	}
	return result, nil
}

func (s *AgentNewServer) SwitchSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("session id is required")
	}

	var newSess *session.Session
	var newSessionID string
	var err error

	if strings.Contains(id, string(os.PathSeparator)) || strings.HasSuffix(id, ".jsonl") {
		sessionPath, err := normalizeSessionPath(id)
		if err != nil {
			return err
		}
		sessionDir := sessionPath
		if strings.HasSuffix(sessionPath, ".jsonl") {
			sessionDir = filepath.Dir(sessionPath)
		}
		opts := session.DefaultLoadOptions()
		newSess, err = session.LoadSessionLazy(sessionDir, opts)
		if err != nil {
			return err
		}
		newSessionID = newSess.GetID()
		s.sessionMgr = session.NewSessionManager(filepath.Dir(sessionDir))
		if err := s.sessionMgr.SetCurrent(newSessionID); err != nil {
			return err
		}
		if err := s.sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	} else {
		if s.sessionMgr == nil {
			return fmt.Errorf("session manager is not available")
		}
		if err := s.sessionMgr.SetCurrent(id); err != nil {
			return err
		}
		newSess, err = s.sessionMgr.GetSession(id)
		if err != nil {
			return err
		}
		newSessionID = newSess.GetID()
		if err := s.sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	}

	s.currentSession = newSess
	s.sessionID = newSessionID
	s.sessionDir = newSess.GetDir()

	if err := s.reloadAgentLocked(context.Background()); err != nil {
		return err
	}
	s.applySessionMessagesToSnapshotLocked()

	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			fh.SetSessionID(newSessionID)
		}
	}

	return nil
}

func (s *AgentNewServer) DeleteSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionMgr == nil {
		return fmt.Errorf("session manager is not available")
	}
	return s.sessionMgr.DeleteSession(strings.TrimSpace(id))
}

func (s *AgentNewServer) Compact() (*rpc.CompactResult, error) {
	return nil, fmt.Errorf("compact is not supported in AgentNew mode")
}

func (s *AgentNewServer) SetAutoCompaction(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.autoCompactionEnabled = enabled
	if err := s.withCompactorConfigLocked(); err != nil {
		return err
	}
	s.cfg.Compactor.AutoCompact = enabled
	s.persistConfigLocked()
	return nil
}

func (s *AgentNewServer) SetToolCallCutoff(cutoff int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cutoff < 0 {
		return fmt.Errorf("cutoff must be >= 0")
	}
	if err := s.withCompactorConfigLocked(); err != nil {
		return err
	}
	s.cfg.Compactor.ToolCallCutoff = cutoff
	s.persistConfigLocked()
	return nil
}

func (s *AgentNewServer) SetToolSummaryStrategy(strategy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	strategy = strings.ToLower(strings.TrimSpace(strategy))
	valid := map[string]bool{"llm": true, "heuristic": true, "off": true}
	if !valid[strategy] {
		return fmt.Errorf("invalid tool summary strategy")
	}
	if err := s.withCompactorConfigLocked(); err != nil {
		return err
	}
	s.cfg.Compactor.ToolSummaryStrategy = strategy
	s.persistConfigLocked()
	return nil
}

func (s *AgentNewServer) SetToolSummaryAutomation(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mode = strings.ToLower(strings.TrimSpace(mode))
	valid := map[string]bool{"off": true, "fallback": true, "always": true}
	if !valid[mode] {
		return fmt.Errorf("invalid tool summary automation mode")
	}
	if err := s.withCompactorConfigLocked(); err != nil {
		return err
	}
	s.cfg.Compactor.ToolSummaryAutomation = mode
	s.persistConfigLocked()
	return nil
}

func (s *AgentNewServer) SetThinkingLevel(level string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	level = strings.ToLower(strings.TrimSpace(level))
	valid := map[string]bool{
		"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true,
	}
	if !valid[level] {
		return "", fmt.Errorf("invalid thinking level")
	}
	s.thinkingLevel = level
	return level, nil
}

func (s *AgentNewServer) CycleThinkingLevel() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cycle := []string{"off", "minimal", "low", "medium", "high", "xhigh"}
	next := cycle[0]
	for i, level := range cycle {
		if level == s.thinkingLevel {
			next = cycle[(i+1)%len(cycle)]
			break
		}
	}
	s.thinkingLevel = next
	return next, nil
}

func (s *AgentNewServer) SetSteeringMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "all" && mode != "one-at-a-time" {
		return fmt.Errorf("invalid steering mode")
	}
	s.steeringMode = mode
	return nil
}

func (s *AgentNewServer) SetFollowUpMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "all" && mode != "one-at-a-time" {
		return fmt.Errorf("invalid follow-up mode")
	}
	s.followUpMode = mode
	return nil
}

func (s *AgentNewServer) GetLastAssistantText() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return "", nil
	}
	for i := len(snapshot.RecentMessages) - 1; i >= 0; i-- {
		if snapshot.RecentMessages[i].Role == "assistant" {
			return snapshot.RecentMessages[i].ExtractText(), nil
		}
	}
	return "", nil
}

func (s *AgentNewServer) GetForkMessages() ([]rpc.ForkMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.syncSessionFromSnapshotLocked(); err != nil {
		slog.Warn("[AgentNew] Failed to sync session before get_fork_messages", "error", err)
	}
	if s.currentSession == nil {
		return nil, nil
	}
	messages := s.currentSession.GetUserMessagesForForking()
	result := make([]rpc.ForkMessage, 0, len(messages))
	for _, msg := range messages {
		result = append(result, rpc.ForkMessage{
			EntryID: msg.EntryID,
			Text:    msg.Text,
		})
	}
	return result, nil
}

func (s *AgentNewServer) GetTree() ([]rpc.TreeEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.syncSessionFromSnapshotLocked(); err != nil {
		slog.Warn("[AgentNew] Failed to sync session before get_tree", "error", err)
	}
	if s.currentSession == nil {
		return nil, nil
	}
	entries := s.currentSession.GetEntries()
	return buildTreeEntries(entries, s.currentSession.GetLeafID()), nil
}

func (s *AgentNewServer) ResumeOnBranch(entryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isStreaming {
		return fmt.Errorf("agent is busy")
	}
	if s.currentSession == nil {
		return fmt.Errorf("session is not available")
	}

	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return fmt.Errorf("entryId is required")
	}

	if err := s.syncSessionFromSnapshotLocked(); err != nil {
		slog.Warn("[AgentNew] Failed to sync session before resume_on_branch", "error", err)
	}

	if entryID == "root" {
		s.currentSession.ResetLeaf()
	} else {
		if err := s.currentSession.Branch(entryID); err != nil {
			return err
		}
	}

	s.applySessionMessagesToSnapshotLocked()
	if s.sessionMgr != nil {
		if err := s.sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	}
	return nil
}

func (s *AgentNewServer) Fork(entryID string) (*rpc.ForkResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sessionMgr == nil || s.currentSession == nil {
		return nil, fmt.Errorf("session is not available")
	}
	if err := s.syncSessionFromSnapshotLocked(); err != nil {
		slog.Warn("[AgentNew] Failed to sync session before fork", "error", err)
	}

	entryID = strings.TrimSpace(entryID)
	entry, ok := s.currentSession.GetEntry(entryID)
	if !ok || entry.Type != session.EntryTypeMessage || entry.Message == nil || entry.Message.Role != "user" {
		return nil, fmt.Errorf("invalid entryId: %s", entryID)
	}

	name := fmt.Sprintf("fork-%s", time.Now().Format("20060102-150405"))
	title := "Forked Session"
	newSess, err := s.sessionMgr.ForkSessionFrom(s.currentSession, entry.ParentID, name, title)
	if err != nil {
		return nil, err
	}
	newSessionID := newSess.GetID()
	if err := s.sessionMgr.SetCurrent(newSessionID); err != nil {
		return nil, err
	}
	if err := s.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	s.currentSession = newSess
	s.sessionID = newSessionID
	s.sessionDir = newSess.GetDir()

	if err := s.reloadAgentLocked(context.Background()); err != nil {
		return nil, err
	}
	s.applySessionMessagesToSnapshotLocked()

	return &rpc.ForkResult{
		Cancelled: false,
		Text:      entry.Message.ExtractText(),
	}, nil
}

func (s *AgentNewServer) SetSessionName(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.currentSession == nil {
		return fmt.Errorf("session is not available")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	if _, err := s.currentSession.AppendSessionInfo(name, ""); err != nil {
		return err
	}
	if s.sessionMgr != nil {
		if err := s.sessionMgr.UpdateSessionName(s.sessionID, name, ""); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		if err := s.sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	}
	return nil
}

func (s *AgentNewServer) SetAutoRetry(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoRetry = enabled
	return nil
}

func (s *AgentNewServer) AbortRetry() error {
	return s.Abort()
}

func (s *AgentNewServer) Bash(command string) (*rpc.BashResult, error) {
	return nil, fmt.Errorf("bash is not supported in AgentNew mode")
}

func (s *AgentNewServer) AbortBash() error {
	return fmt.Errorf("abort_bash is not supported in AgentNew mode")
}

func (s *AgentNewServer) ExportHTML(path string) (string, error) {
	return "", fmt.Errorf("export_html is not supported in AgentNew mode")
}

func (s *AgentNewServer) SetTraceEvents(events []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(events) == 0 {
		return traceevent.ResetToDefaultEvents(), nil
	}

	normalized := make([]string, 0, len(events))
	for _, event := range events {
		event = strings.ToLower(strings.TrimSpace(event))
		if event != "" {
			normalized = append(normalized, event)
		}
	}
	if len(normalized) == 0 {
		return traceevent.ResetToDefaultEvents(), nil
	}

	applyExpanded := func(expanded []string, replace bool) []string {
		if replace {
			traceevent.DisableAllEvents()
		}
		for _, eventName := range expanded {
			traceevent.EnableEvent(eventName)
		}
		return traceevent.GetEnabledEvents()
	}

	switch normalized[0] {
	case "on", "default":
		traceevent.SetEnableUnknownEvents(false)
		return traceevent.ResetToDefaultEvents(), nil
	case "all":
		expanded, _ := traceevent.ExpandEventSelectors([]string{"all"})
		_ = applyExpanded(expanded, true)
		traceevent.SetEnableUnknownEvents(true)
		return traceevent.GetEnabledEvents(), nil
	case "off", "none":
		traceevent.DisableAllEvents()
		return []string{}, nil
	case "enable":
		if len(normalized) == 1 {
			return nil, fmt.Errorf("trace-events enable requires at least one selector")
		}
		expanded, unknown := traceevent.ExpandEventSelectors(normalized[1:])
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
		}
		return applyExpanded(expanded, false), nil
	case "disable":
		if len(normalized) == 1 {
			return nil, fmt.Errorf("trace-events disable requires at least one selector")
		}
		expanded, unknown := traceevent.ExpandEventSelectors(normalized[1:])
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
		}
		for _, eventName := range expanded {
			traceevent.DisableEvent(eventName)
		}
		return traceevent.GetEnabledEvents(), nil
	default:
		expanded, unknown := traceevent.ExpandEventSelectors(normalized)
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
		}
		traceevent.SetEnableUnknownEvents(false)
		return applyExpanded(expanded, true), nil
	}
}

func (s *AgentNewServer) GetTraceEvents() ([]string, error) {
	return traceevent.GetEnabledEvents(), nil
}

func (s *AgentNewServer) GetWorkflowStatus() (*rpc.WorkflowState, error) {
	return &rpc.WorkflowState{
		Phase:      "not_started",
		LastUpdate: time.Now().UTC().Format(time.RFC3339),
	}, nil
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

// Emit forwards AgentNew events to RPC clients without dropping fields.
func (a *rpcEventEmitterAdapter) Emit(event agent.AgentEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.server == nil {
		return
	}

	// Emit via RPC server
	if emitter, ok := a.server.(interface{ EmitEvent(any) }); ok {
		emitter.EmitEvent(event)
	}
}

type rpcCommandRegisterFunc func(registrar *rpcHandlerRegistrar)

type rpcCommandRegistry struct {
	mu       sync.RWMutex
	commands map[string]rpcCommandRegisterFunc
}

type rpcHandlerRegistrar struct {
	server *rpc.Server
	agent  *AgentNewServer
}

func newRPCCommandRegistry() *rpcCommandRegistry {
	return &rpcCommandRegistry{
		commands: make(map[string]rpcCommandRegisterFunc),
	}
}

func (r *rpcCommandRegistry) Register(name string, fn rpcCommandRegisterFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[name] = fn
}

func (r *rpcCommandRegistry) Apply(server *rpc.Server, agentServer *AgentNewServer) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	registrar := &rpcHandlerRegistrar{
		server: server,
		agent:  agentServer,
	}
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r.commands[name](registrar)
	}
}

func (r *rpcCommandRegistry) registerBuiltinCommands() {
	r.Register(rpc.CommandPrompt, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetPromptHandler(func(req rpc.PromptRequest) error {
			message := strings.TrimSpace(req.Message)
			if message == "" {
				return fmt.Errorf("empty prompt message")
			}
			return registrar.agent.Prompt(registrar.server.Context(), message)
		})
	})

	r.Register(rpc.CommandSteer, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSteerHandler(func(message string) error {
			message = strings.TrimSpace(message)
			if message == "" {
				return fmt.Errorf("empty steer message")
			}
			return registrar.agent.Steer(registrar.server.Context(), message)
		})
	})

	r.Register(rpc.CommandFollowUp, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetFollowUpHandler(func(message string) error {
			message = strings.TrimSpace(message)
			if message == "" {
				return fmt.Errorf("empty follow-up message")
			}
			return registrar.agent.FollowUp(registrar.server.Context(), message)
		})
	})

	r.Register(rpc.CommandAbort, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetAbortHandler(func() error {
			return registrar.agent.Abort()
		})
	})

	r.Register(rpc.CommandGetMessages, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetMessagesHandler(func() ([]any, error) {
			return registrar.agent.GetMessages(), nil
		})
	})

	r.Register(rpc.CommandGetState, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetStateHandler(func() (*rpc.SessionState, error) {
			return registrar.agent.GetState()
		})
	})

	r.Register(rpc.CommandGetSessionStats, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetSessionStatsHandler(func() (*rpc.SessionStats, error) {
			return registrar.agent.GetSessionStats()
		})
	})

	r.Register(rpc.CommandGetCommands, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetCommandsHandler(func() ([]rpc.SlashCommand, error) {
			return registrar.agent.GetCommands()
		})
	})

	r.Register(rpc.CommandNewSession, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetNewSessionHandler(func(name, title string) (string, error) {
			return registrar.agent.NewSession(name, title)
		})
	})

	r.Register(rpc.CommandClearSession, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetClearSessionHandler(func() error {
			return registrar.agent.ClearSession()
		})
	})

	r.Register(rpc.CommandListSessions, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetListSessionsHandler(func() ([]any, error) {
			return registrar.agent.ListSessions()
		})
	})

	r.Register(rpc.CommandSwitchSession, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSwitchSessionHandler(func(id string) error {
			return registrar.agent.SwitchSession(id)
		})
	})

	r.Register(rpc.CommandDeleteSession, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetDeleteSessionHandler(func(id string) error {
			return registrar.agent.DeleteSession(id)
		})
	})

	r.Register(rpc.CommandCompact, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetCompactHandler(func() (*rpc.CompactResult, error) {
			return registrar.agent.Compact()
		})
	})

	r.Register(rpc.CommandGetAvailableModels, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetAvailableModelsHandler(func() ([]rpc.ModelInfo, error) {
			return registrar.agent.GetAvailableModels()
		})
	})

	r.Register(rpc.CommandSetModel, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetModelHandler(func(provider, modelID string) (*rpc.ModelInfo, error) {
			return registrar.agent.SetModel(provider, modelID)
		})
	})

	r.Register(rpc.CommandCycleModel, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetCycleModelHandler(func() (*rpc.CycleModelResult, error) {
			return registrar.agent.CycleModel()
		})
	})

	r.Register(rpc.CommandSetAutoCompaction, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetAutoCompactionHandler(func(enabled bool) error {
			return registrar.agent.SetAutoCompaction(enabled)
		})
	})

	r.Register(rpc.CommandSetToolCallCutoff, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetToolCallCutoffHandler(func(cutoff int) error {
			return registrar.agent.SetToolCallCutoff(cutoff)
		})
	})

	r.Register(rpc.CommandSetToolSummaryStrategy, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetToolSummaryStrategyHandler(func(strategy string) error {
			return registrar.agent.SetToolSummaryStrategy(strategy)
		})
	})

	r.Register(rpc.CommandSetToolSummaryAutomation, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetToolSummaryAutomationHandler(func(mode string) error {
			return registrar.agent.SetToolSummaryAutomation(mode)
		})
	})

	r.Register(rpc.CommandSetThinkingLevel, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetThinkingLevelHandler(func(level string) (string, error) {
			return registrar.agent.SetThinkingLevel(level)
		})
	})

	r.Register(rpc.CommandCycleThinkingLevel, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetCycleThinkingLevelHandler(func() (string, error) {
			return registrar.agent.CycleThinkingLevel()
		})
	})

	r.Register(rpc.CommandSetSteeringMode, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetSteeringModeHandler(func(mode string) error {
			return registrar.agent.SetSteeringMode(mode)
		})
	})

	r.Register(rpc.CommandSetFollowUpMode, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetFollowUpModeHandler(func(mode string) error {
			return registrar.agent.SetFollowUpMode(mode)
		})
	})

	r.Register(rpc.CommandGetLastAssistantText, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetLastAssistantTextHandler(func() (string, error) {
			return registrar.agent.GetLastAssistantText()
		})
	})

	r.Register(rpc.CommandGetForkMessages, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetForkMessagesHandler(func() ([]rpc.ForkMessage, error) {
			return registrar.agent.GetForkMessages()
		})
	})

	r.Register(rpc.CommandFork, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetForkHandler(func(entryID string) (*rpc.ForkResult, error) {
			return registrar.agent.Fork(entryID)
		})
	})

	r.Register(rpc.CommandGetTree, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetTreeHandler(func() ([]rpc.TreeEntry, error) {
			return registrar.agent.GetTree()
		})
	})

	r.Register(rpc.CommandResumeOnBranch, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetResumeOnBranchHandler(func(entryID string) error {
			return registrar.agent.ResumeOnBranch(entryID)
		})
	})

	r.Register(rpc.CommandSetSessionName, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetSessionNameHandler(func(name string) error {
			return registrar.agent.SetSessionName(name)
		})
	})

	r.Register(rpc.CommandSetAutoRetry, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetAutoRetryHandler(func(enabled bool) error {
			return registrar.agent.SetAutoRetry(enabled)
		})
	})

	r.Register(rpc.CommandAbortRetry, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetAbortRetryHandler(func() error {
			return registrar.agent.AbortRetry()
		})
	})

	r.Register(rpc.CommandBash, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetBashHandler(func(command string) (*rpc.BashResult, error) {
			return registrar.agent.Bash(command)
		})
	})

	r.Register(rpc.CommandAbortBash, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetAbortBashHandler(func() error {
			return registrar.agent.AbortBash()
		})
	})

	r.Register(rpc.CommandExportHTML, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetExportHTMLHandler(func(path string) (string, error) {
			return registrar.agent.ExportHTML(path)
		})
	})

	r.Register(rpc.CommandSetTraceEvents, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetSetTraceEventsHandler(func(events []string) ([]string, error) {
			return registrar.agent.SetTraceEvents(events)
		})
	})

	r.Register(rpc.CommandGetTraceEvents, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetTraceEventsHandler(func() ([]string, error) {
			return registrar.agent.GetTraceEvents()
		})
	})

	r.Register(rpc.CommandGetWorkflowStatus, func(registrar *rpcHandlerRegistrar) {
		registrar.server.SetGetWorkflowStatusHandler(func() (*rpc.WorkflowState, error) {
			return registrar.agent.GetWorkflowStatus()
		})
	})
}

// SetupAgentNewHandlers configures RPC server handlers to use AgentNew.
func SetupAgentNewHandlers(server *rpc.Server, agentNewServer *AgentNewServer) {
	registry := newRPCCommandRegistry()
	registry.registerBuiltinCommands()
	registry.Apply(server, agentNewServer)
	slog.Info("[AgentNew] RPC handlers configured", "count", len(registry.commands))
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
		"",
		nil,
		"",
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
