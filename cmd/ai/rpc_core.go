package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
)

// rpcCoreConfig holds all parameters needed to construct an RPCCore.
type rpcCoreConfig struct {
	SessionPath        string
	DebugAddr          string
	Input              io.Reader
	Output             io.Writer
	CustomSystemPrompt string
	MaxTurns           int
	Timeout            time.Duration
}

// RPCCore holds all shared state for the RPC server lifecycle.
// Fields are exported so handler closures (in rpc_handlers.go) can access them.
type RPCCore struct {
	// Config and paths (set once at construction)
	ConfigPath string
	Cfg        *config.Config

	// Model and API
	Model                llm.Model
	APIKey               string
	CurrentContextWindow int
	CurrentModelInfo     rpc.ModelInfo

	// Session management
	SessionMgr  *session.SessionManager
	Sess        *session.Session
	SessionID   string
	SessionName string
	SessionsDir string

	// Tools and workspace
	Ws       *tools.Workspace
	Registry *tools.Registry

	// Compaction
	CompactorConfig *compact.Config
	Compactor       *compact.Compactor
	CompactionCtrl  *agent.CompactionController

	// Skills
	SkillResult *skill.LoadResult

	// Agent
	Ag            *agent.Agent
	CheckpointMgr *agent.AgentContextCheckpointManager
	SessionWriter *sessionWriter
	SessionComp   *sessionCompactor
	SystemPrompt  string

	// RPC server
	Server      *rpc.Server
	BashRunner  *bashRunner
	BashTimeout time.Duration

	// Shared mutable state (protected by StateMu)
	StateMu               sync.Mutex
	IsStreaming           bool
	IsCompacting          bool
	CurrentThinkingLevel  string
	AutoCompactionEnabled bool
	SteeringMode          string
	FollowUpMode          string
	PendingSteer          bool
	FollowUpQueue         []string
	ShowThinking          bool
	ShowTools             bool
	ShowPrefix            bool
	BusyMode              string

	// Trace
	TraceOutputPath string

	// I/O (used by Run)
	input              io.Reader
	output             io.Writer
	debugAddr          string
	customSystemPrompt string
}

