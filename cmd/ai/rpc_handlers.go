package main

import (
	"context"
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"path/filepath"
		"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

func runRPC(sessionPath string, debugAddr string, input io.Reader, output io.Writer, customSystemPrompt string, maxTurns int, timeout time.Duration) error {
	// Load configuration
	configPath, err := config.GetDefaultConfigPath()
	if err != nil {
		return fmt.Errorf("failed to get config path: %w", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Warn("Failed to load config", "path", configPath, "error", err)
		// Use defaults - LoadConfig already provides defaults
		cfg, _ = config.LoadConfig(configPath)
	}
	// Initialize logger from config
	log, err := cfg.Log.CreateLogger()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Set the default slog logger
	slog.SetDefault(log)
	traceOutputPath := ""

	// Convert config to llm.Model
	model := cfg.GetLLMModel()

	// Verify model type (ensures llm package is used)
	var _ llm.Model = model

	apiKey, err := config.ResolveAPIKey(model.Provider)
	if err != nil {
		return fmt.Errorf("missing API key: %w", err)
	}

	// Log model info
	slog.Info("Model", "id", model.ID, "provider", model.Provider, "baseURL", model.BaseURL)
	if cfg.Compactor != nil {
		slog.Info("Compactor", "maxMessages", cfg.Compactor.MaxMessages, "maxTokens", cfg.Compactor.MaxTokens,
			"keepRecent", cfg.Compactor.KeepRecent, "keepRecentTokens", cfg.Compactor.KeepRecentTokens,
			"reserveTokens", cfg.Compactor.ReserveTokens,
			"toolCallCutoff", cfg.Compactor.ToolCallCutoff,
			"toolSummaryStrategy", cfg.Compactor.ToolSummaryStrategy,
			"toolSummaryAutomation", cfg.Compactor.ToolSummaryAutomation)
	}

	activeSpec, err := resolveActiveModelSpec(cfg)
	if err != nil {
		slog.Info("Model spec fallback", "error", err)
	}
	model = applyModelLimitsFromSpec(model, activeSpec)
	currentModelInfo := modelInfoFromSpec(activeSpec)
	currentModelInfo.MaxTokens = model.MaxTokens
	currentModelInfo.ContextWindow = model.ContextWindow
	currentContextWindow := activeSpec.ContextWindow

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	// Set workspace (startup path) in config
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
	sessionName := ""
	if sessionPath != "" {
		// Create load options with llm context (will be set later in createBaseContext)
		opts := session.DefaultLoadOptions()
		sess, err = session.LoadSessionLazy(sessionPath, opts)
		if err != nil {
			return fmt.Errorf("failed to load session from %s: %w", sessionPath, err)
		}
		sessionID = sess.GetID()
		sessionName = resolveSessionName(sessionMgr, sessionID)
		_ = sessionMgr.SetCurrent(sessionID)
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		slog.Info("Loaded session", "path", sessionPath, "count", len(sess.GetMessages()))
	} else {
		// If no session path specified, try to restore the current session
		sess, sessionID, err = sessionMgr.LoadCurrent()
		if err != nil {
			// No current session or failed to load, create a new one
			name := time.Now().Format("20060102-150405")
			sess, err = sessionMgr.CreateSession(name, name)
			if err != nil {
				return fmt.Errorf("failed to create new session: %w", err)
			}
			sessionID = sess.GetID()
			sessionName = name
			if err := sessionMgr.SetCurrent(sessionID); err != nil {
				slog.Info("Failed to set current session:", "value", err)
			}
			if err := sessionMgr.SaveCurrent(); err != nil {
				slog.Info("Failed to update session metadata:", "value", err)
			}
			slog.Info("Created new session", "id", sessionID, "count", len(sess.GetMessages()))
		} else {
			// Successfully restored previous session
			sessionName = resolveSessionName(sessionMgr, sessionID)
			slog.Info("Restored previous session", "id", sessionID, "name", sessionName, "count", len(sess.GetMessages()))
		}
	}

	// Create tool registry and register tools
	// Create a shared workspace object for all tools to track directory changes
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

	// Create compactors for automatic context compression
	compactorConfig := cfg.Compactor
	if compactorConfig == nil {
		compactorConfig = compact.DefaultConfig()
	}
	compactor := compact.NewCompactor(
		compactorConfig,
		model,
		apiKey,
		prompt.CompactorBasePrompt(),
		currentContextWindow,
	)

	// LLM-driven mini compactor for lightweight context management
	ctxManager := compact.NewContextManager(
		compact.DefaultContextManagerConfig(),
		model,
		apiKey,
		currentContextWindow,
		prompt.ContextManagementSystemPrompt(),
		compactor, // Pass compactor to enable compact tool
	)


	slog.Info("Registered tools: read, bash, write, grep, edit", "count", len(registry.All()))

	// Load skills
	_, traceOutputPath, err = initTraceFileHandler(sessionID)
	if err != nil {
		slog.Warn("Failed to create trace handler", "outputDir", traceOutputPath, "error", err)
	} else {
		slog.Info("Trace handler initialized", "outputDir", traceOutputPath)
	}

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

	// Build the full system prompt (used for agent and compactor)
	buildSystemPrompt := func(currentSess *session.Session) string {
		// Use custom system prompt if provided (e.g., via --system-prompt flag)
		if customSystemPrompt != "" {
			slog.Info("Using custom system prompt", "length", len(customSystemPrompt))
			return customSystemPrompt
		}
		// Use workspace to get dynamic cwd for each prompt build
		promptBuilder := prompt.NewBuilderWithWorkspace("", ws)
		promptBuilder.SetTools(registry.All()).SetSkills(skillResult.Skills)

		// Set llm context for system prompt explanation (tells LLM about the mechanism)
		// The actual content is injected dynamically in the agent loop.
		if currentSess != nil {
			sessionDir := currentSess.GetDir()
			if sessionDir != "" {
				wm := agentctx.NewLLMContext(sessionDir)
				_ = wm // LLM context loaded by compact system
			}
		}

		return promptBuilder.Build()
	}
	systemPrompt := buildSystemPrompt(sess)

	// Helper function to create a new agent context
	// restoreLLMContextFromCompaction restores the llm context overview.md
	// from the latest compaction summary on the current session branch.
	restoreLLMContextFromCompaction := func(sess *session.Session) {
		// Get the latest compaction summary
		summary := sess.GetLastCompactionSummary()
		if summary == "" {
			// No compaction summary found, nothing to restore
			slog.Info("[resume-on-branch] No compaction summary found, skipping llm context restore")
			return
		}

		// Get llm context and write the summary
		sessionDir := sess.GetDir()
		if sessionDir == "" {
			slog.Warn("[resume-on-branch] No session directory, cannot restore llm context")
			return
		}

		wm := agentctx.NewLLMContext(sessionDir)
		if err := wm.WriteContent(summary); err != nil {
			slog.Warn("[resume-on-branch] Failed to restore llm context", "error", err)
		} else {
			slog.Info("[resume-on-branch] Restored llm context from compaction summary", "summary_len", len(summary))
		}
	}

	createBaseContext := func() *agentctx.AgentContext {
		// Rebuild system prompt from the current session so llm-context paths
		// stay in sync after /resume, /new, /fork, and branch resume operations.
		systemPrompt = buildSystemPrompt(sess)
		ctx := agentctx.NewAgentContext(systemPrompt)
		for _, tool := range registry.All() {
			ctx.AddTool(tool)
		}
		// Initialize llm context and set it on session for compaction summaries
		if sess != nil {
			sessionDir := sess.GetDir()
			if sessionDir != "" {
				// LLMContext is now a string, not a file manager
				// The file management is handled internally by the session
				ctx.LLMContext = ""
			}
			// Restore conversation history from session
			ctx.RecentMessages = sess.GetMessages()
			// Restore agent state and messages from checkpoint (preserves trigger counters, CWD, tokens, etc.)
			// Checkpoint messages are compacted, so prefer them over full session history to reduce token pressure.
			if sessionDir != "" {
				if cpInfo, err := agentctx.LoadLatestCheckpoint(sessionDir); err == nil && cpInfo != nil {
					cpPath := filepath.Join(sessionDir, cpInfo.Path)
					// Try loading full snapshot (messages + state) from checkpoint
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
								if err := ws.SetCWD(snapshot.AgentState.CurrentWorkingDir); err != nil {
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
						// Restore LLM context from checkpoint if available
						if snapshot.LLMContext != "" {
							ctx.LLMContext = snapshot.LLMContext
						}
					} else {
						// Fallback: load only agent state if full snapshot fails
						if savedState, err := agentctx.LoadCheckpointAgentState(cpPath); err == nil {
							ctx.AgentState = savedState
							if savedState.CurrentWorkingDir != "" {
								if err := ws.SetCWD(savedState.CurrentWorkingDir); err != nil {
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
			// Set up persistence callback for compact operations.
			// Compact events are appended to messages.jsonl (immutable log).
			// OnMessagesChanged (SaveMessages full rewrite) is removed.
			ctx.OnCompactEvent = func(detail *agentctx.CompactEventDetail) error {
				return sess.AppendCompactEvent(detail)
			}
		}
		return ctx
	}

	agentCtx := createBaseContext()

	// Pre-config: sessionWriter, sessionComp, executor, toolOutputConfig
	sessionWriter := newSessionWriter(256)
	defer sessionWriter.Close()
	sessionComp := &sessionCompactor{
		session:   sess,
		compactor: compactor,
		writer:    sessionWriter,
	}

	concurrencyConfig := cfg.Concurrency
	if concurrencyConfig == nil {
		concurrencyConfig = config.DefaultConcurrencyConfig()
	}
	executor := agent.NewExecutorPool(map[string]int{
		"maxConcurrentTools": concurrencyConfig.MaxConcurrentTools,
		"toolTimeout":        concurrencyConfig.ToolTimeout,
		"queueTimeout":       concurrencyConfig.QueueTimeout,
	})

	toolOutputConfig := cfg.ToolOutput
	if toolOutputConfig == nil {
		toolOutputConfig = config.DefaultToolOutputConfig()
	}

	// Build LoopConfig with all settings
	loopCfg := cfg.ToLoopConfig(
		config.WithCompactors([]agent.Compactor{ctxManager, sessionComp}),
		config.WithContextWindow(currentContextWindow),
		config.WithToolCallCutoff(compactorConfig.ToolCallCutoff),
		config.WithExecutor(executor),
		config.WithToolOutputLimits(agent.ToolOutputLimits{
			MaxChars: toolOutputConfig.MaxChars,
		}),
	)

	// Set model and apiKey
	loopCfg.Model = model
	loopCfg.APIKey = apiKey
	loopCfg.GetWorkingDir = ws.GetCWD
	loopCfg.GetStartupPath = ws.GetInitialCWD
		loopCfg.GetSessionDir = func() string {
		if sess != nil {
			return sess.GetDir()
		}
		return ""
	}

	// Set max turns limit if specified
	if maxTurns > 0 {
		loopCfg.MaxTurns = maxTurns
		slog.Info("Max turns limit set", "max_turns", maxTurns)
	}

	// Create agent with LoopConfig
		ag := agent.NewAgentFromConfigWithContext(model, apiKey, agentCtx, loopCfg)
	defer ag.Shutdown()

	// Start timeout watchdog if timeout is set
	if timeout > 0 {
		go func() {
			<-time.After(timeout)
			slog.Warn("[RPC] Timeout reached, aborting agent", "timeout", timeout)
			ag.Abort()
		}()
	}

	// Initialize checkpoint manager for persistent state
	var checkpointMgr *agent.AgentContextCheckpointManager
	if sess != nil {
		sessionDir := sess.GetDir()
		if mgr, err := agent.NewAgentContextCheckpointManager(sessionDir); err != nil {
			slog.Warn("Failed to create checkpoint manager", "error", err)
			checkpointMgr = nil
		} else {
			checkpointMgr = mgr
			defer func() {
				if checkpointMgr != nil {
					checkpointMgr.Close()
				}
			}()
		}
	}

	slog.Info("Auto-compact enabled", "maxMessages", compactorConfig.MaxMessages, "maxTokens", compactorConfig.MaxTokens)
	slog.Info("Concurrency control enabled", "maxConcurrentTools", concurrencyConfig.MaxConcurrentTools, "toolTimeout", concurrencyConfig.ToolTimeout)
	slog.Info("Tool output truncation", "maxChars", toolOutputConfig.MaxChars)

	setAgentContext := func(ctx *agentctx.AgentContext) {
		ag.SetContext(ctx)
	}

	bashRunner := newBashRunner()
	bashTimeout := time.Duration(concurrencyConfig.ToolTimeout) * time.Second
	if bashTimeout <= 0 {
		bashTimeout = 30 * time.Second
	}

	// Create RPC server
	server := rpc.NewServer()
	server.SetOutput(output)
	stateMu := sync.Mutex{}
	isStreaming := false
	isCompacting := false
	currentThinkingLevel := "high"
	autoCompactionEnabled := compactorConfig.AutoCompact
	steeringMode := "all"
	followUpMode := "one-at-a-time"
	pendingSteer := false
	ag.SetThinkingLevel(currentThinkingLevel)

	// Helper function to expand /skill:name commands
	expandSkillCommands := func(text string) string {
		return skill.ExpandCommand(text, skillResult.Skills)
	}

	// Trigger automatic compaction right before a new request is executed
	// (prompt/idle-steer), instead of during resume operations.
	compactBeforeRequest := func(trigger string) {
		if compactor == nil || sess == nil {
			return
		}

		messages := ag.GetMessages()
		if !compactor.ShouldCompactOld(messages) {
			return
		}
		if !sess.CanCompact(compactor) {
			slog.Info("Pre-request compaction skipped: session not compactable",
				"trigger", trigger,
				"messages", len(messages),
				"estimatedTokens", compactor.EstimateContextTokensOld(messages))
			return
		}

		beforeCount := len(messages)
		compactionInfo := agent.CompactionInfo{
			Auto:    true,
			Before:  beforeCount,
			Trigger: trigger,
		}

		stateMu.Lock()
		isCompacting = true
		stateMu.Unlock()
		server.EmitEvent(agent.NewCompactionStartEvent(compactionInfo))

		err := runDetachedTraceSpan(
			"compaction",
			traceevent.CategoryEvent,
			[]traceevent.Field{
				{Key: "source", Value: "pre_request"},
				{Key: "auto", Value: true},
				{Key: "trigger", Value: trigger},
				{Key: "before_messages", Value: beforeCount},
			},
			func(_ context.Context, span *traceevent.Span) error {
				result, err := sess.Compact(compactor)
				if err != nil {
					return err
				}

				ag.GetContext().RecentMessages = sess.GetMessages()
				afterCount := len(ag.GetMessages())
				compactionInfo.After = afterCount

				span.AddField("after_messages", afterCount)
				span.AddField("tokens_before", result.TokensBefore)
				span.AddField("tokens_after", result.TokensAfter)
				return nil
			},
		)

		stateMu.Lock()
		isCompacting = false
		stateMu.Unlock()

		if err != nil {
			compactionInfo.Error = err.Error()
			if session.IsNonActionableCompactionError(err) {
				slog.Info("Pre-request compaction skipped", "trigger", trigger, "reason", err)
			} else {
				slog.Error("Pre-request compaction failed", "trigger", trigger, "error", err)
			}
		}
		server.EmitEvent(agent.NewCompactionEndEvent(compactionInfo))
	}


	
	
	
	// Helper function to update checkpoint manager for new session
	updateCheckpointManager := func() error {
		stateMu.Lock()
		defer stateMu.Unlock()

		// Close old checkpoint manager if exists
		if checkpointMgr != nil {
			if err := checkpointMgr.Close(); err != nil {
				slog.Warn("Failed to close old checkpoint manager", "error", err)
			}
		}

		// Create new checkpoint manager for current session
		if sess != nil {
			sessionDir := sess.GetDir()
			if sessionDir != "" {
				mgr, err := agent.NewAgentContextCheckpointManager(sessionDir)
				if err != nil {
					slog.Warn("Failed to create checkpoint manager", "error", err)
					checkpointMgr = nil
				} else {
					checkpointMgr = mgr
					slog.Info("Updated checkpoint manager", "sessionDir", sessionDir)
				}
			} else {
				slog.Warn("Session directory is empty, checkpoint manager not updated")
				checkpointMgr = nil
			}
		}

		return nil
	}

	// === Protocol command handlers ===

	server.Register(rpc.CommandPrompt, func(cmd rpc.RPCCommand) (any, error) {
		// Parse prompt data
		var data struct {
			Message           string            `json:"message"`
			StreamingBehavior string            `json:"streamingBehavior"`
			Images            []json.RawMessage `json:"images"`
		}
		if len(cmd.Data) > 0 {
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return nil, fmt.Errorf("invalid data: %w", err)
			}
		}
		message := cmd.Message
		if message == "" {
			message = data.Message
		}
		message = strings.TrimSpace(message)
		if message == "" {
			return nil, fmt.Errorf("empty prompt message")
		}
		if len(data.Images) > 0 {
			return nil, fmt.Errorf("images are not supported in this RPC implementation")
		}

				// Expand /skill:name commands BEFORE generic slash dispatch.
		// Skill commands like /skill:name are not registered as slash handlers;
		// they are expanded into full prompts and processed normally.
		if skill.IsSkillCommand(message) {
			expandedMessage := expandSkillCommands(message)
			slog.Info("Expanded skill command", "original", message, "skill", skill.ExtractSkillName(message))

			stateMu.Lock()
			streaming := isStreaming
			stateMu.Unlock()

			if streaming {
				// During streaming, treat expanded skill prompt as a steer
				stateMu.Lock()
				pendingSteer = true
				stateMu.Unlock()
				ag.Steer(expandedMessage)
				return nil, nil
			}

			compactBeforeRequest("pre_request_prompt")
			return nil, ag.Prompt(expandedMessage)
		}

		// Intercept slash commands — execute synchronously without agent.
		// Only non-skill slash commands (e.g. /get_state, /compact) reach here.
		if message[0] == '/' {
			cmdName, args, err := command.ParseSlashCommand(message)
			if err != nil {
				return nil, fmt.Errorf("invalid slash command: %w", err)
			}
			handler, ok := server.GetSlashHandler(cmdName)
			if !ok {
				return nil, fmt.Errorf("unknown command: /%s", cmdName)
			}
			return handler(args)
		}

				stateMu.Lock()
		streaming := isStreaming
		mode := steeringMode
		followMode := followUpMode
		pending := pendingSteer
		stateMu.Unlock()

		if streaming {
			behavior := strings.TrimSpace(data.StreamingBehavior)
			if behavior == "" {
				return nil, fmt.Errorf("agent is streaming; specify streamingBehavior")
			}
			switch behavior {
			case "steer":
				if mode == "one-at-a-time" && pending {
					return nil, fmt.Errorf("steer already pending")
				}
				stateMu.Lock()
				pendingSteer = true
				stateMu.Unlock()
				ag.Steer(message)
				return nil, nil
			case "followUp", "follow_up":
				if followMode == "one-at-a-time" && ag.GetPendingFollowUps() > 0 {
					return nil, fmt.Errorf("follow-up queue already has a pending message")
				}
				return nil, ag.FollowUp(message)
			default:
				return nil, fmt.Errorf("invalid streamingBehavior: %s", behavior)
			}
		}

		compactBeforeRequest("pre_request_prompt")
		return nil, ag.Prompt(message)
	})

	server.Register(rpc.CommandSteer, func(cmd rpc.RPCCommand) (any, error) {
		message := cmd.Message
		if message == "" && len(cmd.Data) > 0 {
			var data struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return nil, fmt.Errorf("invalid data: %w", err)
			}
			message = data.Message
		}

		slog.Info("Received steer:", "value", message)

		if strings.TrimSpace(message) == "" {
			return nil, fmt.Errorf("empty steer message")
		}

		// Expand /skill:name commands
		expandedMessage := expandSkillCommands(message)
		if skill.IsSkillCommand(message) {
			slog.Info("Expanded skill command in steer", "original", message, "skill", skill.ExtractSkillName(message))
		}

		stateMu.Lock()
		mode := steeringMode
		pending := pendingSteer
		streaming := isStreaming
		stateMu.Unlock()
		if mode == "one-at-a-time" && pending {
			return nil, fmt.Errorf("steer already pending")
		}
		if !streaming {
			compactBeforeRequest("pre_request_steer")
		}
		stateMu.Lock()
		pendingSteer = true
		stateMu.Unlock()
		ag.Steer(expandedMessage)
		return nil, nil
	})

	server.Register(rpc.CommandFollowUp, func(cmd rpc.RPCCommand) (any, error) {
		message := cmd.Message
		if message == "" && len(cmd.Data) > 0 {
			var data struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(cmd.Data, &data); err != nil {
				return nil, fmt.Errorf("invalid data: %w", err)
			}
			message = data.Message
		}

		slog.Info("Received follow_up:", "value", message)
		if strings.TrimSpace(message) == "" {
			return nil, fmt.Errorf("empty follow-up message")
		}

		// Expand /skill:name commands
		expandedMessage := expandSkillCommands(message)
		if skill.IsSkillCommand(message) {
			slog.Info("Expanded skill command in follow_up", "original", message, "skill", skill.ExtractSkillName(message))
		}

		stateMu.Lock()
		mode := followUpMode
		stateMu.Unlock()
		if mode == "one-at-a-time" && ag.GetPendingFollowUps() > 0 {
			return nil, fmt.Errorf("follow-up queue already has a pending message")
		}
		return nil, ag.FollowUp(expandedMessage)
	})

	server.Register(rpc.CommandAbort, func(cmd rpc.RPCCommand) (any, error) {
		slog.Info("Received abort")
		ag.Abort()
		return nil, nil
	})

	// === Slash command handlers ===
	// All commands below are registered as slash commands.
		// They are invoked via the prompt channel: {"type": "prompt", "message": "/command args"}
	// and intercepted by the prompt handler before entering the agent loop.
	// For backward compatibility, they also handle direct JSON-RPC calls where
	// args is raw JSON from cmd.Data (e.g. {"provider":"xxx","modelId":"yyy"}).

	// parseJSONArgs attempts to unmarshal args as JSON into target.
	// Returns true if args looks like JSON and was successfully parsed.
	parseJSONArgs := func(args string, target any) bool {
		args = strings.TrimSpace(args)
		if len(args) > 0 && args[0] == '{' {
			return json.Unmarshal([]byte(args), target) == nil
		}
		return false
	}

	server.RegisterSlash("clear_session", func(args string) (any, error) {
		slog.Info("Received clear_session")
		if err := sess.Clear(); err != nil {
			return nil, err
		}
		// Clear agent context
		setAgentContext(createBaseContext())
		slog.Info("Session cleared")
		return nil, nil
	})

		server.RegisterSlash("new_session", func(args string) (any, error) {
		var name, title string
		var jsonData struct {
			Name  string `json:"name"`
			Title string `json:"title"`
		}
		if parseJSONArgs(args, &jsonData) {
			name, title = jsonData.Name, jsonData.Title
		} else {
			parts := strings.SplitN(args, " ", 2)
			name = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				title = strings.TrimSpace(parts[1])
			}
		}
		slog.Info("Received new_session", "name", name, "title", title)
		if strings.TrimSpace(name) == "" {
			name = time.Now().Format("20060102-150405")
		}
		if strings.TrimSpace(title) == "" {
			title = name
		}
		newSess, err := sessionMgr.CreateSession(name, title)
		if err != nil {
			return nil, err
		}

		newSessionID := newSess.GetID()

		// Update session manager's current ID
		if err := sessionMgr.SetCurrent(newSessionID); err != nil {
			return nil, err
		}

		// Update current session metadata
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}

		sess = newSess
		sessionComp.Update(sess, compactor)
		setAgentContext(createBaseContext())

		// Update checkpoint manager for new session
		if err := updateCheckpointManager(); err != nil {
			slog.Warn("Failed to update checkpoint manager for new session", "error", err)
		}

		stateMu.Lock()
		sessionID = newSessionID
		sessionName = name
		stateMu.Unlock()

		slog.Info("Created new session", "name", name, "id", newSessionID)
		return map[string]any{"sessionId": newSessionID, "cancelled": false}, nil
	})

	server.RegisterSlash("list_sessions", func(args string) (any, error) {
		slog.Info("Received list_sessions")
		sessions, err := sessionMgr.ListSessions()
		if err != nil {
			return nil, err
		}

		// Get workspace and current directory info
		startupPath := cfg.Workspace  // This is the initial working directory (git root or cwd at startup)
		currentWorkdir := ws.GetCWD() // This is the current working directory

		result := make([]any, len(sessions))
		for i, sess := range sessions {
			// Add workspace info to each session
			sess.Workspace = startupPath
			sess.CurrentWorkdir = currentWorkdir
			result[i] = sess
		}
		return map[string]any{"sessions": result}, nil
	})

		server.RegisterSlash("switch_session", func(args string) (any, error) {
		var jsonData struct {
			ID          string `json:"id"`
			SessionPath string `json:"sessionPath"`
		}
		id := strings.TrimSpace(args)
		if parseJSONArgs(args, &jsonData) {
			id = jsonData.ID
			if jsonData.SessionPath != "" {
				id = jsonData.SessionPath
			}
		}
		slog.Info("Received switch_session: id=", "id", id)
		if id == "" {
			return nil, fmt.Errorf("session id is required")
		}

		// Treat absolute or relative path as session file
		if strings.Contains(id, string(os.PathSeparator)) || strings.HasSuffix(id, ".jsonl") {
			sessionPath, err := normalizeSessionPath(id)
			if err != nil {
				return nil, err
			}
			// LoadSessionLazy expects session directory, not file path
			// Extract directory if sessionPath points to messages.jsonl
			sessionDir := sessionPath
			if strings.HasSuffix(sessionPath, ".jsonl") {
				info, err := os.Stat(sessionPath)
				if err != nil {
					return nil, err
				}
				if !info.IsDir() {
					sessionDir = filepath.Dir(sessionPath)
				}
			}
			opts := session.DefaultLoadOptions()
			newSess, err := session.LoadSessionLazy(sessionDir, opts)
			if err != nil {
				return nil, err
			}
			newSessionID := newSess.GetID()
			sessionsDir = sessionDir
			sessionMgr = session.NewSessionManager(sessionsDir)
			_ = sessionMgr.SetCurrent(newSessionID)
			if err := sessionMgr.SaveCurrent(); err != nil {
				slog.Info("Failed to update session metadata:", "value", err)
			}

			// Clear agent context and load new messages
			sess = newSess
			sessionComp.Update(sess, compactor)
			setAgentContext(createBaseContext())
			// Restore last compaction summary if available
			ag.GetContext().LastCompactionSummary = newSess.GetLastCompactionSummary()

			// Update checkpoint manager for new session
			if err := updateCheckpointManager(); err != nil {
				slog.Warn("Failed to update checkpoint manager for new session", "error", err)
			}

			stateMu.Lock()
			sessionID = newSessionID
			sessionName = resolveSessionName(sessionMgr, newSessionID)
			stateMu.Unlock()

			// Update trace handler session ID
			if handler := traceevent.GetHandler(); handler != nil {
				if fh, ok := handler.(*traceevent.FileHandler); ok {
					fh.SetSessionID(newSessionID)
				}
			}

						slog.Info("Switched to session", "id", newSessionID, "count", len(newSess.GetMessages()))
			return map[string]any{"switched": true, "cancelled": false}, nil
		}

		if err := sessionMgr.SetCurrent(id); err != nil {
			return nil, err
		}

		// Load the new session
		newSess, err := sessionMgr.GetSession(id)
		if err != nil {
			return nil, err
		}
		newSessionID := newSess.GetID()
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}

		// Clear agent context and load new messages
		sess = newSess
		sessionComp.Update(sess, compactor)
		setAgentContext(createBaseContext())
		// Restore last compaction summary if available
		ag.GetContext().LastCompactionSummary = newSess.GetLastCompactionSummary()

		// Update checkpoint manager for new session
		if err := updateCheckpointManager(); err != nil {
			slog.Warn("Failed to update checkpoint manager for new session", "error", err)
		}

		stateMu.Lock()
		sessionID = newSessionID
		sessionName = resolveSessionName(sessionMgr, newSessionID)
		stateMu.Unlock()

		// Update trace handler session ID
		if handler := traceevent.GetHandler(); handler != nil {
			if fh, ok := handler.(*traceevent.FileHandler); ok {
				fh.SetSessionID(newSessionID)
			}
		}

						slog.Info("Switched to session", "id", newSessionID, "count", len(newSess.GetMessages()))
		return map[string]any{"switched": true, "cancelled": false}, nil
	})

	server.RegisterSlash("delete_session", func(args string) (any, error) {
		var jsonData struct {
			ID string `json:"id"`
		}
		id := strings.TrimSpace(args)
		if parseJSONArgs(args, &jsonData) && jsonData.ID != "" {
			id = jsonData.ID
		}
						slog.Info("Received delete_session: id=", "id", id)
		if err := sessionMgr.DeleteSession(id); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true}, nil
	})

		server.RegisterSlash("set_session_name", func(args string) (any, error) {
		name := strings.TrimSpace(args)
		var jsonData struct {
			Name string `json:"name"`
		}
		if parseJSONArgs(args, &jsonData) {
			name = jsonData.Name
		}
		slog.Info("Received set_session_name", "name", name)
		if name == "" {
			return nil, fmt.Errorf("session name cannot be empty")
		}
		if _, err := sess.AppendSessionInfo(name, ""); err != nil {
			return nil, err
		}
		if err := sessionMgr.UpdateSessionName(sessionID, name, ""); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		stateMu.Lock()
		sessionName = name
		stateMu.Unlock()
		return nil, nil
	})

	server.RegisterSlash("get_state", func(args string) (any, error) {
		slog.Info("Received get_state")
		compactionState := buildCompactionState(compactorConfig, compactor)
		stateMu.Lock()
		currentSessionID := sessionID
		currentSessionName := sessionName
		streaming := isStreaming
		compacting := isCompacting
		thinkingLevel := currentThinkingLevel
		autoCompact := autoCompactionEnabled
		currentSteeringMode := steeringMode
		currentFollowUpMode := followUpMode
		modelInfo := currentModelInfo
		stateMu.Unlock()

		// Get current trace file path from FileHandler
		aiLogPath := traceOutputPath
		if handler := traceevent.GetHandler(); handler != nil {
			if fh, ok := handler.(*traceevent.FileHandler); ok {
				aiLogPath = fh.TraceFilePath("")
			}
		}

		return &rpc.SessionState{
			Model:                 &modelInfo,
			ThinkingLevel:         thinkingLevel,
			IsStreaming:           streaming,
			IsCompacting:          compacting,
			SteeringMode:          currentSteeringMode,
			FollowUpMode:          currentFollowUpMode,
			SessionFile:           sess.GetPath(),
			SessionID:             currentSessionID,
			SessionName:           currentSessionName,
			AIPid:                 os.Getpid(),
			AILogPath:             aiLogPath,
			AIWorkingDir:          ws.GetCWD(),
			AIStartupPath:         ws.GetGitRoot(),
			AutoCompactionEnabled: autoCompact,
			MessageCount:          len(ag.GetMessages()),
			PendingMessageCount:   ag.GetPendingFollowUps(),
			Compaction:            compactionState,
		}, nil
	})

	server.RegisterSlash("get_messages", func(args string) (any, error) {
		slog.Info("Received get_messages")
		messages := ag.GetMessages()
		result := make([]any, len(messages))
		for i, msg := range messages {
			result[i] = msg
		}
		return map[string]any{"messages": result}, nil
	})

	server.RegisterSlash("compact", func(args string) (any, error) {
		slog.Info("Received compact")
		beforeCount := len(ag.GetMessages())

		// Estimate current context usage
		estimatedTokens := compactor.EstimateContextTokensOld(ag.GetMessages())
		keepTokens := compactor.KeepRecentTokens()

		// Check if compaction is possible before starting
		if !sess.CanCompact(compactor) {
			if estimatedTokens < keepTokens {
				return nil, fmt.Errorf("all %d messages (%d tokens) fit within keep-recent budget (%d tokens); no compaction needed",
					beforeCount, estimatedTokens, keepTokens)
			}
			return nil, fmt.Errorf("no messages available for compaction (all within retention window)")
		}

		compactionInfo := agent.CompactionInfo{
			Auto:    false,
			Before:  beforeCount,
			Trigger: "manual_command",
		}
		stateMu.Lock()
		isCompacting = true
		stateMu.Unlock()
		server.EmitEvent(agent.NewCompactionStartEvent(compactionInfo))
		defer func() {
			stateMu.Lock()
			isCompacting = false
			stateMu.Unlock()
			server.EmitEvent(agent.NewCompactionEndEvent(compactionInfo))
		}()

		var response *rpc.CompactResult
		err := runDetachedTraceSpan(
			"compaction",
			traceevent.CategoryEvent,
			[]traceevent.Field{{Key: "source", Value: "manual"}},
			func(_ context.Context, span *traceevent.Span) error {
				span.AddField("before_messages", beforeCount)

				result, err := sess.Compact(compactor)
				if err != nil {
					slog.Info("Compact failed:", "value", err)
					return err
				}

				// Replace messages with compacted version
				ag.GetContext().RecentMessages = sess.GetMessages()

				afterCount := len(ag.GetMessages())
				span.AddField("after_messages", afterCount)
				span.AddField("tokens_before", result.TokensBefore)
				span.AddField("tokens_after", result.TokensAfter)
				compactionInfo.After = afterCount

				slog.Info("Compact successful", "before", beforeCount, "after", afterCount)
				response = &rpc.CompactResult{
					FirstKeptEntryID: result.FirstKeptEntryID,
					TokensBefore:     result.TokensBefore,
					TokensAfter:      result.TokensAfter,
				}

				// Create checkpoint after manual compaction to preserve AgentState for resume
				if checkpointMgr != nil && checkpointMgr.ShouldCheckpoint() {
					agentCtx := ag.GetContext()
					slog.Info("[Loop] Creating checkpoint after manual compact", "trigger", "manual_command", "turn", agentCtx.AgentState.TotalTurns)
					checkpointTurn, err := checkpointMgr.CreateSnapshot(agentCtx, agentCtx.LLMContext, agentCtx.AgentState.TotalTurns)
					if err != nil {
						slog.Warn("[Loop] Failed to create checkpoint after manual compact", "error", err, "turn", agentCtx.AgentState.TotalTurns)
					} else {
						slog.Info("[Loop] Checkpoint created after manual compact", "trigger", "manual_command", "checkpoint_turn", checkpointTurn)
					}
				}
				return nil
			},
		)
		if err != nil {
			compactionInfo.Error = err.Error()
			return nil, err
		}
		return response, nil
	})

	server.RegisterSlash("get_available_models", func(args string) (any, error) {
		slog.Info("Received get_available_models")
		specs, modelsPath, err := loadModelSpecs(cfg)
		if err != nil {
			return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
		}

		specs = filterModelSpecsWithKeys(specs)
		if len(specs) == 0 {
			authPath, _ := config.GetDefaultAuthPath()
			return nil, fmt.Errorf("no models available (missing API keys?). Set provider keys or update %s", authPath)
		}

		models := make([]rpc.ModelInfo, 0, len(specs))
		for _, spec := range specs {
			models = append(models, modelInfoFromSpec(spec))
		}
		return map[string]any{"models": models}, nil
	})

		server.RegisterSlash("set_model", func(args string) (any, error) {
		var provider, modelID string
		var jsonData struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		}
		if parseJSONArgs(args, &jsonData) {
			provider, modelID = jsonData.Provider, jsonData.ModelID
		} else {
			parts := strings.SplitN(args, " ", 2)
			provider = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				modelID = strings.TrimSpace(parts[1])
			}
		}
		slog.Info("Received set_model", "provider", provider, "modelId", modelID)
		if strings.TrimSpace(provider) == "" || strings.TrimSpace(modelID) == "" {
			return nil, fmt.Errorf("provider and modelId are required")
		}

		specs, modelsPath, err := loadModelSpecs(cfg)
		if err != nil {
			return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
		}
		filtered := filterModelSpecsWithKeys(specs)
		spec, ok := findModelSpec(filtered, provider, modelID)
		if !ok {
			if _, exists := findModelSpec(specs, provider, modelID); exists {
				authPath, _ := config.GetDefaultAuthPath()
				envVar := strings.ToUpper(strings.TrimSpace(provider)) + "_API_KEY"
				return nil, fmt.Errorf("no API key for %q (set %s or update %s)", provider, envVar, authPath)
			}
			return nil, fmt.Errorf("model not found: %s/%s (edit %s)", provider, modelID, modelsPath)
		}
		if strings.TrimSpace(spec.BaseURL) == "" || strings.TrimSpace(spec.API) == "" {
			return nil, fmt.Errorf("model %s/%s missing baseUrl or api in %s", spec.Provider, spec.ID, modelsPath)
		}

		newAPIKey, err := config.ResolveAPIKey(spec.Provider)
		if err != nil {
			return nil, err
		}

		model = llm.Model{
			ID:            spec.ID,
			Provider:      spec.Provider,
			BaseURL:       spec.BaseURL,
			API:           spec.API,
			ContextWindow: spec.ContextWindow,
			MaxTokens:     spec.MaxTokens,
		}
		apiKey = newAPIKey

		cfg.Model.ID = spec.ID
		cfg.Model.Provider = spec.Provider
		cfg.Model.BaseURL = spec.BaseURL
		cfg.Model.API = spec.API
		cfg.Model.MaxTokens = spec.MaxTokens

		ag.SetModel(model)
		ag.SetAPIKey(apiKey)

		// Recreate compactor with new model
		compactor = compact.NewCompactor(compactorConfig, model, apiKey, systemPrompt, spec.ContextWindow)
		sessionComp.Update(sess, compactor)
		ag.SetCompactor(sessionComp)
		ag.SetContextWindow(spec.ContextWindow)

		if err := config.SaveConfig(cfg, configPath); err != nil {
			slog.Info("Failed to save config:", "value", err)
		}

		info := modelInfoFromSpec(spec)
		stateMu.Lock()
		currentModelInfo = info
		currentContextWindow = spec.ContextWindow
		stateMu.Unlock()
		return &info, nil
	})

	server.RegisterSlash("cycle_model", func(args string) (any, error) {
		slog.Info("Received cycle_model")
		specs, modelsPath, err := loadModelSpecs(cfg)
		if err != nil {
			return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
		}
		filtered := filterModelSpecsWithKeys(specs)
		if len(filtered) == 0 {
			authPath, _ := config.GetDefaultAuthPath()
			return nil, fmt.Errorf("no models available (missing API keys?). Set provider keys or update %s", authPath)
		}
		if len(filtered) == 1 {
			return nil, nil
		}

		index := -1
		for i, spec := range filtered {
			if spec.Provider == model.Provider && spec.ID == model.ID {
				index = i
				break
			}
		}
		next := filtered[0]
		if index >= 0 {
			next = filtered[(index+1)%len(filtered)]
		}

		newAPIKey, err := config.ResolveAPIKey(next.Provider)
		if err != nil {
			return nil, err
		}

		model = llm.Model{
			ID:            next.ID,
			Provider:      next.Provider,
			BaseURL:       next.BaseURL,
			API:           next.API,
			ContextWindow: next.ContextWindow,
			MaxTokens:     next.MaxTokens,
		}
		apiKey = newAPIKey

		cfg.Model.ID = next.ID
		cfg.Model.Provider = next.Provider
		cfg.Model.BaseURL = next.BaseURL
		cfg.Model.API = next.API
		cfg.Model.MaxTokens = next.MaxTokens

		ag.SetModel(model)
		ag.SetAPIKey(apiKey)

		// Recreate compactor with new model
		compactor = compact.NewCompactor(compactorConfig, model, apiKey, systemPrompt, next.ContextWindow)
		sessionComp.Update(sess, compactor)
		ag.SetCompactor(sessionComp)
		ag.SetContextWindow(next.ContextWindow)

		if err := config.SaveConfig(cfg, configPath); err != nil {
			slog.Info("Failed to save config:", "value", err)
		}

		info := modelInfoFromSpec(next)
		stateMu.Lock()
		currentModelInfo = info
		currentContextWindow = next.ContextWindow
		stateMu.Unlock()

		return &rpc.CycleModelResult{
			Model:         info,
			ThinkingLevel: currentThinkingLevel,
			IsScoped:      false,
		}, nil
	})

	skillCommands := buildSkillCommands(skillResult.Skills)

	server.RegisterSlash("get_commands", func(args string) (any, error) {
		slog.Info("Received get_commands")
		return map[string]any{"commands": skillCommands}, nil
	})

	server.RegisterSlash("get_session_stats", func(args string) (any, error) {
		slog.Info("Received get_session_stats")
		messages := ag.GetMessages()
		userCount, assistantCount, toolCalls, toolResults, tokens, cost := collectSessionUsage(messages)

		// Estimate system prompt and tools tokens for display purposes only
		// Uses the unified token estimation from AgentContext for consistency
		// with runtime compaction decisions.
		agentCtx := ag.GetContext()

		// Build fresh system prompt for estimation
		currentSystemPrompt := buildSystemPrompt(sess)

		// Estimate tokens from string lengths
		tokens.SystemPromptTokens = len(currentSystemPrompt) / 4

		// Use unified tools token estimation
		tokens.SystemToolsTokens = agentCtx.EstimateToolsTokens()

		// Calculate active window tokens (current conversation) for percentage display
		// This aligns with runtime_state and represents what's actually sent to LLM
		activeWindowTokens := agent.EstimateConversationTokens(messages)
		tokens.ActiveWindowTokens = activeWindowTokens + tokens.SystemPromptTokens + tokens.SystemToolsTokens

		// Note: tokens.Total is cumulative across the entire session for cost tracking
		// tokens.ActiveWindowTokens is the current active turn + system prompt + tools

		stateMu.Lock()
		currentSessionID := sessionID
		stateMu.Unlock()
		var tokenRate *rpc.TokenRateStats
		if metrics := ag.GetMetrics(); metrics != nil {
			llmMetrics := metrics.GetLLMMetrics()
			tokenRate = &rpc.TokenRateStats{
				ActiveInputPerSec:   llmMetrics.ActiveInputTokensPerSec,
				ActiveOutputPerSec:  llmMetrics.ActiveOutputTokensPerSec,
				ActiveTotalPerSec:   llmMetrics.ActiveTotalTokensPerSec,
				WallInputPerSec:     llmMetrics.WallInputTokensPerSec,
				WallOutputPerSec:    llmMetrics.WallOutputTokensPerSec,
				WallTotalPerSec:     llmMetrics.WallTotalTokensPerSec,
				LastInputPerSec:     llmMetrics.LastInputTokensPerSec,
				LastOutputPerSec:    llmMetrics.LastOutputTokensPerSec,
				LastTotalPerSec:     llmMetrics.LastTotalTokensPerSec,
				RecentWindowSeconds: llmMetrics.RecentWindowSeconds,
				RecentInputPerSec:   llmMetrics.RecentInputTokensPerSec,
				RecentOutputPerSec:  llmMetrics.RecentOutputTokensPerSec,
				RecentTotalPerSec:   llmMetrics.RecentTotalTokensPerSec,
			}
		}
		return &rpc.SessionStats{
			SessionFile:       sess.GetPath(),
			SessionID:         currentSessionID,
			UserMessages:      userCount,
			AssistantMessages: assistantCount,
			ToolCalls:         toolCalls,
			ToolResults:       toolResults,
			TotalMessages:     len(messages),
			CompactionCount:   sess.GetCompactionCount(),
			Tokens:            tokens,
			TokenRate:         tokenRate,
			Cost:              cost,
			Workspace:         ws.GetGitRoot(),
			CurrentWorkdir:    ws.GetCWD(),
		}, nil
	})

		server.RegisterSlash("bash", func(args string) (any, error) {
		command := args
		var jsonData struct {
			Command string `json:"command"`
		}
		if parseJSONArgs(args, &jsonData) {
			command = jsonData.Command
		}
		slog.Info("Received bash")
		return bashRunner.Run(ws.GetCWD(), command, bashTimeout)
	})

	server.RegisterSlash("abort_bash", func(args string) (any, error) {
		slog.Info("Received abort_bash")
		return nil, bashRunner.Abort()
	})

		server.RegisterSlash("set_auto_retry", func(args string) (any, error) {
		enabled := strings.TrimSpace(strings.ToLower(args))
		var jsonData struct {
			Enabled bool `json:"enabled"`
		}
		if parseJSONArgs(args, &jsonData) {
			ag.SetAutoRetry(jsonData.Enabled)
			slog.Info("Received set_auto_retry", "enabled", jsonData.Enabled)
			return nil, nil
		}
		slog.Info("Received set_auto_retry", "enabled", enabled)
		ag.SetAutoRetry(enabled == "true" || enabled == "1")
		return nil, nil
	})

	server.RegisterSlash("abort_retry", func(args string) (any, error) {
		slog.Info("Received abort_retry")
		ag.Abort()
		return nil, nil
	})

	server.RegisterSlash("export_html", func(args string) (any, error) {
		slog.Info("Received export_html", "outputPath", args)
		return "", fmt.Errorf("export_html is not supported")
	})

		server.RegisterSlash("set_auto_compaction", func(args string) (any, error) {
		enabled := strings.TrimSpace(strings.ToLower(args))
		var jsonData struct {
			Enabled bool `json:"enabled"`
		}
		if parseJSONArgs(args, &jsonData) {
			compactorConfig.AutoCompact = jsonData.Enabled
			stateMu.Lock()
			autoCompactionEnabled = jsonData.Enabled
			stateMu.Unlock()
			slog.Info("Received set_auto_compaction: enabled=", "value", jsonData.Enabled)
			return nil, nil
		}
		val := enabled == "true" || enabled == "1"
		compactorConfig.AutoCompact = val
		stateMu.Lock()
		autoCompactionEnabled = val
		stateMu.Unlock()
		slog.Info("Received set_auto_compaction: enabled=", "value", val)
		return nil, nil
	})

		server.RegisterSlash("set_tool_call_cutoff", func(args string) (any, error) {
		var cutoff int
		var jsonData struct {
			Cutoff int `json:"cutoff"`
		}
		if parseJSONArgs(args, &jsonData) {
			cutoff = jsonData.Cutoff
		} else {
			var err error
			cutoff, err = strconv.Atoi(strings.TrimSpace(args))
			if err != nil {
				return nil, fmt.Errorf("invalid cutoff value: %w", err)
			}
		}
		slog.Info("Received set_tool_call_cutoff", "cutoff", cutoff)
		if cutoff < 0 {
			return nil, fmt.Errorf("cutoff must be >= 0")
		}
		compactorConfig.ToolCallCutoff = cutoff
		ag.SetToolCallCutoff(cutoff)
		if err := config.SaveConfig(cfg, configPath); err != nil {
			slog.Info("Failed to save config:", "value", err)
		}
		return map[string]any{"cutoff": cutoff}, nil
	})

	validToolSummaryStrategies := map[string]bool{
		"llm":       true,
		"heuristic": true,
		"off":       true,
	}
	validToolSummaryAutomations := map[string]bool{
		"off":      true,
		"fallback": true,
		"always":   true,
	}

		server.RegisterSlash("set_tool_summary_strategy", func(args string) (any, error) {
		var jsonData struct {
			Strategy string `json:"strategy"`
		}
		strategy := strings.ToLower(strings.TrimSpace(args))
		if parseJSONArgs(args, &jsonData) {
			strategy = strings.ToLower(jsonData.Strategy)
		}
		slog.Info("Received set_tool_summary_strategy", "strategy", strategy)
		if !validToolSummaryStrategies[strategy] {
			return nil, fmt.Errorf("invalid tool summary strategy")
		}
				compactorConfig.ToolSummaryStrategy = strategy
		if err := config.SaveConfig(cfg, configPath); err != nil {
			slog.Info("Failed to save config:", "value", err)
		}
		return map[string]any{"strategy": strategy}, nil
	})

		server.RegisterSlash("set_tool_summary_automation", func(args string) (any, error) {
		var jsonData struct {
			Mode string `json:"mode"`
		}
		mode := strings.ToLower(strings.TrimSpace(args))
		if parseJSONArgs(args, &jsonData) {
			mode = strings.ToLower(jsonData.Mode)
		}
		slog.Info("Received set_tool_summary_automation", "mode", mode)
		if !validToolSummaryAutomations[mode] {
			return nil, fmt.Errorf("invalid tool summary automation mode")
		}
		compactorConfig.ToolSummaryAutomation = mode
		if err := config.SaveConfig(cfg, configPath); err != nil {
			slog.Info("Failed to save config:", "value", err)
		}
		return map[string]any{"mode": mode}, nil
	})

		server.RegisterSlash("set_trace_events", func(args string) (any, error) {
		// Parse args — support both space-separated text and JSON {"events": [...]}
		events := strings.Fields(args)
		var jsonData struct {
			Events []string `json:"events"`
		}
		if parseJSONArgs(args, &jsonData) && len(jsonData.Events) > 0 {
			events = jsonData.Events
		}
		slog.Info("Received set_trace_events", "events", events)

		if len(events) == 0 {
			// Empty args means reset to default set.
			return map[string]any{"events": traceevent.ResetToDefaultEvents()}, nil
		}

		normalized := make([]string, 0, len(events))
		for _, e := range events {
			e = strings.ToLower(strings.TrimSpace(e))
			if e == "" {
				continue
			}
			normalized = append(normalized, e)
		}
		if len(normalized) == 0 {
			return map[string]any{"events": traceevent.ResetToDefaultEvents()}, nil
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

		op := normalized[0]
		switch op {
		case "on":
			return map[string]any{"events": traceevent.ResetToDefaultEvents()}, nil
		case "all":
			expanded, _ := traceevent.ExpandEventSelectors([]string{"all"})
			return map[string]any{"events": applyExpanded(expanded, true)}, nil
		case "default":
			return map[string]any{"events": traceevent.ResetToDefaultEvents()}, nil
		case "off", "none":
			traceevent.DisableAllEvents()
			return map[string]any{"events": []string{}}, nil
		case "enable":
			if len(normalized) == 1 {
				return nil, fmt.Errorf("trace-events enable requires at least one selector")
			}
			expanded, unknown := traceevent.ExpandEventSelectors(normalized[1:])
			if len(unknown) > 0 {
				return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
			}
			return map[string]any{"events": applyExpanded(expanded, false)}, nil
		case "disable":
			if len(normalized) == 1 {
				return nil, fmt.Errorf("trace-events disable requires at least one selector")
			}
			expanded, unknown := traceevent.ExpandEventSelectors(normalized[1:])
			if len(unknown) > 0 {
				return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
			}
			for _, eventName := range expanded {
				traceevent.DisableEvent(eventName)
			}
			return map[string]any{"events": traceevent.GetEnabledEvents()}, nil
		default:
			expanded, unknown := traceevent.ExpandEventSelectors(normalized)
			if len(unknown) > 0 {
				return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
			}
			return map[string]any{"events": applyExpanded(expanded, true)}, nil
		}
	})

	server.RegisterSlash("get_trace_events", func(args string) (any, error) {
		slog.Info("Received get_trace_events")
		return map[string]any{"events": traceevent.GetEnabledEvents()}, nil
	})

	server.RegisterSlash("get_workflow_status", func(args string) (any, error) {
		slog.Info("Received get_workflow_status")
				status, err := getWorkflowStatus(ws.GetCWD())
		if err != nil {
			return nil, err
		}
		if status == nil {
			return nil, nil
		}
		return status, nil
	})

		validSteeringModes := map[string]bool{
		"all":           true,
		"immediate":     true,
		"one-at-a-time": true,
	}
	validFollowUpModes := map[string]bool{
		"all":           true,
		"immediate":     true,
		"one-at-a-time": true,
	}

		server.RegisterSlash("set_steering_mode", func(args string) (any, error) {
		var jsonData struct {
			Mode string `json:"mode"`
		}
		mode := strings.ToLower(strings.TrimSpace(args))
		if parseJSONArgs(args, &jsonData) {
			mode = strings.ToLower(jsonData.Mode)
		}
		slog.Info("Received set_steering_mode", "mode", mode)
		if !validSteeringModes[mode] {
			return nil, fmt.Errorf("invalid steering mode")
		}
		stateMu.Lock()
		steeringMode = mode
		stateMu.Unlock()
		return nil, nil
	})

		server.RegisterSlash("set_follow_up_mode", func(args string) (any, error) {
		var jsonData struct {
			Mode string `json:"mode"`
		}
		mode := strings.ToLower(strings.TrimSpace(args))
		if parseJSONArgs(args, &jsonData) {
			mode = strings.ToLower(jsonData.Mode)
		}
		slog.Info("Received set_follow_up_mode", "mode", mode)
		if !validFollowUpModes[mode] {
			return nil, fmt.Errorf("invalid follow-up mode")
		}
		stateMu.Lock()
		followUpMode = mode
		stateMu.Unlock()
		return nil, nil
	})

	validThinkingLevels := map[string]bool{
		"off":     true,
		"minimal": true,
		"low":     true,
		"medium":  true,
		"high":    true,
		"xhigh":   true,
	}
	thinkingCycle := []string{"off", "minimal", "low", "medium", "high", "xhigh"}

		server.RegisterSlash("set_thinking_level", func(args string) (any, error) {
		level := strings.ToLower(strings.TrimSpace(args))
		var jsonData struct {
			Level string `json:"level"`
		}
		if parseJSONArgs(args, &jsonData) {
			level = strings.ToLower(strings.TrimSpace(jsonData.Level))
		}
		if !validThinkingLevels[level] {
			return nil, fmt.Errorf("invalid thinking level")
		}
		stateMu.Lock()
		currentThinkingLevel = level
		stateMu.Unlock()
		ag.SetThinkingLevel(level)
		return map[string]any{"level": level}, nil
	})

	server.RegisterSlash("cycle_thinking_level", func(args string) (any, error) {
		stateMu.Lock()
		current := currentThinkingLevel
		stateMu.Unlock()

		next := "medium"
		for i, level := range thinkingCycle {
			if level == current {
				next = thinkingCycle[(i+1)%len(thinkingCycle)]
				break
			}
		}

		stateMu.Lock()
		currentThinkingLevel = next
		stateMu.Unlock()
		ag.SetThinkingLevel(next)
				return map[string]any{"level": next}, nil
	})

	server.RegisterSlash("get_last_assistant_text", func(args string) (any, error) {
		slog.Info("Received get_last_assistant_text")
		messages := ag.GetMessages()
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" {
				return map[string]any{"text": messages[i].ExtractText()}, nil
			}
		}
		return "", nil
	})

	server.RegisterSlash("get_fork_messages", func(args string) (any, error) {
		slog.Info("Received get_fork_messages")
		forkMessages := sess.GetUserMessagesForForking()
		result := make([]rpc.ForkMessage, 0, len(forkMessages))
		for _, msg := range forkMessages {
			result = append(result, rpc.ForkMessage{
				EntryID: msg.EntryID,
				Text:    msg.Text,
			})
		}
		return map[string]any{"messages": result}, nil
	})

	server.RegisterSlash("get_tree", func(args string) (any, error) {
		slog.Info("Received get_tree")
		entries := sess.GetEntries()
		tree := buildTreeEntries(entries, sess.GetLeafID())
		return map[string]any{"entries": tree}, nil
	})

		server.RegisterSlash("resume_on_branch", func(args string) (any, error) {
		var jsonData struct {
			EntryID string `json:"entryId"`
		}
		entryID := strings.TrimSpace(args)
		if parseJSONArgs(args, &jsonData) && jsonData.EntryID != "" {
			entryID = jsonData.EntryID
		}
		slog.Info("Received resume_on_branch", "entryId", entryID)
		stateMu.Lock()
		streaming := isStreaming
		stateMu.Unlock()
		if streaming {
			return nil, fmt.Errorf("agent is busy")
		}

		if entryID == "" {
			return nil, fmt.Errorf("entryId is required")
		}

		if entryID == "root" {
			sess.ResetLeaf()
		} else {
			if err := sess.Branch(entryID); err != nil {
				return nil, err
			}
		}

		setAgentContext(createBaseContext())

		// Restore llm context from the latest compaction summary on this branch
		restoreLLMContextFromCompaction(sess)

		// Update checkpoint manager (session might have changed due to branch switch)
		if err := updateCheckpointManager(); err != nil {
			slog.Warn("Failed to update checkpoint manager for branch resume", "error", err)
		}

		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		return map[string]any{"switched": true}, nil
		return nil, nil
	})

		server.RegisterSlash("fork", func(args string) (any, error) {
		var jsonData struct {
			EntryID string `json:"entryId"`
		}
		entryID := strings.TrimSpace(args)
		if parseJSONArgs(args, &jsonData) && jsonData.EntryID != "" {
			entryID = jsonData.EntryID
		}
		slog.Info("Received fork: entryId=", "value", entryID)
		entry, ok := sess.GetEntry(entryID)
		if !ok || entry.Type != session.EntryTypeMessage || entry.Message == nil || entry.Message.Role != "user" {
			return nil, fmt.Errorf("invalid entryId: %s", entryID)
		}

		text := entry.Message.ExtractText()
		name := fmt.Sprintf("fork-%s", time.Now().Format("20060102-150405"))
		title := "Forked Session"
		newSess, err := sessionMgr.ForkSessionFrom(sess, entry.ParentID, name, title)
		if err != nil {
			return nil, err
		}

		newSessionID := newSess.GetID()
		if err := sessionMgr.SetCurrent(newSessionID); err != nil {
			return nil, err
		}
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}

		sess = newSess
		sessionComp.Update(sess, compactor)
		setAgentContext(createBaseContext())

		// Update checkpoint manager for new session
		if err := updateCheckpointManager(); err != nil {
			slog.Warn("Failed to update checkpoint manager for fork", "error", err)
		}

		stateMu.Lock()
		sessionID = newSessionID
		sessionName = name
		stateMu.Unlock()

		slog.Info("Forked to new session", "name", name, "id", newSessionID)
		return &rpc.ForkResult{Cancelled: false, Text: text}, nil
	})
	// Start event emitter
	eventEmitterDone := make(chan struct{})
	shutdownEmitter := make(chan struct{})
	go func() {
		defer close(eventEmitterDone)
		for {
			select {
			case event := <-ag.Events():
				if event.Type == "agent_start" {
					stateMu.Lock()
					isStreaming = true
					stateMu.Unlock()
				}
				if event.Type == "agent_end" {
					stateMu.Lock()
					isStreaming = false
					isCompacting = false
					pendingSteer = false
					stateMu.Unlock()
				}
				if event.Type == "compaction_start" {
					stateMu.Lock()
					isCompacting = true
					stateMu.Unlock()
				}
				if event.Type == "compaction_end" {
					stateMu.Lock()
					isCompacting = false
					stateMu.Unlock()
				}

				if event.Type == "message_end" && event.Message != nil {
					if sessionWriter != nil {
						sessionWriter.Append(sess, *event.Message)
					}
				}
				if event.Type == "tool_execution_end" && event.Result != nil {
					if sessionWriter != nil {
						sessionWriter.Append(sess, *event.Result)
					}
				}
				if event.Type == "agent_end" && sessionWriter != nil {
					// No longer Replace (full rewrite of messages.jsonl).
					// Messages are appended one-by-one during the turn via sessionWriter.Append.
					// Compact events are appended via AppendCompactEvent.
				}

				emitAt := time.Now()
				if event.EventAt == 0 {
					event.EventAt = emitAt.UnixNano()
				}
				server.EmitEvent(event)

				if event.Type == "agent_end" {
					go func() {
						if err := sessionMgr.SaveCurrent(); err != nil {
							slog.Info("Failed to update session metadata:", "value", err)
						}
					}()
				}

			case <-shutdownEmitter:
				for {
					select {
					case event := <-ag.Events():
						if event.Type == "agent_start" {
							stateMu.Lock()
							isStreaming = true
							stateMu.Unlock()
						}
						if event.Type == "agent_end" {
							stateMu.Lock()
							isStreaming = false
							isCompacting = false
							pendingSteer = false
							stateMu.Unlock()
						}
						if event.Type == "compaction_start" {
							stateMu.Lock()
							isCompacting = true
							stateMu.Unlock()
						}
						if event.Type == "compaction_end" {
							stateMu.Lock()
							isCompacting = false
							stateMu.Unlock()
						}

						if event.Type == "message_end" && event.Message != nil {
							if sessionWriter != nil {
								sessionWriter.Append(sess, *event.Message)
							}
						}
						if event.Type == "tool_execution_end" && event.Result != nil {
							if sessionWriter != nil {
								sessionWriter.Append(sess, *event.Result)
							}
						}
						if event.Type == "agent_end" && sessionWriter != nil {
							// No longer Replace (full rewrite of messages.jsonl).
							// Messages are appended one-by-one during the turn via sessionWriter.Append.
							// Compact events are appended via AppendCompactEvent.
						}

						emitAt := time.Now()
						if event.EventAt == 0 {
							event.EventAt = emitAt.UnixNano()
						}
						server.EmitEvent(event)
						if event.Type == "agent_end" {
							go func() {
								if err := sessionMgr.SaveCurrent(); err != nil {
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
	allTools := registry.All()
	toolNames := make([]string, len(allTools))
	for i, t := range allTools {
		toolNames[i] = t.Name()
	}
	server.EmitEvent(map[string]any{
		"type":  "server_start",
		"model": model.ID,
		"tools": toolNames,
	})

	// Start debug server if enabled
	if debugAddr != "" {
		go func() {
			// Register metrics endpoint on DefaultServeMux
			http.HandleFunc("/debug/metrics", func(w http.ResponseWriter, r *http.Request) {
				metrics := ag.GetMetrics()
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

			slog.Info("Debug server listening on", "value", debugAddr)
			slog.Info("Debug endpoints available at:")
			slog.Info("- http:///debug/pprof/          (profiling index)", "value", debugAddr)
			slog.Info("- http:///debug/pprof/profile   (CPU profile)", "value", debugAddr)
			slog.Info("- http:///debug/pprof/heap       (memory profile)", "value", debugAddr)
			slog.Info("- http:///debug/pprof/goroutine  (goroutine dump)", "value", debugAddr)
			slog.Info("- http:///debug/pprof/trace      (execution trace)", "value", debugAddr)
			slog.Info("- http:///debug/metrics         (agent metrics)", "value", debugAddr)

			if err := http.ListenAndServe(debugAddr, nil); err != nil {
				slog.Error("Debug server error:", "error", err)
			}
		}()
	}

	// Run RPC server
	slog.Info("RPC server started", "model", model.ID, "cwd", cwd)
	slog.Info("Waiting for commands...")
	runErr := server.RunWithIO(input, output)

	// Server stopped, event emitter will exit automatically
	slog.Info("RPC server stopped, waiting for cleanup...")

	// Wait for agent to complete
	slog.Info("Waiting for agent to complete...")
	ag.Wait()

	close(shutdownEmitter)
	<-eventEmitterDone

	slog.Info("Agent completed, exiting...")
	return runErr
}

type bashRunner struct {
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

func newBashRunner() *bashRunner {
	return &bashRunner{}
}

func (b *bashRunner) Run(cwd, command string, timeout time.Duration) (*rpc.BashResult, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return nil, fmt.Errorf("bash already running")
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	b.running = true
	b.cancel = cancel
	b.mu.Unlock()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	ctxErr := ctx.Err()

	b.mu.Lock()
	b.running = false
	b.cancel = nil
	b.mu.Unlock()
	cancel()

	result := &rpc.BashResult{
		Output: string(output),
	}
	if ctxErr == context.DeadlineExceeded {
		result.ExitCode = -1
		result.Error = "command timed out"
		return result, nil
	}
	if ctxErr == context.Canceled {
		result.ExitCode = -1
		result.Error = "command cancelled"
		return result, nil
	}
	if err != nil {
		result.Error = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
		return result, nil
	}
	result.ExitCode = 0
	return result, nil
}

func (b *bashRunner) Abort() error {
	b.mu.Lock()
	cancel := b.cancel
	running := b.running
	b.mu.Unlock()
	if !running || cancel == nil {
		return fmt.Errorf("no bash command running")
	}
	cancel()
	return nil
}

// getWorkflowStatus reads .workflow/state.json and tasks.md to return workflow state.
func getWorkflowStatus(cwd string) (*rpc.WorkflowState, error) {
	state := &rpc.WorkflowState{
		Phase:      "not_started",
		LastUpdate: time.Now().UTC().Format(time.RFC3339),
	}

	workflowDir := filepath.Join(cwd, ".workflow")
	stateFile := filepath.Join(workflowDir, "state.json")

	// Read state.json if it exists
	if data, err := os.ReadFile(stateFile); err == nil {
		var stateData struct {
			Phase     string `json:"phase"`
			StartedAt string `json:"started_at"`
			TasksFile string `json:"tasks_file"`
		}
		if err := json.Unmarshal(data, &stateData); err == nil {
			state.Phase = stateData.Phase
			state.StartedAt = stateData.StartedAt
			if stateData.TasksFile != "" {
				// Handle relative or absolute path
				if filepath.IsAbs(stateData.TasksFile) {
					state.TasksFile = stateData.TasksFile
				} else {
					state.TasksFile = filepath.Join(cwd, stateData.TasksFile)
				}
			}
		}
	}

	// Read tasks.md if specified
	if state.TasksFile != "" {
		tasksData, err := os.ReadFile(state.TasksFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read tasks file %s: %w", state.TasksFile, err)
		}

		// Parse task statuses
		lines := strings.Split(string(tasksData), "\n")
		var inProgressTask *rpc.WorkflowTask

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "- [") {
				continue
			}

			status := "pending"
			if strings.HasPrefix(line, "- [x]") || strings.HasPrefix(line, "- [X]") {
				status = "done"
				state.DoneTasks++
			} else if strings.HasPrefix(line, "- [-]") {
				status = "in_progress"
				state.PendingTasks++ // In-progress also counts toward pending
			} else if strings.HasPrefix(line, "- [!]") {
				status = "failed"
				state.FailedTasks++
			} else {
				state.PendingTasks++
			}

			state.TotalTasks++

			// Extract task ID and description for in-progress task
			if status == "in_progress" && inProgressTask == nil {
				// Extract task ID (e.g., TASK001, T01, etc.)
				var id string
				idMatch := regexp.MustCompile(`[A-Z]{3,}\d+|[A-Z]\d+`).FindString(line)
				if idMatch != "" {
					id = idMatch
				}

				// Extract description: remove checkbox first, then task ID
				desc := line
				// Remove checkbox
				desc = regexp.MustCompile(`^-\s*\[[xX\-\!]\]\s*`).ReplaceAllString(desc, "")
				desc = regexp.MustCompile(`^-\s*\[\s*\]\s*`).ReplaceAllString(desc, "")
				// Remove task ID (e.g., TASK002: or TASK002 )
				desc = regexp.MustCompile(`^[A-Z]{3,}\d+:?\s*`).ReplaceAllString(desc, "")
				desc = strings.TrimSpace(desc)

				inProgressTask = &rpc.WorkflowTask{
					ID:          id,
					Description: desc,
					Status:      status,
				}
			}
		}

		state.InProgressTask = inProgressTask
	}

	return state, nil
}
