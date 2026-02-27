package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	currentModelInfo := modelInfoFromSpec(activeSpec)
	currentContextWindow := activeSpec.ContextWindow

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

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
		sess, err = session.LoadSessionLazy(sessionPath, session.DefaultLoadOptions())
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
	registry := tools.NewRegistry()
	registry.Register(tools.NewReadTool(cwd))
	registry.Register(tools.NewBashTool(cwd))
	registry.Register(tools.NewWriteTool(cwd))
	registry.Register(tools.NewGrepTool(cwd))
	registry.Register(tools.NewEditTool(cwd))

	// Create compactor early so compact_history is present when building the system prompt.
	compactorConfig := cfg.Compactor
	if compactorConfig == nil {
		compactorConfig = compact.DefaultConfig()
	}
	compactor := compact.NewCompactor(
		compactorConfig,
		model,
		apiKey,
		"You are a helpful coding assistant.",
		currentContextWindow,
	)

	// Register compact_history before prompt build so LLM sees it in Tooling section.
	compactHistoryTool := tools.NewCompactHistoryTool(nil, compactor, model, apiKey, "")
	registry.Register(compactHistoryTool)

	slog.Info("Registered tools: read, bash, write, grep, edit, compact_history", "count", len(registry.All()))

	// Load skills
	traceOutputPath, err = initTraceFileHandler()
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

	// Create agent with skills using structured prompt builder

	// - Always output JSON objects as specified by the task schema.
	// - Never output free text or markdown explanations.

	basePrompt := `You are a helpful AI coding assistant.
- If you cannot answer the request, return an empty JSON with error field.
- Do not hallucinate or add unnecessary commentary.`

	// Build the full system prompt (used for agent and compactor)
	buildSystemPrompt := func(currentSess *session.Session) string {
		promptBuilder := prompt.NewBuilder(basePrompt, cwd)
		promptBuilder.SetTools(registry.All()).SetSkills(skillResult.Skills)

		// Set working memory for system prompt explanation (tells LLM about the mechanism)
		// The actual content is injected dynamically in the agent loop.
		if currentSess != nil {
			sessionDir := currentSess.GetDir()
			if sessionDir != "" {
				wm := agent.NewWorkingMemory(sessionDir)
				promptBuilder.SetWorkingMemory(wm)
			}
		}
		return promptBuilder.Build()
	}
	systemPrompt := buildSystemPrompt(sess)

	// Helper function to create a new agent context
	createBaseContext := func() *agent.AgentContext {
		// Rebuild system prompt from the current session so working-memory paths
		// stay in sync after /resume, /new, /fork, and branch resume operations.
		systemPrompt = buildSystemPrompt(sess)
		ctx := agent.NewAgentContext(systemPrompt)
		for _, tool := range registry.All() {
			ctx.AddTool(tool)
		}
		// Initialize working memory and restore messages from session
		if sess != nil {
			sessionDir := sess.GetDir()
			if sessionDir != "" {
				wm := agent.NewWorkingMemory(sessionDir)
				ctx.WorkingMemory = wm
			}
			// Restore conversation history from session
			ctx.Messages = sess.GetMessages()
		}
		return ctx
	}

	agentCtx := createBaseContext()
	compactHistoryTool.SetAgentContext(agentCtx)

	ag := agent.NewAgentWithContext(model, apiKey, agentCtx)
	defer ag.Shutdown()

	// Enable automatic compression
	sessionWriter := newSessionWriter(256)
	defer sessionWriter.Close()
	sessionComp := &sessionCompactor{
		session:   sess,
		compactor: compactor,
		writer:    sessionWriter,
	}
	ag.SetCompactor(sessionComp)
	ag.SetContextWindow(currentContextWindow)
	ag.SetToolCallCutoff(compactorConfig.ToolCallCutoff)
	ag.SetToolSummaryStrategy(compactorConfig.ToolSummaryStrategy)
	ag.SetToolSummaryAutomation(compactorConfig.ToolSummaryAutomation)
	slog.Info("Auto-compact enabled", "maxMessages", compactorConfig.MaxMessages, "maxTokens", compactorConfig.MaxTokens)

	slog.Info("compact_history tool enabled for LLM-driven context management")

	setAgentContext := func(ctx *agent.AgentContext) {
		ag.SetContext(ctx)
		compactHistoryTool.SetAgentContext(ag.GetContext())
	}

	// Set up executor with concurrency control
	concurrencyConfig := cfg.Concurrency
	if concurrencyConfig == nil {
		concurrencyConfig = config.DefaultConcurrencyConfig()
	}

	executor := agent.NewExecutorPool(map[string]int{
		"maxConcurrentTools": concurrencyConfig.MaxConcurrentTools,
		"toolTimeout":        concurrencyConfig.ToolTimeout,
		"queueTimeout":       concurrencyConfig.QueueTimeout,
	})
	ag.SetExecutor(executor)
	slog.Info("Concurrency control enabled", "maxConcurrentTools", concurrencyConfig.MaxConcurrentTools, "toolTimeout", concurrencyConfig.ToolTimeout)

	bashRunner := newBashRunner()
	bashTimeout := time.Duration(concurrencyConfig.ToolTimeout) * time.Second
	if bashTimeout <= 0 {
		bashTimeout = 30 * time.Second
	}

	toolOutputConfig := cfg.ToolOutput
	if toolOutputConfig == nil {
		toolOutputConfig = config.DefaultToolOutputConfig()
	}
	ag.SetToolOutputLimits(agent.ToolOutputLimits{
		MaxLines:             toolOutputConfig.MaxLines,
		MaxBytes:             toolOutputConfig.MaxBytes,
		MaxChars:             toolOutputConfig.MaxChars,
		LargeOutputThreshold: toolOutputConfig.LargeOutputThreshold,
		TruncateMode:         toolOutputConfig.TruncateMode,
	})
	slog.Info("Tool output truncation",
		"maxLines", toolOutputConfig.MaxLines,
		"maxBytes", toolOutputConfig.MaxBytes,
		"maxChars", toolOutputConfig.MaxChars,
		"largeOutputThreshold", toolOutputConfig.LargeOutputThreshold,
		"truncateMode", toolOutputConfig.TruncateMode,
	)

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
		if !compactor.ShouldCompact(messages) {
			return
		}
		if !sess.CanCompact(compactor) {
			slog.Info("Pre-request compaction skipped: session not compactable",
				"trigger", trigger,
				"messages", len(messages),
				"estimatedTokens", compactor.EstimateContextTokens(messages))
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

				ag.GetContext().Messages = sess.GetMessages()
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

	// Set up handlers
	server.SetPromptHandler(func(req rpc.PromptRequest) error {
		slog.Info("Received prompt:", "value", req.Message)
		message := strings.TrimSpace(req.Message)
		if message == "" {
			return fmt.Errorf("empty prompt message")
		}
		if len(req.Images) > 0 {
			return fmt.Errorf("images are not supported in this RPC implementation")
		}

		// Expand /skill:name commands
		expandedMessage := expandSkillCommands(message)
		if skill.IsSkillCommand(message) {
			slog.Info("Expanded skill command", "original", message, "skill", skill.ExtractSkillName(message))
		}

		stateMu.Lock()
		streaming := isStreaming
		mode := steeringMode
		followMode := followUpMode
		pending := pendingSteer
		stateMu.Unlock()

		if streaming {
			behavior := strings.TrimSpace(req.StreamingBehavior)
			if behavior == "" {
				return fmt.Errorf("agent is streaming; specify streamingBehavior")
			}
			switch behavior {
			case "steer":
				if mode == "one-at-a-time" && pending {
					return fmt.Errorf("steer already pending")
				}
				stateMu.Lock()
				pendingSteer = true
				stateMu.Unlock()
				ag.Steer(expandedMessage)
				return nil
			case "followUp", "follow_up":
				if followMode == "one-at-a-time" && ag.GetPendingFollowUps() > 0 {
					return fmt.Errorf("follow-up queue already has a pending message")
				}
				return ag.FollowUp(expandedMessage)
			default:
				return fmt.Errorf("invalid streamingBehavior: %s", behavior)
			}
		}

		compactBeforeRequest("pre_request_prompt")
		return ag.Prompt(expandedMessage)
	})

	server.SetSteerHandler(func(message string) error {
		slog.Info("Received steer:", "value", message)
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("empty steer message")
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
			return fmt.Errorf("steer already pending")
		}
		if !streaming {
			compactBeforeRequest("pre_request_steer")
		}
		stateMu.Lock()
		pendingSteer = true
		stateMu.Unlock()
		ag.Steer(expandedMessage)
		return nil
	})

	server.SetFollowUpHandler(func(message string) error {
		slog.Info("Received follow_up:", "value", message)
		if strings.TrimSpace(message) == "" {
			return fmt.Errorf("empty follow-up message")
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
			return fmt.Errorf("follow-up queue already has a pending message")
		}
		return ag.FollowUp(expandedMessage)
	})

	server.SetAbortHandler(func() error {
		slog.Info("Received abort")
		ag.Abort()
		return nil
	})

	server.SetClearSessionHandler(func() error {
		slog.Info("Received clear_session")
		if err := sess.Clear(); err != nil {
			return err
		}
		// Clear agent context
		setAgentContext(createBaseContext())
		slog.Info("Session cleared")
		return nil
	})

	server.SetNewSessionHandler(func(name, title string) (string, error) {
		slog.Info("Received new_session", "name", name, "title", title)
		if strings.TrimSpace(name) == "" {
			name = time.Now().Format("20060102-150405")
		}
		if strings.TrimSpace(title) == "" {
			title = name
		}
		newSess, err := sessionMgr.CreateSession(name, title)
		if err != nil {
			return "", err
		}

		newSessionID := newSess.GetID()

		// Update session manager's current ID
		if err := sessionMgr.SetCurrent(newSessionID); err != nil {
			return "", err
		}

		// Update current session metadata
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}

		sess = newSess
		sessionComp.Update(sess, compactor)
		setAgentContext(createBaseContext())

		stateMu.Lock()
		sessionID = newSessionID
		sessionName = name
		stateMu.Unlock()

		slog.Info("Created new session", "name", name, "id", newSessionID)
		return newSessionID, nil
	})

	server.SetListSessionsHandler(func() ([]any, error) {
		slog.Info("Received list_sessions")
		sessions, err := sessionMgr.ListSessions()
		if err != nil {
			return nil, err
		}

		result := make([]any, len(sessions))
		for i, sess := range sessions {
			result[i] = sess
		}
		return result, nil
	})

	server.SetSwitchSessionHandler(func(id string) error {
		slog.Info("Received switch_session: id=", "id", id)
		if id == "" {
			return fmt.Errorf("session id is required")
		}

		// Treat absolute or relative path as session file
		if strings.Contains(id, string(os.PathSeparator)) || strings.HasSuffix(id, ".jsonl") {
			sessionPath, err := normalizeSessionPath(id)
			if err != nil {
				return err
			}
			// LoadSessionLazy expects session directory, not file path
			// Extract directory if sessionPath points to messages.jsonl
			sessionDir := sessionPath
			if strings.HasSuffix(sessionPath, ".jsonl") {
				info, err := os.Stat(sessionPath)
				if err != nil {
					return err
				}
				if !info.IsDir() {
					sessionDir = filepath.Dir(sessionPath)
				}
			}
			newSess, err := session.LoadSessionLazy(sessionDir, session.DefaultLoadOptions())
			if err != nil {
				return err
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

			stateMu.Lock()
			sessionID = newSessionID
			sessionName = resolveSessionName(sessionMgr, newSessionID)
			stateMu.Unlock()

			slog.Info("Switched to session", "id", newSessionID, "count", len(newSess.GetMessages()))
			return nil
		}

		if err := sessionMgr.SetCurrent(id); err != nil {
			return err
		}

		// Load the new session
		newSess, err := sessionMgr.GetSession(id)
		if err != nil {
			return err
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

		stateMu.Lock()
		sessionID = newSessionID
		sessionName = resolveSessionName(sessionMgr, newSessionID)
		stateMu.Unlock()

		slog.Info("Switched to session", "id", newSessionID, "count", len(newSess.GetMessages()))
		return nil
	})

	server.SetDeleteSessionHandler(func(id string) error {
		slog.Info("Received delete_session: id=", "id", id)
		return sessionMgr.DeleteSession(id)
	})

	server.SetSetSessionNameHandler(func(name string) error {
		slog.Info("Received set_session_name", "name", name)
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return fmt.Errorf("session name cannot be empty")
		}
		if _, err := sess.AppendSessionInfo(trimmed, ""); err != nil {
			return err
		}
		if err := sessionMgr.UpdateSessionName(sessionID, trimmed, ""); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}
		stateMu.Lock()
		sessionName = trimmed
		stateMu.Unlock()
		return nil
	})

	server.SetGetStateHandler(func() (*rpc.SessionState, error) {
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
			AILogPath:             traceOutputPath,
			AIWorkingDir:          cwd,
			AutoCompactionEnabled: autoCompact,
			MessageCount:          len(ag.GetMessages()),
			PendingMessageCount:   ag.GetPendingFollowUps(),
			Compaction:            compactionState,
		}, nil
	})

	server.SetGetMessagesHandler(func() ([]any, error) {
		slog.Info("Received get_messages")
		messages := ag.GetMessages()
		result := make([]any, len(messages))
		for i, msg := range messages {
			result[i] = msg
		}
		return result, nil
	})

	server.SetCompactHandler(func() (*rpc.CompactResult, error) {
		slog.Info("Received compact")
		beforeCount := len(ag.GetMessages())

		// Estimate current context usage
		estimatedTokens := compactor.EstimateContextTokens(ag.GetMessages())
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
				ag.GetContext().Messages = sess.GetMessages()

				afterCount := len(ag.GetMessages())
				span.AddField("after_messages", afterCount)
				span.AddField("tokens_before", result.TokensBefore)
				span.AddField("tokens_after", result.TokensAfter)
				compactionInfo.After = afterCount

				slog.Info("Compact successful", "before", beforeCount, "after", afterCount)
				response = &rpc.CompactResult{
					Summary:          result.Summary,
					FirstKeptEntryID: result.FirstKeptEntryID,
					TokensBefore:     result.TokensBefore,
					TokensAfter:      result.TokensAfter,
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

	server.SetGetAvailableModelsHandler(func() ([]rpc.ModelInfo, error) {
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
		return models, nil
	})

	server.SetSetModelHandler(func(provider, modelID string) (*rpc.ModelInfo, error) {
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
			ID:       spec.ID,
			Provider: spec.Provider,
			BaseURL:  spec.BaseURL,
			API:      spec.API,
		}
		apiKey = newAPIKey

		cfg.Model.ID = spec.ID
		cfg.Model.Provider = spec.Provider
		cfg.Model.BaseURL = spec.BaseURL
		cfg.Model.API = spec.API

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

	server.SetCycleModelHandler(func() (*rpc.CycleModelResult, error) {
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
			ID:       next.ID,
			Provider: next.Provider,
			BaseURL:  next.BaseURL,
			API:      next.API,
		}
		apiKey = newAPIKey

		cfg.Model.ID = next.ID
		cfg.Model.Provider = next.Provider
		cfg.Model.BaseURL = next.BaseURL
		cfg.Model.API = next.API

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
	server.SetGetCommandsHandler(func() ([]rpc.SlashCommand, error) {
		slog.Info("Received get_commands")
		return skillCommands, nil
	})

	server.SetGetSessionStatsHandler(func() (*rpc.SessionStats, error) {
		slog.Info("Received get_session_stats")
		messages := ag.GetMessages()
		userCount, assistantCount, toolCalls, toolResults, tokens, cost := collectSessionUsage(messages)
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
		}, nil
	})

	server.SetBashHandler(func(command string) (*rpc.BashResult, error) {
		slog.Info("Received bash")
		return bashRunner.Run(cwd, command, bashTimeout)
	})

	server.SetAbortBashHandler(func() error {
		slog.Info("Received abort_bash")
		return bashRunner.Abort()
	})

	server.SetSetAutoRetryHandler(func(enabled bool) error {
		slog.Info("Received set_auto_retry", "enabled", enabled)
		ag.SetAutoRetry(enabled)
		return nil
	})

	server.SetAbortRetryHandler(func() error {
		slog.Info("Received abort_retry")
		ag.Abort()
		return nil
	})

	server.SetExportHTMLHandler(func(outputPath string) (string, error) {
		slog.Info("Received export_html", "outputPath", outputPath)
		return "", fmt.Errorf("export_html is not supported")
	})

	server.SetSetAutoCompactionHandler(func(enabled bool) error {
		slog.Info("Received set_auto_compaction: enabled=", "value", enabled)
		compactorConfig.AutoCompact = enabled
		stateMu.Lock()
		autoCompactionEnabled = enabled
		stateMu.Unlock()
		return nil
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

	server.SetSetToolCallCutoffHandler(func(cutoff int) error {
		slog.Info("Received set_tool_call_cutoff", "cutoff", cutoff)
		if cutoff < 0 {
			return fmt.Errorf("cutoff must be >= 0")
		}
		compactorConfig.ToolCallCutoff = cutoff
		ag.SetToolCallCutoff(cutoff)
		if err := config.SaveConfig(cfg, configPath); err != nil {
			slog.Info("Failed to save config:", "value", err)
		}
		return nil
	})

	server.SetSetTraceEventsHandler(func(events []string) ([]string, error) {
		slog.Info("Received set_trace_events", "events", events)

		if len(events) == 0 {
			// Empty array means reset to default set.
			return traceevent.ResetToDefaultEvents(), nil
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

		op := normalized[0]
		switch op {
		case "default":
			return traceevent.ResetToDefaultEvents(), nil
		case "all":
			expanded, _ := traceevent.ExpandEventSelectors([]string{"all"})
			return applyExpanded(expanded, true), nil
		case "off", "none":
			traceevent.DisableAllEvents()
			return []string{}, nil
		case "enable":
			if len(normalized) == 1 {
				return nil, fmt.Errorf("trace-events enable requires at least one selector")
			}
			expanded, unknown := traceevent.ExpandEventSelectors(normalized[1:])
			if len(unknown) > 0 {
				return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
			}
			return applyExpanded(expanded, false), nil
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
			return traceevent.GetEnabledEvents(), nil
		default:
			// Backward-compatible absolute set.
			expanded, unknown := traceevent.ExpandEventSelectors(normalized)
			if len(unknown) > 0 {
				return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
			}
			return applyExpanded(expanded, true), nil
		}
	})

	server.SetGetTraceEventsHandler(func() ([]string, error) {
		slog.Info("Received get_trace_events")
		return traceevent.GetEnabledEvents(), nil
	})

	server.SetSetToolSummaryStrategyHandler(func(strategy string) error {
		strategy = strings.ToLower(strings.TrimSpace(strategy))
		slog.Info("Received set_tool_summary_strategy", "strategy", strategy)
		if !validToolSummaryStrategies[strategy] {
			return fmt.Errorf("invalid tool summary strategy")
		}
		compactorConfig.ToolSummaryStrategy = strategy
		ag.SetToolSummaryStrategy(strategy)
		if err := config.SaveConfig(cfg, configPath); err != nil {
			slog.Info("Failed to save config:", "value", err)
		}
		return nil
	})

	server.SetSetToolSummaryAutomationHandler(func(mode string) error {
		mode = strings.ToLower(strings.TrimSpace(mode))
		slog.Info("Received set_tool_summary_automation", "mode", mode)
		if !validToolSummaryAutomations[mode] {
			return fmt.Errorf("invalid tool summary automation mode")
		}
		compactorConfig.ToolSummaryAutomation = mode
		ag.SetToolSummaryAutomation(mode)
		if err := config.SaveConfig(cfg, configPath); err != nil {
			slog.Info("Failed to save config:", "value", err)
		}
		return nil
	})

	validSteeringModes := map[string]bool{
		"all":           true,
		"one-at-a-time": true,
	}
	validFollowUpModes := map[string]bool{
		"all":           true,
		"one-at-a-time": true,
	}

	server.SetSetSteeringModeHandler(func(mode string) error {
		slog.Info("Received set_steering_mode", "mode", mode)
		mode = strings.ToLower(strings.TrimSpace(mode))
		if !validSteeringModes[mode] {
			return fmt.Errorf("invalid steering mode")
		}
		stateMu.Lock()
		steeringMode = mode
		stateMu.Unlock()
		return nil
	})

	server.SetSetFollowUpModeHandler(func(mode string) error {
		slog.Info("Received set_follow_up_mode", "mode", mode)
		mode = strings.ToLower(strings.TrimSpace(mode))
		if !validFollowUpModes[mode] {
			return fmt.Errorf("invalid follow-up mode")
		}
		stateMu.Lock()
		followUpMode = mode
		stateMu.Unlock()
		return nil
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

	server.SetSetThinkingLevelHandler(func(level string) (string, error) {
		level = strings.ToLower(strings.TrimSpace(level))
		if !validThinkingLevels[level] {
			return "", fmt.Errorf("invalid thinking level")
		}
		stateMu.Lock()
		currentThinkingLevel = level
		stateMu.Unlock()
		ag.SetThinkingLevel(level)
		return level, nil
	})

	server.SetCycleThinkingLevelHandler(func() (string, error) {
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
		return next, nil
	})

	server.SetGetLastAssistantTextHandler(func() (string, error) {
		slog.Info("Received get_last_assistant_text")
		messages := ag.GetMessages()
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "assistant" {
				return messages[i].ExtractText(), nil
			}
		}
		return "", nil
	})

	server.SetGetForkMessagesHandler(func() ([]rpc.ForkMessage, error) {
		slog.Info("Received get_fork_messages")
		forkMessages := sess.GetUserMessagesForForking()
		result := make([]rpc.ForkMessage, 0, len(forkMessages))
		for _, msg := range forkMessages {
			result = append(result, rpc.ForkMessage{
				EntryID: msg.EntryID,
				Text:    msg.Text,
			})
		}
		return result, nil
	})

	server.SetGetTreeHandler(func() ([]rpc.TreeEntry, error) {
		slog.Info("Received get_tree")
		entries := sess.GetEntries()
		tree := buildTreeEntries(entries, sess.GetLeafID())
		return tree, nil
	})

	server.SetResumeOnBranchHandler(func(entryID string) error {
		slog.Info("Received resume_on_branch", "entryId", entryID)
		stateMu.Lock()
		streaming := isStreaming
		stateMu.Unlock()
		if streaming {
			return fmt.Errorf("agent is busy")
		}

		entryID = strings.TrimSpace(entryID)
		if entryID == "" {
			return fmt.Errorf("entryId is required")
		}

		if entryID == "root" {
			sess.ResetLeaf()
		} else {
			if err := sess.Branch(entryID); err != nil {
				return err
			}
		}

		setAgentContext(createBaseContext())

		if err := sessionMgr.SaveCurrent(); err != nil {
			slog.Info("Failed to update session metadata:", "value", err)
		}

		return nil
	})

	server.SetForkHandler(func(entryID string) (*rpc.ForkResult, error) {
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
					if err := sessionWriter.Replace(sess, event.Messages); err != nil {
						slog.Info("Failed to replace session messages on agent_end:", "value", err)
					}
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
							if err := sessionWriter.Replace(sess, event.Messages); err != nil {
								slog.Info("Failed to replace session messages on agent_end:", "value", err)
							}
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
	server.EmitEvent(map[string]any{
		"type":  "server_start",
		"model": model.ID,
		"tools": []string{"read", "bash", "write", "grep", "edit"},
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