// NewRPCCore constructs an RPCCore by performing all initialization up to
// (but not including) handler registration and the event loop.
func NewRPCCore(cfg rpcCoreConfig) (*RPCCore, error) {
	c := &RPCCore{
		input:              cfg.Input,
		output:             cfg.Output,
		debugAddr:          cfg.DebugAddr,
		customSystemPrompt: cfg.CustomSystemPrompt,
	}

	// Load configuration
	configPath, err := config.GetDefaultConfigPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get config path: %w", err)
	}
	c.ConfigPath = configPath

	appCfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Warn("Failed to load config", "path", configPath, "error", err)
		appCfg, _ = config.LoadConfig(configPath)
	}
	c.Cfg = appCfg

	// Initialize logger from config
	log, err := appCfg.Log.CreateLogger()
	if err != nil {
		return nil, fmt.Errorf("failed to create logger: %w", err)
	}
	slog.SetDefault(log)

	// Convert config to llm.Model
	model := appCfg.GetLLMModel()
	var _ llm.Model = model

	apiKey, err := config.ResolveAPIKey(model.Provider)
	if err != nil {
		return nil, fmt.Errorf("missing API key: %w", err)
	}

	slog.Info("Model", "id", model.ID, "provider", model.Provider, "baseURL", model.BaseURL)
	if appCfg.Compactor != nil {
		slog.Info("Compactor", "maxMessages", appCfg.Compactor.MaxMessages, "maxTokens", appCfg.Compactor.MaxTokens,
			"keepRecent", appCfg.Compactor.KeepRecent, "keepRecentTokens", appCfg.Compactor.KeepRecentTokens,
			"reserveTokens", appCfg.Compactor.ReserveTokens,
			"toolCallCutoff", appCfg.Compactor.ToolCallCutoff,
			"toolSummaryStrategy", appCfg.Compactor.ToolSummaryStrategy,
			"toolSummaryAutomation", appCfg.Compactor.ToolSummaryAutomation)
	}

	activeSpec, err := resolveActiveModelSpec(appCfg)
	if err != nil {
		slog.Info("Model spec fallback", "error", err)
	}
	model = applyModelLimitsFromSpec(model, activeSpec)
	currentModelInfo := modelInfoFromSpec(activeSpec)
	currentModelInfo.MaxTokens = model.MaxTokens
	currentModelInfo.ContextWindow = model.ContextWindow
	currentContextWindow := activeSpec.ContextWindow

	c.Model = model
	c.APIKey = apiKey
	c.CurrentContextWindow = currentContextWindow
	c.CurrentModelInfo = currentModelInfo

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}
	appCfg.Workspace = cwd

	sessionPath, err := normalizeSessionPath(cfg.SessionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize session path: %w", err)
	}

	// Initialize session manager
	sessionsDir, err := session.GetDefaultSessionsDir(cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions path: %w", err)
	}
	if sessionPath != "" {
		sessionsDir = filepath.Dir(sessionPath)
	}
	c.SessionsDir = sessionsDir
	c.SessionMgr = session.NewSessionManager(sessionsDir)

	// Load current session
	var sess *session.Session
	sessionID := ""
	sessionName := ""
	if sessionPath != "" {
		opts := session.DefaultLoadOptions()
		sess, err = session.LoadSessionLazy(sessionPath, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to load session from %s: %w", sessionPath, err)
		}
		sessionID = sess.GetID()
		sessionName = resolveSessionName(c.SessionMgr, sessionID)
		_ = c.SessionMgr.SetCurrent(sessionID)
		if err := c.SessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		slog.Info("Loaded session", "path", sessionPath, "count", len(sess.GetMessages()))
	} else {
		sess, sessionID, err = c.SessionMgr.LoadCurrent()
		if err != nil {
			name := time.Now().Format("20060102-150405")
			sess, err = c.SessionMgr.CreateSession(name, name)
			if err != nil {
				return nil, fmt.Errorf("failed to create new session: %w", err)
			}
			sessionID = sess.GetID()
			sessionName = name
			if err := c.SessionMgr.SetCurrent(sessionID); err != nil {
				slog.Info("Failed to set current session:", "value", err)
			}
			if err := c.SessionMgr.SaveCurrent(); err != nil {
				slog.Info("Failed to update session metadata:", "value", err)
			}
			slog.Info("Created new session", "id", sessionID, "count", len(sess.GetMessages()))
		} else {
			sessionName = resolveSessionName(c.SessionMgr, sessionID)
			slog.Info("Restored previous session", "id", sessionID, "name", sessionName, "count", len(sess.GetMessages()))
		}
	}
	c.Sess = sess
	c.SessionID = sessionID
	c.SessionName = sessionName

	// Create shared workspace
	ws, err := tools.NewWorkspace(cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	c.Ws = ws

	// Create tool registry
	registry := tools.NewRegistry()
	readTool := tools.NewReadTool(ws)
	editTool := tools.NewEditTool(ws)
	if appCfg.ToolOutput != nil && appCfg.ToolOutput.HashLines {
		readTool.SetHashLines(true)
	}
	if appCfg.Edit != nil && appCfg.Edit.Mode == "hashline" {
		editTool.SetEditMode(tools.EditModeHashline)
	}
	registry.Register(readTool)
	registry.Register(tools.NewBashTool(ws))
	registry.Register(tools.NewWriteTool(ws))
	registry.Register(tools.NewGrepTool(ws))
	registry.Register(editTool)
	registry.Register(tools.NewChangeWorkspaceTool(ws))
	c.Registry = registry

	// Create compactors
	compactorConfig := appCfg.Compactor
	if compactorConfig == nil {
		compactorConfig = compact.DefaultConfig()
	}
	c.CompactorConfig = compactorConfig

	compactor := compact.NewCompactor(
		compactorConfig,
		model,
		apiKey,
		prompt.CompactorBasePrompt(),
		currentContextWindow,
	)
	c.Compactor = compactor

	ctxManager := compact.NewContextManager(
		compact.DefaultContextManagerConfig(),
		model,
		apiKey,
		currentContextWindow,
		prompt.ContextManagementSystemPrompt(),
		compactor,
	)

	slog.Info("Registered tools: read, bash, write, grep, edit", "count", len(registry.All()))

	// Load skills
	_, traceOutputPath, err := initTraceFileHandler(sessionID)
	if err != nil {
		slog.Warn("Failed to create trace handler", "outputDir", traceOutputPath, "error", err)
	} else {
		slog.Info("Trace handler initialized", "outputDir", traceOutputPath)
	}
	c.TraceOutputPath = traceOutputPath

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	agentDir := filepath.Join(homeDir, ".ai")

	skillLoader := skill.NewLoader(agentDir)
	skillResult := skillLoader.Load(&skill.LoadOptions{
		CWD:             cwd,
		AgentDir:        agentDir,
		SkillPaths:      nil,
		IncludeDefaults: true,
	})

	if len(skillResult.Diagnostics) > 0 {
		slog.Info("Skill loading:  diagnostics", "count", len(skillResult.Diagnostics))
		for _, diag := range skillResult.Diagnostics {
			if diag.Type == "error" {
				slog.Error("Skill error", "type", diag.Type, "path", diag.Path, "message", diag.Message)
			} else {
				slog.Warn("Skill warning", "type", diag.Type, "path", diag.Path, "message", diag.Message)
			}
		}
	}
	slog.Info("Loaded  skills", "count", len(skillResult.Skills))
	for _, s := range skillResult.Skills {
		slog.Info("Skill", "name", s.Name, "description", s.Description)
	}
	c.SkillResult = skillResult

	// Build system prompt
	c.SystemPrompt = c.buildSystemPrompt()

	// Create initial agent context
	agentCtx := c.CreateBaseContext()

	// Pre-config: sessionWriter, sessionComp, executor, toolOutputConfig
	sessionWriter := newSessionWriter(256)
	c.SessionWriter = sessionWriter

	sessionComp := &sessionCompactor{
		session:   sess,
		compactor: compactor,
		writer:    sessionWriter,
	}
	c.SessionComp = sessionComp

	concurrencyConfig := appCfg.Concurrency
	if concurrencyConfig == nil {
		concurrencyConfig = config.DefaultConcurrencyConfig()
	}
	executor := agent.NewToolExecutor(
		concurrencyConfig.MaxConcurrentTools,
		concurrencyConfig.QueueTimeout,
	)

	toolOutputConfig := appCfg.ToolOutput
	if toolOutputConfig == nil {
		toolOutputConfig = config.DefaultToolOutputConfig()
	}

	// Build LoopConfig
	loopCfg := appCfg.ToLoopConfig(
		config.WithCompactors([]agent.Compactor{ctxManager, sessionComp}),
		config.WithContextWindow(currentContextWindow),
		config.WithToolCallCutoff(compactorConfig.ToolCallCutoff),
		config.WithExecutor(executor),
		config.WithToolOutputLimits(agent.ToolOutputLimits{
			MaxChars: toolOutputConfig.MaxChars,
		}),
	)
	loopCfg.Model = model
	loopCfg.APIKey = apiKey
	loopCfg.GetWorkingDir = ws.GetCWD
	loopCfg.GetStartupPath = ws.GetInitialCWD
	loopCfg.GetSessionDir = func() string {
		if c.Sess != nil {
			return c.Sess.GetDir()
		}
		return ""
	}
	if cfg.MaxTurns > 0 {
		loopCfg.MaxTurns = cfg.MaxTurns
		slog.Info("Max turns limit set", "max_turns", cfg.MaxTurns)
	}

	// Create agent
	ag := agent.NewAgentFromConfigWithContext(model, apiKey, agentCtx, loopCfg)
	c.Ag = ag

	// Start timeout watchdog if timeout is set
	if cfg.Timeout > 0 {
		go func() {
			<-time.After(cfg.Timeout)
			slog.Warn("[RPC] Timeout reached, aborting agent", "timeout", cfg.Timeout)
			ag.Abort()
		}()
	}

	// Initialize checkpoint manager
	if sess != nil {
		sessionDir := sess.GetDir()
		if mgr, err := agent.NewAgentContextCheckpointManager(sessionDir); err != nil {
			slog.Warn("Failed to create checkpoint manager", "error", err)
		} else {
			c.CheckpointMgr = mgr
		}
	}

	slog.Info("Auto-compact enabled", "maxMessages", compactorConfig.MaxMessages, "maxTokens", compactorConfig.MaxTokens)
	slog.Info("Concurrency control enabled", "maxConcurrentTools", concurrencyConfig.MaxConcurrentTools, "toolTimeout", concurrencyConfig.ToolTimeout)
	slog.Info("Tool output truncation", "maxChars", toolOutputConfig.MaxChars)

	// Bash runner
	c.BashRunner = newBashRunner()
	bashTimeout := time.Duration(concurrencyConfig.ToolTimeout) * time.Second
	if bashTimeout <= 0 {
		bashTimeout = 30 * time.Second
	}
	c.BashTimeout = bashTimeout

	// Create RPC server
	server := rpc.NewServer()
	server.SetOutput(c.output)
	c.Server = server

	// Initialize shared mutable state
	c.CurrentThinkingLevel = "high"
	c.AutoCompactionEnabled = compactorConfig.AutoCompact
	c.SteeringMode = "all"
	c.FollowUpMode = "one-at-a-time"
	c.ShowThinking = true
	c.ShowTools = true
	c.ShowPrefix = true
	c.BusyMode = "steer"
	ag.SetThinkingLevel(c.CurrentThinkingLevel)

	// Wire CompactionController
	c.CompactionCtrl = agent.NewCompactionController(agent.CompactionDeps{
		Compactor: compactor,
		Agent:     ag,
		EmitEvent: func(ev agent.AgentEvent) { server.EmitEvent(ev) },
		SetState: func(compacting bool) {
			c.StateMu.Lock()
			c.IsCompacting = compacting
			c.StateMu.Unlock()
		},
	})

	return c, nil
}

