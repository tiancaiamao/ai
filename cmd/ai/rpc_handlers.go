package main

import (
	"context"
	"encoding/json"
	"errors"
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

// RPCOption configures runRPC behavior.
type RPCOption struct {
	SystemPrompt string
	Tools        []string
	KeepTools    bool
	CMPrompt     string
}

func runRPC(sessionPath string, debugAddr string, maxTurns int, input io.Reader, output io.Writer, opts ...RPCOption) error {
	var opt RPCOption
	if len(opts) > 0 {
		opt = opts[0]
	}
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
		opt.SystemPrompt,
		opt.CMPrompt,
		opt.Tools,
		opt.KeepTools,
	)
	if err != nil {
		return fmt.Errorf("failed to create agent server: %w", err)
	}
	defer agentServer.Close()

	// Set max turns if specified (typically for headless mode)
	if maxTurns > 0 {
		agentServer.GetAgent().SetMaxTurns(maxTurns)
		slog.Info("[AgentNew] Max turns limit set", "max_turns", maxTurns)
	}

	// Create RPC server
	server := rpc.NewServer()
	server.SetOutput(output)
	agentServer.SetEventEmitter(server)
	agentServer.SetContext(server.Context())

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
	ctx        context.Context // base context for operations (set from RPC server)

	// Configuration
	model           *llm.Model
	apiKey          string
	configPath      string
	cfg             *config.Config
	traceOutputPath string
	sessionMgr      *session.SessionManager
	currentSession  *session.Session

	// CLI options
	systemPrompt string
	cmPrompt     string
	toolList     []string
	keepTools    bool

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

	// Cancellation support
	cancel context.CancelFunc

	// Command registry (unified mechanism)
	commands *agent.CommandRegistry
}

// Compile-time interface check: AgentNewServer implements agent.AgentBackend.
var _ agent.AgentBackend = (*AgentNewServer)(nil)

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
	systemPrompt string,
	cmPrompt string,
	toolList []string,
	keepTools bool,
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

	agentServer := &AgentNewServer{
		agent:                 ag,
		sessionDir:            sessionDir,
		sessionID:             sessionID,
		ctx:                   context.Background(), // will be updated via SetContext
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
		systemPrompt:          systemPrompt,
		cmPrompt:              cmPrompt,
		toolList:              toolList,
		keepTools:             keepTools,
		steeringMode:          "all",
		followUpMode:          "all",
		thinkingLevel:         "high",
		autoCompactionEnabled: autoCompactionEnabled,
		commands:              agent.NewCommandRegistry(),
	}

	// Inject skills and project context into agent's system prompt
	skillsText := formatSkillsForPrompt(skills)
	var projectContext string
	if workspace != nil {
		projectContext = prompt.BuildProjectContext(workspace.GetCWD())
	}
	agentServer.agent.SetSystemPromptExtras(skillsText, projectContext)

	// Set custom system prompt from --system-prompt flag if provided
	if systemPrompt != "" {
		agentServer.agent.SetCustomSystemPrompt(systemPrompt)
		slog.Info("[AgentNew] Custom system prompt applied from CLI flag", "length", len(systemPrompt))
	}

	// Register built-in commands
	agentServer.registerBuiltinCommands()

	return agentServer, nil
}

// SetEventEmitter sets the event emitter (typically the RPC server).
func (s *AgentNewServer) SetEventEmitter(emitter interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if adapter, ok := s.eventEmitter.(*rpcEventEmitterAdapter); ok {
		adapter.server = emitter
	}
}

// SetContext sets the base context for operations, replacing the default context.Background().
// This should be called after construction to enable proper tracing context propagation.
func (s *AgentNewServer) SetContext(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctx = ctx
}

// context returns the base context for operations.
func (s *AgentNewServer) context() context.Context {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// GetSnapshot returns the current snapshot from the agent.
func (s *AgentNewServer) GetSnapshot() *agentctx.ContextSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agent.GetSnapshot()
}

// GetAgent returns the underlying AgentNew instance.
func (s *AgentNewServer) GetAgent() *agent.AgentNew {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.agent
}

