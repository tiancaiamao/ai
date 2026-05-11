package main

import (
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"io"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
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

	// Load skill usage stats and register find_skill tool
	skillStats := skill.LoadStats(filepath.Join(agentDir, "skill-stats.json"))
	registry.Register(tools.NewFindSkillTool(skillResult.Skills, skillStats))

	// --- Construct rpcApp ---
	app := &rpcApp{
		customSystemPrompt:    customSystemPrompt,
		maxTurns:              maxTurns,
		output:                output,
		debugAddr:             debugAddr,
		cfg:                   cfg,
		configPath:            configPath,
		model:                 model,
		apiKey:                apiKey,
		activeSpec:            activeSpec,
		currentModelInfo:      currentModelInfo,
		currentContextWindow:  currentContextWindow,
		cwd:                   cwd,
		agentDir:              agentDir,
		sessionPath:           sessionPath,
		sessionMgr:            sessionMgr,
		sess:                  sess,
		sessionID:             sessionID,
		sessionName:           sessionName,
		ws:                    ws,
		registry:              registry,
		compactor:             compactor,
		ctxManager:            ctxManager,
		compactorConfig:       compactorConfig,
		traceOutputPath:       traceOutputPath,
		skillResult:           skillResult,
		skillStats:            skillStats,
		autoCompactionEnabled: compactorConfig.AutoCompact,
		steeringMode:          "all",
		followUpMode:          "one-at-a-time",
		currentThinkingLevel:  "high",
		showThinking:          true,
		showTools:             true,
		showPrefix:            true,
		busyMode:              "steer",
	}

	app.initHelpers()

	// --- Create agent context ---
	agentCtx := app.createBaseContext()

	// --- Pre-config: sessionWriter, sessionComp, executor, toolOutputConfig ---
	sessionWriter := newSessionWriter(256)
	defer sessionWriter.Close()
	sessionComp := &sessionCompactor{
		session:   sess,
		compactor: compactor,
		writer:    sessionWriter,
	}
	app.sessionWriter = sessionWriter
	app.sessionComp = sessionComp

	concurrencyConfig := cfg.Concurrency
	if concurrencyConfig == nil {
		concurrencyConfig = config.DefaultConcurrencyConfig()
	}
	executor := agent.NewToolExecutor(
		concurrencyConfig.MaxConcurrentTools,
		concurrencyConfig.QueueTimeout,
	)

	toolOutputConfig := cfg.ToolOutput
	if toolOutputConfig == nil {
		toolOutputConfig = config.DefaultToolOutputConfig()
	}
	app.toolOutputConfig = toolOutputConfig

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
	app.loopCfg = loopCfg

	// Create agent with LoopConfig
	ag := agent.NewAgentFromConfigWithContext(model, apiKey, agentCtx, loopCfg)
	defer ag.Shutdown()
	ag.SetThinkingLevel("high")
	app.ag = ag

	// Start timeout watchdog if timeout is set
	if timeout > 0 {
		go func() {
			<-time.After(timeout)
			slog.Warn("[RPC] Timeout reached, aborting agent", "timeout", timeout)
			ag.Abort()
		}()
	}

	// Initialize checkpoint manager for persistent state
	if sess != nil {
		sessionDir := sess.GetDir()
		if mgr, err := agent.NewAgentContextCheckpointManager(sessionDir); err != nil {
			slog.Warn("Failed to create checkpoint manager", "error", err)
			app.checkpointMgr = nil
		} else {
			app.checkpointMgr = mgr
			defer func() {
				if app.checkpointMgr != nil {
					app.checkpointMgr.Close()
				}
			}()
		}
	}

	slog.Info("Auto-compact enabled", "maxMessages", compactorConfig.MaxMessages, "maxTokens", compactorConfig.MaxTokens)
	slog.Info("Concurrency control enabled", "maxConcurrentTools", concurrencyConfig.MaxConcurrentTools, "toolTimeout", concurrencyConfig.ToolTimeout)
	slog.Info("Tool output truncation", "maxChars", toolOutputConfig.MaxChars)

	// --- Create RPC server ---
	server := rpc.NewServer()
	server.SetOutput(output)
	app.server = server

	// --- Register all handlers ---
	validToolSummaryStrategies := map[string]bool{"full": true, "summary": true, "full-then-summary": true}
	validToolSummaryAutomations := map[string]bool{"auto": true, "manual": true, "off": true}
	validSteeringModes := map[string]bool{"all": true, "off": true}
	validFollowUpModes := map[string]bool{"one-at-a-time": true, "queue": true, "off": true}
	validThinkingLevels := map[string]bool{"off": true, "low": true, "medium": true, "high": true}

	app.registerHandlers(
		validToolSummaryStrategies,
		validToolSummaryAutomations,
		validSteeringModes,
		validFollowUpModes,
		validThinkingLevels,
	)

	// --- Build skill commands list ---
	app.skillCommands = make([]rpc.SlashCommand, 0)
	for _, cmd := range server.ListSlashCommands() {
		if cmd.Hidden {
			continue
		}
		app.skillCommands = append(app.skillCommands, rpc.SlashCommand{
			Name:        cmd.Name,
			Description: cmd.Description,
		})
	}
	for _, s := range skillResult.Skills {
		app.skillCommands = append(app.skillCommands, rpc.SlashCommand{
			Name:        "/skill:" + s.Name,
			Description: s.Description,
		})
	}

	// --- Start event emitter ---
	shutdownEmitter, eventEmitterDone := app.initEventEmitter()

	// --- Emit start event ---
	app.emitStartEvent()

	// --- Start debug server if enabled ---
	app.startDebugServer()

	// --- Run RPC server ---
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

// formatMessagesForDisplay converts AgentMessages into a structured summary for the /messages command.
// It returns the last `count` messages with previews truncated to maxPreviewLen characters.
func formatMessagesForDisplay(messages []agentctx.AgentMessage, count int, maxPreviewLen int) rpc.MessagesResult {
	total := len(messages)

	start := total - count
	if start < 0 {
		start = 0
	}
	showing := total - start

	formatted := make([]rpc.FormattedMessage, 0, showing)
	for i := start; i < total; i++ {
		msg := messages[i]
		fm := rpc.FormattedMessage{
			Index: i,
			Role:  msg.Role,
		}

		// Build preview from text content
		preview := msg.ExtractText()
		if preview == "" {
			// Try thinking content as fallback for assistant messages
			if thinking := msg.ExtractThinking(); thinking != "" {
				preview = "(thinking) " + thinking
			}
		}
		if len(preview) > maxPreviewLen {
			preview = preview[:maxPreviewLen] + "..."
		}
		fm.Preview = preview

		// Extract tool call names for assistant messages
		toolCalls := msg.ExtractToolCalls()
		if len(toolCalls) > 0 {
			names := make([]string, 0, len(toolCalls))
			for _, tc := range toolCalls {
				names = append(names, tc.Name)
			}
			fm.ToolCalls = names
		}

		// Include tool name for tool results
		if msg.ToolName != "" {
			fm.ToolName = msg.ToolName
		}
		fm.IsError = msg.IsError

		formatted = append(formatted, fm)
	}

	return rpc.MessagesResult{
		Total:    total,
		Showing:  showing,
		Messages: formatted,
	}
}