// buildSystemPrompt builds the full system prompt used for agent and compactor.
func (c *RPCCore) buildSystemPrompt() string {
	// Use custom system prompt if provided (e.g., via --system-prompt flag)
	if c.customSystemPrompt != "" {
		slog.Info("Using custom system prompt", "length", len(c.customSystemPrompt))
		return c.customSystemPrompt
	}
	// Use workspace to get dynamic cwd for each prompt build
	promptBuilder := prompt.NewBuilderWithWorkspace("", c.Ws)
	promptBuilder.SetTools(c.Registry.All()).SetSkills(c.SkillResult.Skills)

	if c.Sess != nil {
		sessionDir := c.Sess.GetDir()
		if sessionDir != "" {
			wm := agentctx.NewLLMContext(sessionDir)
			_ = wm
		}
	}

	return promptBuilder.Build()
}

// CreateBaseContext creates a fresh AgentContext for the current session,
// restoring conversation history and checkpoint state.
func (c *RPCCore) CreateBaseContext() *agentctx.AgentContext {
	// Rebuild system prompt from the current session so llm-context paths
	// stay in sync after /resume, /new, /fork, and branch resume operations.
	c.SystemPrompt = c.buildSystemPrompt()
	ctx := agentctx.NewAgentContext(c.SystemPrompt)
	for _, tool := range c.Registry.All() {
		ctx.AddTool(tool)
	}
	// Initialize llm context and set it on session for compaction summaries
	if c.Sess != nil {
		sessionDir := c.Sess.GetDir()
		if sessionDir != "" {
			ctx.LLMContext = ""
		}
		// Restore conversation history from session
		ctx.RecentMessages = c.Sess.GetMessages()
		// Restore agent state and messages from checkpoint
		if sessionDir != "" {
			if cpInfo, err := agentctx.LoadLatestCheckpoint(sessionDir); err == nil && cpInfo != nil {
				cpPath := filepath.Join(sessionDir, cpInfo.Path)
				if snapshot, err := agentctx.LoadCheckpoint(sessionDir, cpInfo); err == nil && snapshot != nil {
					if len(snapshot.RecentMessages) > 0 {
						sessionMsgCount := len(ctx.RecentMessages)
						ctx.RecentMessages = snapshot.RecentMessages
						slog.Info("Restored messages from checkpoint",
							"checkpoint_messages", len(snapshot.RecentMessages),
							"session_messages", sessionMsgCount,
							"saved", sessionMsgCount-len(snapshot.RecentMessages),
						)
					}
					if snapshot.AgentState != nil {
						ctx.AgentState = snapshot.AgentState
						if snapshot.AgentState.CurrentWorkingDir != "" {
							if err := c.Ws.SetCWD(snapshot.AgentState.CurrentWorkingDir); err != nil {
								slog.Warn("Failed to restore CWD from checkpoint", "cwd", snapshot.AgentState.CurrentWorkingDir, "error", err)
							}
						}
						slog.Info("Restored agent state from checkpoint",
							"turns", snapshot.AgentState.TotalTurns,
							"tokens", snapshot.AgentState.TokensUsed,
							"toolCallsSince", snapshot.AgentState.ToolCallsSinceLastTrigger,
							"cwd", snapshot.AgentState.CurrentWorkingDir,
						)
					}
					if snapshot.LLMContext != "" {
						ctx.LLMContext = snapshot.LLMContext
					}
				} else {
					if savedState, err := agentctx.LoadCheckpointAgentState(cpPath); err == nil {
						ctx.AgentState = savedState
						if savedState.CurrentWorkingDir != "" {
							if err := c.Ws.SetCWD(savedState.CurrentWorkingDir); err != nil {
								slog.Warn("Failed to restore CWD from checkpoint", "cwd", savedState.CurrentWorkingDir, "error", err)
							}
						}
						slog.Info("Restored agent state from checkpoint (no messages)",
							"turns", savedState.TotalTurns,
							"tokens", savedState.TokensUsed,
							"toolCallsSince", savedState.ToolCallsSinceLastTrigger,
							"cwd", savedState.CurrentWorkingDir,
						)
					}
				}
			}
		}
		// Set up persistence callback for compact operations
		ctx.OnCompactEvent = func(detail *agentctx.CompactEventDetail) error {
			return c.Sess.AppendCompactEvent(detail)
		}
	}
	return ctx
}