// Prompt handles the prompt command using AgentNew.
// It implements the two-layer loop pattern from the old Agent architecture:
//
//	Inner loop: ExecuteTurn processes one user message (LLM → tools → LLM → ...)
//	Outer loop: drains followUpQueue after each turn completes
//
// Steer cancels the current turn's context, causing ExecuteTurn to return;
// the outer loop then picks up the queued message from followUpQueue.
// FollowUp just queues a message; it's picked up after the current turn finishes naturally.
func (s *AgentNewServer) Prompt(ctx context.Context, message string) error {
	s.mu.Lock()
	if s.isStreaming {
		s.mu.Unlock()
		return fmt.Errorf("agent is busy")
	}
	s.isStreaming = true

	// Create a cancellable context for this turn
	turnCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.mu.Unlock()

	defer func() {
		cancel() // Release context-scoped resources for the initial turn
		s.mu.Lock()
		s.isStreaming = false
		s.pendingSteer = false
		s.cancel = nil
		s.mu.Unlock()
	}()

	beforeCount := 0
	if snapshot := s.agent.GetSnapshot(); snapshot != nil {
		beforeCount = len(snapshot.RecentMessages)
	}

	turnTraceCtx, finalizeTurnTrace := s.beginTurnTrace(turnCtx)

	s.emitEvent(agent.NewAgentStartEvent())
	s.emitEvent(agent.NewTurnStartEvent())

	// --- Inner loop: execute one turn ---
	// Context cancellation (from Steer) causes ExecuteTurn to return.
	// We don't treat context.Canceled as a fatal error here — the outer loop
	// will drain any follow-up messages that were queued by Steer/FollowUp.
	execErr := s.agent.ExecuteTurn(turnTraceCtx, message)
	if execErr != nil && !errors.Is(execErr, context.Canceled) {
		// Check if there are follow-ups queued — if so, don't abort;
		// let the outer loop process them (e.g., Steer cancels then queues).
		if s.agent.PendingFollowUpCount() == 0 {
			s.emitEvent(agent.NewErrorEvent(execErr))
			s.emitEvent(agent.NewAgentEndEvent(nil))
			finalizeTurnTrace()
			return fmt.Errorf("failed to execute turn: %w", execErr)
		}
		slog.Info("[AgentNew] Turn ended with error but follow-ups pending, continuing",
			"error", execErr,
			"pending_count", s.agent.PendingFollowUpCount(),
		)
	}

	// Finalize the first turn's trace
	finalizeTurnTrace()

	s.mu.Lock()
	if err := s.syncSessionFromSnapshotLocked(); err != nil {
		slog.Warn("[AgentNew] Failed to sync session after prompt", "error", err)
	}
	s.mu.Unlock()

	assistantMessage, toolResults := s.collectPostTurnResults(beforeCount)
	s.emitEvent(agent.NewTurnEndEvent(assistantMessage, toolResults))

	// --- Outer loop: drain follow-up queue ---
	// This handles both Steer (cancel + queue) and FollowUp (queue only).
	// After the inner turn completes (either normally or via cancellation),
	// we drain any messages that were queued during that turn.
	for {
		followUps := s.agent.DrainFollowUps()
		if len(followUps) == 0 {
			break
		}

		for _, followUpMsg := range followUps {
			slog.Info("[AgentNew] Processing follow-up from queue",
				"message", followUpMsg,
			)

			s.mu.Lock()
			s.pendingSteer = false
			s.mu.Unlock()

			// Create fresh context for this follow-up turn
			followUpCtx, followUpCancel := context.WithCancel(ctx)
			s.mu.Lock()
			s.cancel = followUpCancel
			s.mu.Unlock()

			beforeCount = 0
			if snapshot := s.agent.GetSnapshot(); snapshot != nil {
				beforeCount = len(snapshot.RecentMessages)
			}

			followUpTraceCtx, finalizeFollowUpTrace := s.beginTurnTrace(followUpCtx)

			s.emitEvent(agent.NewTurnStartEvent())

			followUpErr := s.agent.ExecuteTurn(followUpTraceCtx, followUpMsg)
			if followUpErr != nil && !errors.Is(followUpErr, context.Canceled) {
				if s.agent.PendingFollowUpCount() == 0 {
					s.emitEvent(agent.NewErrorEvent(followUpErr))
					finalizeFollowUpTrace()
					followUpCancel()
					s.emitEvent(agent.NewAgentEndEvent(nil))
					return fmt.Errorf("failed to execute follow-up turn: %w", followUpErr)
				}
				slog.Info("[AgentNew] Follow-up turn ended with error but more follow-ups pending",
					"error", followUpErr,
				)
			}

			finalizeFollowUpTrace()

			s.mu.Lock()
			if err := s.syncSessionFromSnapshotLocked(); err != nil {
				slog.Warn("[AgentNew] Failed to sync session after follow-up", "error", err)
			}
			s.mu.Unlock()

			assistantMessage, toolResults = s.collectPostTurnResults(beforeCount)
			s.emitEvent(agent.NewTurnEndEvent(assistantMessage, toolResults))

			// Cancel the follow-up context to release associated resources
			followUpCancel()
		}
	}

	s.emitEvent(agent.NewAgentEndEvent(nil))
	return nil
}