// UpdateCheckpointManager creates a new checkpoint manager for the current session.
func (c *RPCCore) UpdateCheckpointManager() error {
	c.StateMu.Lock()
	defer c.StateMu.Unlock()

	// Close old checkpoint manager if exists
	if c.CheckpointMgr != nil {
		if err := c.CheckpointMgr.Close(); err != nil {
			slog.Warn("Failed to close old checkpoint manager", "error", err)
		}
	}

	// Create new checkpoint manager for current session
	if c.Sess != nil {
		sessionDir := c.Sess.GetDir()
		if sessionDir != "" {
			mgr, err := agent.NewAgentContextCheckpointManager(sessionDir)
			if err != nil {
				slog.Warn("Failed to create checkpoint manager", "error", err)
				c.CheckpointMgr = nil
			} else {
				c.CheckpointMgr = mgr
				slog.Info("Updated checkpoint manager", "sessionDir", sessionDir)
			}
		} else {
			slog.Warn("Session directory is empty, checkpoint manager not updated")
			c.CheckpointMgr = nil
		}
	}

	return nil
}

// SetAgentContext is a convenience wrapper around Ag.SetContext.
func (c *RPCCore) SetAgentContext(ctx *agentctx.AgentContext) {
	c.Ag.SetContext(ctx)
}