func (s *AgentNewServer) emitEvent(event agent.AgentEvent) {
	if s.eventEmitter != nil {
		s.eventEmitter.Emit(event)
	}

	traceFields := []traceevent.Field{
		{Key: "event_at", Value: event.EventAt},
	}
	if event.Message != nil {
		traceFields = append(traceFields,
			traceevent.Field{Key: "role", Value: event.Message.Role},
			traceevent.Field{Key: "stop_reason", Value: event.Message.StopReason},
		)
	}
	if event.ToolName != "" {
		traceFields = append(traceFields,
			traceevent.Field{Key: "tool_name", Value: event.ToolName},
			traceevent.Field{Key: "tool_call_id", Value: event.ToolCallID},
		)
	}
	if event.Error != "" {
		traceFields = append(traceFields, traceevent.Field{Key: "error_message", Value: event.Error})
	}
	if event.ErrorStack != "" {
		traceFields = append(traceFields, traceevent.Field{Key: "error_stack", Value: event.ErrorStack})
	}

	traceevent.Log(s.context(), traceevent.CategoryEvent, event.Type, traceFields...)
	if event.Type == agent.EventError {
		traceevent.Log(s.context(), traceevent.CategoryEvent, "run_loop_error", traceFields...)
	}

	switch update := event.AssistantMessageEvent.(type) {
	case agent.AssistantMessageEvent:
		s.traceAssistantMessageUpdate(update)
	case *agent.AssistantMessageEvent:
		if update != nil {
			s.traceAssistantMessageUpdate(*update)
		}
	}
}

func (s *AgentNewServer) traceAssistantMessageUpdate(update agent.AssistantMessageEvent) {
	traceevent.Log(s.context(), traceevent.CategoryEvent, "message_update",
		traceevent.Field{Key: "update_type", Value: update.Type},
		traceevent.Field{Key: "content_index", Value: update.ContentIndex},
	)

	switch update.Type {
	case "text_start":
		traceevent.Log(s.context(), traceevent.CategoryLLM, "assistant_text",
			traceevent.Field{Key: "state", Value: "start"},
		)
	case "text_end":
		traceevent.Log(s.context(), traceevent.CategoryLLM, "assistant_text",
			traceevent.Field{Key: "state", Value: "end"},
		)
	case "text_delta":
		traceevent.Log(s.context(), traceevent.CategoryLLM, "text_delta",
			traceevent.Field{Key: "content_index", Value: update.ContentIndex},
			traceevent.Field{Key: "delta", Value: update.Delta},
		)
	case "thinking_delta":
		traceevent.Log(s.context(), traceevent.CategoryLLM, "thinking_delta",
			traceevent.Field{Key: "content_index", Value: update.ContentIndex},
			traceevent.Field{Key: "delta", Value: update.Delta},
		)
	case "toolcall_delta":
		traceevent.Log(s.context(), traceevent.CategoryLLM, "tool_call_delta",
			traceevent.Field{Key: "content_index", Value: update.ContentIndex},
		)
	}
}

// collectPostTurnResults extracts the last assistant message and tool results from
// new messages added during this turn. It does NOT emit any events — those are
// already streamed in real-time by the agent loop (pkg/agent/loop_normal.go).
// The returned values are used by NewTurnEndEvent.
func (s *AgentNewServer) collectPostTurnResults(beforeCount int) (*agentctx.AgentMessage, []agentctx.AgentMessage) {
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
			msgCopy := msg
			lastAssistant = &msgCopy
		case "toolResult":
			msgCopy := msg
			toolResults = append(toolResults, msgCopy)
		}
	}

	return lastAssistant, toolResults
}