// ExpandSkillCommands expands /skill:name commands using loaded skills.
func (c *RPCCore) ExpandSkillCommands(text string) string {
	return skill.ExpandCommand(text, c.SkillResult.Skills)
}

// Run starts the RPC server event loop and blocks until the server exits.
// It should be called after all handlers are registered.
func (c *RPCCore) Run() error {
	// Close session writer on exit
	defer c.SessionWriter.Close()

	// Close checkpoint manager on exit
	defer func() {
		if c.CheckpointMgr != nil {
			c.CheckpointMgr.Close()
		}
	}()

	// Shutdown agent on exit
	defer c.Ag.Shutdown()

	// Start event emitter goroutine
	eventEmitterDone := make(chan struct{})
	shutdownEmitter := make(chan struct{})
	go func() {
		defer close(eventEmitterDone)
		for {
			select {
			case event := <-c.Ag.Events():
				c.handleAgentEvent(event)
				if event.Type == "agent_end" {
					go func() {
						if err := c.SessionMgr.SaveCurrent(); err != nil {
							slog.Info("Failed to update session metadata:", "value", err)
						}
					}()
				}
			case <-shutdownEmitter:
				// Drain remaining events
				for {
					select {
					case event := <-c.Ag.Events():
						c.handleAgentEvent(event)
						if event.Type == "agent_end" {
							go func() {
								if err := c.SessionMgr.SaveCurrent(); err != nil {
									slog.Info("Failed to update session metadata:", "value", err)
								}
							}()
						}
					default:
						return
					}
				}
			}
		}
	}()

	// Emit start event
	allTools := c.Registry.All()
	toolNames := make([]string, len(allTools))
	for i, t := range allTools {
		toolNames[i] = t.Name()
	}
	c.Server.EmitEvent(map[string]any{
		"type":  "server_start",
		"model": c.Model.ID,
		"tools": toolNames,
	})

	// Start debug server if enabled
	if c.debugAddr != "" {
		go func() {
			http.HandleFunc("/debug/metrics", func(w http.ResponseWriter, r *http.Request) {
				metrics := c.Ag.GetMetrics()
				if metrics == nil {
					http.Error(w, "Metrics not available", http.StatusServiceUnavailable)
					return
				}
				fullMetrics := metrics.GetFullMetrics()
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(fullMetrics); err != nil {
					slog.Error("Failed to encode metrics:", "value", err)
					http.Error(w, "Failed to encode metrics", http.StatusInternalServerError)
				}
			})

			slog.Info("Debug server listening on", "value", c.debugAddr)
			slog.Info("Debug endpoints available at:")
			slog.Info("- http:///debug/pprof/          (profiling index)", "value", c.debugAddr)
			slog.Info("- http:///debug/pprof/profile   (CPU profile)", "value", c.debugAddr)
			slog.Info("- http:///debug/pprof/heap       (memory profile)", "value", c.debugAddr)
			slog.Info("- http:///debug/pprof/goroutine  (goroutine dump)", "value", c.debugAddr)
			slog.Info("- http:///debug/pprof/trace      (execution trace)", "value", c.debugAddr)
			slog.Info("- http:///debug/metrics         (agent metrics)", "value", c.debugAddr)

			if err := http.ListenAndServe(c.debugAddr, nil); err != nil {
				slog.Error("Debug server error:", "error", err)
			}
		}()
	}

	// Run RPC server
	cwd := c.Ws.GetCWD()
	slog.Info("RPC server started", "model", c.Model.ID, "cwd", cwd)
	slog.Info("Waiting for commands...")
	runErr := c.Server.RunWithIO(c.input, c.output)

	// Server stopped, event emitter will exit automatically
	slog.Info("RPC server stopped, waiting for cleanup...")

	// Wait for agent to complete
	slog.Info("Waiting for agent to complete...")
	c.Ag.Wait()

	close(shutdownEmitter)
	<-eventEmitterDone

	slog.Info("Agent completed, exiting...")
	return runErr
}

// handleAgentEvent processes a single agent event, updating state and forwarding to the server.
func (c *RPCCore) handleAgentEvent(event agent.AgentEvent) {
	if event.Type == "agent_start" {
		c.StateMu.Lock()
		c.IsStreaming = true
		c.StateMu.Unlock()
	}
	if event.Type == "agent_end" {
		c.StateMu.Lock()
		c.IsStreaming = false
		c.IsCompacting = false
		c.PendingSteer = false
		c.StateMu.Unlock()
	}
	if event.Type == "compaction_start" {
		c.StateMu.Lock()
		c.IsCompacting = true
		c.StateMu.Unlock()
	}
	if event.Type == "compaction_end" {
		c.StateMu.Lock()
		c.IsCompacting = false
		c.StateMu.Unlock()
	}

	if event.Type == "message_end" && event.Message != nil {
		if c.SessionWriter != nil {
			c.SessionWriter.Append(c.Sess, *event.Message)
		}
	}
	if event.Type == "tool_execution_end" && event.Result != nil {
		if c.SessionWriter != nil {
			c.SessionWriter.Append(c.Sess, *event.Result)
		}
	}

	emitAt := time.Now()
	if event.EventAt == 0 {
		event.EventAt = emitAt.UnixNano()
	}
	c.Server.EmitEvent(event)
}