// Steer handles the steer command using AgentNew.
// Steer interrupts the current turn and immediately processes the new input.
// It follows the old Agent pattern: cancel the current context + queue the message.
// Prompt's outer loop will drain the queue and process the message after ExecuteTurn returns.
func (s *AgentNewServer) Steer(ctx context.Context, message string) error {
	s.mu.Lock()
	mode := s.steeringMode
	pending := s.pendingSteer
	isStreaming := s.isStreaming
	cancel := s.cancel
	s.mu.Unlock()

	if mode == "one-at-a-time" && pending {
		return fmt.Errorf("steer already pending")
	}

	// If not currently streaming, execute immediately
	if !isStreaming {
		s.mu.Lock()
		s.pendingSteer = true
		s.mu.Unlock()

		ctx, finalizeTrace := s.beginTurnTrace(ctx)
		defer finalizeTrace()

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

	// Streaming: cancel current turn + queue message
	// This matches the old Agent.Steer pattern:
	// 1. Cancel the current context → ExecuteTurn returns with context.Canceled
	// 2. Queue the message → Prompt's outer loop drains it and processes it
	slog.Info("[AgentNew] Steer during streaming - canceling current turn and queuing message",
		"message", message,
		"steering_mode", mode,
	)

	// Queue the message first (before cancel, to avoid race where Prompt drains before we queue)
	if err := s.agent.QueueFollowUp(message); err != nil {
		slog.Warn("[AgentNew] Failed to queue steer message, canceling turn anyway",
			"message", message,
			"error", err,
		)
		// Still cancel the current turn even if queue is full
		if cancel != nil {
			cancel()
		}
		return fmt.Errorf("failed to queue steer message: %w", err)
	}

	// Cancel the current turn
	if cancel != nil {
		slog.Info("[AgentNew] Canceling current turn for steer")
		cancel()
	}

	s.mu.Lock()
	s.pendingSteer = true
	s.mu.Unlock()

	return nil
}

// FollowUp handles the follow_up command using AgentNew.
// FollowUp queues the message to be processed after the current turn completes (without cancellation).
// Prompt's outer loop will drain the queue after ExecuteTurn finishes naturally.
func (s *AgentNewServer) FollowUp(ctx context.Context, message string) error {
	s.mu.Lock()
	mode := s.followUpMode
	isStreaming := s.isStreaming
	pendingSteer := s.pendingSteer
	s.mu.Unlock()

	if mode == "one-at-a-time" && pendingSteer {
		return fmt.Errorf("follow-up not allowed while steer is pending")
	}

	// If not currently streaming, execute immediately
	if !isStreaming {
		s.mu.Lock()
		s.pendingSteer = true
		s.mu.Unlock()

		ctx, finalizeTrace := s.beginTurnTrace(ctx)
		defer finalizeTrace()

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
			slog.Warn("[AgentNew] Failed to sync session after follow-up", "error", err)
		}
		return nil
	}

	// Streaming: queue the message without canceling
	// Prompt's outer loop will drain it after the current turn completes naturally.
	slog.Info("[AgentNew] Follow-up during streaming - queueing message",
		"message", message,
		"followUpMode", mode,
	)

	if err := s.agent.QueueFollowUp(message); err != nil {
		return fmt.Errorf("failed to queue follow-up message: %w", err)
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
	s.mu.Lock()
	cancel := s.cancel
	isStreaming := s.isStreaming
	s.mu.Unlock()

	slog.Info("[AgentNew] Abort called", "is_streaming", isStreaming)

	if !isStreaming {
		return fmt.Errorf("agent is not executing")
	}

	if cancel != nil {
		slog.Info("[AgentNew] Canceling current turn")
		cancel()
		return nil
	}

	return fmt.Errorf("no cancel function available")
}

func (s *AgentNewServer) syncSessionFromSnapshotLocked() error {
	if s.currentSession == nil || s.agent == nil {
		return nil
	}
	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return nil
	}

	// DON'T use SaveMessages() - it rewrites the entire messages.jsonl file,
	// which overwrites journal entries (like compact events) that were appended.
	// New architecture uses journal-based persistence, which is already handled
	// by the agent layer (journal.AppendMessage, journal.AppendCompact, etc.)
	//
	// Note: This means currentSession.entries will be out of sync with snapshot.RecentMessages,
	// but that's expected - currentSession is kept for legacy compatibility only.

	// if err := s.currentSession.SaveMessages(snapshot.RecentMessages); err != nil {
	// 	return err
	// }

	if s.sessionMgr != nil {
		if err := s.sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	}
	return nil
}

func (s *AgentNewServer) reloadAgentLocked(ctx context.Context) error {
	// Preserve extras from the previous agent before closing it.
	var skillsText, projectContext string
	var thinkingLevel string
	var customSystemPrompt string
	if s.agent != nil {
		skillsText = s.agent.GetSkillsExtra()
		projectContext = s.agent.GetProjectContextExtra()
		thinkingLevel = s.agent.GetThinkingLevel()
		customSystemPrompt = s.agent.GetCustomSystemPrompt()
		if err := s.agent.Close(); err != nil {
			slog.Warn("[AgentNew] Failed to close previous agent", "error", err)
		}
	} else {
		// Fallback: re-derive from server fields (first-time initialization).
		skillsText = formatSkillsForPrompt(s.skills)
		if s.workspace != nil {
			projectContext = prompt.BuildProjectContext(s.workspace.GetCWD())
		}
		thinkingLevel = s.thinkingLevel
		customSystemPrompt = s.systemPrompt // Use server's stored systemPrompt
	}
	ag, err := agent.LoadSession(ctx, s.sessionDir, s.model, s.apiKey, s.eventEmitter)
	if err != nil {
		return err
	}
	// Re-inject extras into the new agent.
	ag.SetSystemPromptExtras(skillsText, projectContext)
	if customSystemPrompt != "" {
		ag.SetCustomSystemPrompt(customSystemPrompt)
	}
	if thinkingLevel != "" {
		if _, err := ag.SetThinkingLevel(thinkingLevel); err != nil {
			slog.Warn("[AgentNew] Failed to restore thinking level", "level", thinkingLevel, "error", err)
		}
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

	pendingCount := s.agent.PendingFollowUpCount()
	if s.pendingSteer && pendingCount == 0 {
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

func (s *AgentNewServer) applyModelSpecLocked(ctx context.Context, spec config.ModelSpec) (*rpc.ModelInfo, error) {
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

	if err := s.reloadAgentLocked(ctx); err != nil {
		return nil, err
	}
	// applySessionMessagesToSnapshotLocked removed - not needed for new architecture

	info := modelInfoFromSpec(spec)
	return &info, nil
}

func (s *AgentNewServer) SetModel(provider, modelID string) (*rpc.ModelInfo, error) {
	// Get context before acquiring lock to avoid RWMutex deadlock
	ctx := s.context()
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
	return s.applyModelSpecLocked(ctx, spec)
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
	// Get context before acquiring lock to avoid RWMutex deadlock
	ctx := s.context()
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

	if err := s.reloadAgentLocked(ctx); err != nil {
		return "", err
	}
	// applySessionMessagesToSnapshotLocked removed - not needed for new architecture
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
	// Get context before acquiring lock to avoid RWMutex deadlock
	ctx := s.context()
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

	if err := s.reloadAgentLocked(ctx); err != nil {
		return err
	}
	// applySessionMessagesToSnapshotLocked removed - not needed for new architecture

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
	s.mu.Lock()
	if s.isStreaming {
		s.mu.Unlock()
		return nil, fmt.Errorf("agent is busy")
	}
	s.isStreaming = true

	// Get context before unlocking to avoid RWMutex deadlock
	ctx := s.context()

	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.isStreaming = false
		s.mu.Unlock()
	}()

	if s.agent == nil {
		return nil, fmt.Errorf("agent is not available")
	}

	// Get snapshot before compaction
	snapshot := s.agent.GetSnapshot()
	if snapshot == nil {
		return nil, fmt.Errorf("snapshot is not available")
	}

	beforeCount := len(snapshot.RecentMessages)
	beforeTokens := snapshot.EstimateTokens()

	slog.Info("[RPC] Performing manual compaction",
		"message_count", beforeCount,
		"tokens", beforeTokens,
	)

	// Perform compaction using the new architecture
	// Note: This is done without holding s.mu, so other commands can check isStreaming
	if err := s.agent.Compact(ctx); err != nil {
		return nil, fmt.Errorf("compact failed: %w", err)
	}

	// Get snapshot after compaction
	snapshot = s.agent.GetSnapshot()
	afterCount := len(snapshot.RecentMessages)
	afterTokens := snapshot.EstimateTokens()

	slog.Info("[RPC] Compaction successful",
		"before_count", beforeCount,
		"after_count", afterCount,
		"before_tokens", beforeTokens,
		"after_tokens", afterTokens,
	)

	return &rpc.CompactResult{
		TokensBefore:   beforeTokens,
		TokensAfter:    afterTokens,
		MessagesBefore: beforeCount,
		MessagesAfter:  afterCount,
	}, nil
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

// GetTree returns the message tree for the /rewind command.
// It is invoked internally by /rewind when the user needs to select a branch point.
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

	// applySessionMessagesToSnapshotLocked removed - not needed for new architecture
	if s.sessionMgr != nil {
		if err := s.sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
	}
	return nil
}

func (s *AgentNewServer) Fork(entryID string) (*rpc.ForkResult, error) {
	// Get context before acquiring lock to avoid RWMutex deadlock
	ctx := s.context()
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

	if err := s.reloadAgentLocked(ctx); err != nil {
		return nil, err
	}
	// applySessionMessagesToSnapshotLocked removed - not needed for new architecture

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
	// Wire through to the agent's retry configuration
	if s.agent != nil {
		if enabled {
			s.agent.SetMaxLLMRetries(0) // use defaults (1 retry, 8 for rate limit)
		} else {
			s.agent.SetMaxLLMRetries(-1) // disable retry
		}
	}
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

// Close closes the agent and releases resources.
func (s *AgentNewServer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.agent != nil {
		return s.agent.Close()
	}
	return nil
}

// HandleCommand dispatches a command through the unified CommandRegistry.
// This is the AgentBackend extension point for all non-interface operations.
func (s *AgentNewServer) HandleCommand(ctx context.Context, cmd agent.Command) (any, error) {
	return s.commands.Handle(ctx, cmd)
}

// Commands returns the underlying CommandRegistry for direct registration.
func (s *AgentNewServer) Commands() *agent.CommandRegistry {
	return s.commands
}

// registerBuiltinCommands registers all built-in commands using the unified CommandRegistry.
func (s *AgentNewServer) registerBuiltinCommands() {
	cr := s.commands

	// --- Core conversation ---
	cr.Register("prompt", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Message string `json:"message"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			return nil, fmt.Errorf("empty prompt message")
		}
		return nil, s.Prompt(s.context(), message)
	}, agent.CommandMeta{Name: "prompt", Description: "Send a prompt", Source: "builtin"})

	cr.Register("steer", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Message string `json:"message"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			return nil, fmt.Errorf("empty steer message")
		}
		return nil, s.Steer(s.context(), message)
	}, agent.CommandMeta{Name: "steer", Description: "Steer the conversation", Source: "builtin"})

	cr.Register("follow_up", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Message string `json:"message"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			return nil, fmt.Errorf("empty follow-up message")
		}
		return nil, s.FollowUp(s.context(), message)
	}, agent.CommandMeta{Name: "follow_up", Description: "Send a follow-up message", Source: "builtin"})

	cr.Register("abort", func(ctx context.Context, cmd agent.Command) (any, error) {
		return nil, s.Abort()
	}, agent.CommandMeta{Name: "abort", Description: "Abort current operation", Source: "builtin"})

	// --- Session management ---
	cr.Register("new_session", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Name  string `json:"name"`
			Title string `json:"title"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		sessionID, err := s.NewSession(payload.Name, payload.Title)
		if err != nil {
			return nil, err
		}
		return map[string]any{"sessionId": sessionID, "cancelled": false}, nil
	}, agent.CommandMeta{Name: "new_session", Description: "Create a new session", Source: "builtin"})

	cr.Register("clear_session", func(ctx context.Context, cmd agent.Command) (any, error) {
		return nil, s.ClearSession()
	}, agent.CommandMeta{Name: "clear_session", Description: "Clear current session", Source: "builtin"})

	cr.Register("list_sessions", func(ctx context.Context, cmd agent.Command) (any, error) {
		sessions, err := s.ListSessions()
		if err != nil {
			return nil, err
		}
		return map[string]any{"sessions": sessions}, nil
	}, agent.CommandMeta{Name: "list_sessions", Description: "List all sessions", Source: "builtin"})

	cr.Register("switch_session", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			ID string `json:"id"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.SwitchSession(payload.ID)
	}, agent.CommandMeta{Name: "switch_session", Description: "Switch to a session", Source: "builtin"})

	cr.Register("delete_session", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			ID string `json:"id"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.DeleteSession(payload.ID)
	}, agent.CommandMeta{Name: "delete_session", Description: "Delete a session", Source: "builtin"})

	// --- State & queries ---
	cr.Register("get_state", func(ctx context.Context, cmd agent.Command) (any, error) {
		return s.GetState()
	}, agent.CommandMeta{Name: "get_state", Description: "Get session state", Source: "builtin"})

	cr.Register("get_messages", func(ctx context.Context, cmd agent.Command) (any, error) {
		messages := s.GetMessages()
		return map[string]any{"messages": messages}, nil
	}, agent.CommandMeta{Name: "get_messages", Description: "Get session messages", Source: "builtin"})

	cr.Register("get_session_stats", func(ctx context.Context, cmd agent.Command) (any, error) {
		return s.GetSessionStats()
	}, agent.CommandMeta{Name: "get_session_stats", Description: "Get session statistics", Source: "builtin"})

	cr.Register("get_commands", func(ctx context.Context, cmd agent.Command) (any, error) {
		commands, err := s.GetCommands()
		if err != nil {
			return nil, err
		}
		return map[string]any{"commands": commands}, nil
	}, agent.CommandMeta{Name: "get_commands", Description: "List available commands", Source: "builtin"})

	// --- Model management ---
	cr.Register("get_available_models", func(ctx context.Context, cmd agent.Command) (any, error) {
		models, err := s.GetAvailableModels()
		if err != nil {
			return nil, err
		}
		return map[string]any{"models": models}, nil
	}, agent.CommandMeta{Name: "get_available_models", Description: "List available models", Source: "builtin"})

	cr.Register("set_model", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return s.SetModel(payload.Provider, payload.ModelID)
	}, agent.CommandMeta{Name: "set_model", Description: "Set the active model", Source: "builtin"})

	// --- Compaction ---
	cr.Register("compact", func(ctx context.Context, cmd agent.Command) (any, error) {
		return s.Compact()
	}, agent.CommandMeta{Name: "compact", Description: "Trigger compaction", Source: "builtin"})

	cr.Register("set_auto_compaction", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Enabled bool `json:"enabled"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.SetAutoCompaction(payload.Enabled)
	}, agent.CommandMeta{Name: "set_auto_compaction", Description: "Toggle auto compaction", Source: "builtin"})

	// --- Thinking ---
	cr.Register("set_thinking_level", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Level string `json:"level"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return s.SetThinkingLevel(payload.Level)
	}, agent.CommandMeta{Name: "set_thinking_level", Description: "Set thinking level", Source: "builtin"})

	// --- Tool configuration ---
	cr.Register("set_tool_call_cutoff", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Cutoff int `json:"cutoff"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.SetToolCallCutoff(payload.Cutoff)
	}, agent.CommandMeta{Name: "set_tool_call_cutoff", Description: "Set tool call cutoff", Source: "builtin"})

	cr.Register("set_tool_summary_strategy", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Strategy string `json:"strategy"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.SetToolSummaryStrategy(payload.Strategy)
	}, agent.CommandMeta{Name: "set_tool_summary_strategy", Description: "Set tool summary strategy", Source: "builtin"})

	cr.Register("set_tool_summary_automation", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Mode string `json:"mode"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.SetToolSummaryAutomation(payload.Mode)
	}, agent.CommandMeta{Name: "set_tool_summary_automation", Description: "Set tool summary automation", Source: "builtin"})

	// --- Steering / follow-up modes ---
	cr.Register("set_steering_mode", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Mode string `json:"mode"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.SetSteeringMode(payload.Mode)
	}, agent.CommandMeta{Name: "set_steering_mode", Description: "Set steering mode", Source: "builtin"})

	cr.Register("set_follow_up_mode", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Mode string `json:"mode"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.SetFollowUpMode(payload.Mode)
	}, agent.CommandMeta{Name: "set_follow_up_mode", Description: "Set follow-up mode", Source: "builtin"})

	// --- Fork / branch ---
	cr.Register("get_last_assistant_text", func(ctx context.Context, cmd agent.Command) (any, error) {
		return s.GetLastAssistantText()
	}, agent.CommandMeta{Name: "get_last_assistant_text", Description: "Get last assistant text", Source: "builtin"})

	cr.Register("get_fork_messages", func(ctx context.Context, cmd agent.Command) (any, error) {
		messages, err := s.GetForkMessages()
		if err != nil {
			return nil, err
		}
		return map[string]any{"messages": messages}, nil
	}, agent.CommandMeta{Name: "get_fork_messages", Description: "Get fork messages", Source: "builtin"})

	cr.Register("fork", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			EntryID string `json:"entryId"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return s.Fork(payload.EntryID)
	}, agent.CommandMeta{Name: "fork", Description: "Fork at a message", Source: "builtin"})

	cr.Register("get_tree", func(ctx context.Context, cmd agent.Command) (any, error) {
		entries, err := s.GetTree()
		if err != nil {
			return nil, err
		}
		return map[string]any{"entries": entries}, nil
	}, agent.CommandMeta{Name: "get_tree", Description: "Get session tree", Source: "builtin"})

	cr.Register("resume_on_branch", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			EntryID string `json:"entryId"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.ResumeOnBranch(payload.EntryID)
	}, agent.CommandMeta{Name: "resume_on_branch", Description: "Resume on a branch", Source: "builtin"})

	// --- Session settings ---
	cr.Register("set_session_name", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Name string `json:"name"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.SetSessionName(payload.Name)
	}, agent.CommandMeta{Name: "set_session_name", Description: "Set session name", Source: "builtin"})

	// --- Auto retry ---
	cr.Register("set_auto_retry", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Enabled bool `json:"enabled"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return nil, s.SetAutoRetry(payload.Enabled)
	}, agent.CommandMeta{Name: "set_auto_retry", Description: "Toggle auto retry", Source: "builtin"})

	cr.Register("abort_retry", func(ctx context.Context, cmd agent.Command) (any, error) {
		return nil, s.AbortRetry()
	}, agent.CommandMeta{Name: "abort_retry", Description: "Abort retry", Source: "builtin"})

	// --- Bash ---
	cr.Register("bash", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Command string `json:"command"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return s.Bash(payload.Command)
	}, agent.CommandMeta{Name: "bash", Description: "Execute a bash command", Source: "builtin"})

	cr.Register("abort_bash", func(ctx context.Context, cmd agent.Command) (any, error) {
		return nil, s.AbortBash()
	}, agent.CommandMeta{Name: "abort_bash", Description: "Abort bash execution", Source: "builtin"})

	// --- Export ---
	cr.Register("export_html", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Path string `json:"path"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return s.ExportHTML(payload.Path)
	}, agent.CommandMeta{Name: "export_html", Description: "Export session as HTML", Source: "builtin"})

	// --- Trace events ---
	cr.Register("set_trace_events", func(ctx context.Context, cmd agent.Command) (any, error) {
		var payload struct {
			Events []string `json:"events"`
		}
		if len(cmd.Payload) > 0 {
			json.Unmarshal(cmd.Payload, &payload)
		}
		return s.SetTraceEvents(payload.Events)
	}, agent.CommandMeta{Name: "set_trace_events", Description: "Set trace events", Source: "builtin"})

	cr.Register("get_trace_events", func(ctx context.Context, cmd agent.Command) (any, error) {
		return s.GetTraceEvents()
	}, agent.CommandMeta{Name: "get_trace_events", Description: "Get trace events", Source: "builtin"})

	// --- Workflow ---
	cr.Register("get_workflow_status", func(ctx context.Context, cmd agent.Command) (any, error) {
		return s.GetState() // TODO: implement workflow status
	}, agent.CommandMeta{Name: "get_workflow_status", Description: "Get workflow status", Source: "builtin"})
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

// SetupAgentNewHandlers shares the AgentNewServer's command registry with the RPC server.
// All commands registered on AgentNewServer (via registerBuiltinCommands or Commands().Register)
// are automatically available to the RPC dispatch.
func SetupAgentNewHandlers(server *rpc.Server, agentNewServer *AgentNewServer) {
	server.SetRegistry(agentNewServer.Commands())
	slog.Info("[AgentNew] RPC handlers configured", "count", len(agentNewServer.Commands().ListNames()))
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
		"",    // systemPrompt: not available in this code path
		"",    // cmPrompt: not available in this code path
		nil,   // toolList: not available in this code path
		false, // keepTools: not available in this code path
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

// formatSkillsForPrompt formats []*skill.Skill for inclusion in the system prompt.
func formatSkillsForPrompt(skills []*skill.Skill) string {
	concrete := make([]skill.Skill, 0, len(skills))
	for _, s := range skills {
		if s != nil {
			concrete = append(concrete, *s)
		}
	}
	return skill.FormatForPrompt(concrete)
}
