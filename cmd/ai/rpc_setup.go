package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

			"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"

	"github.com/tiancaiamao/ai/pkg/agentconfig"
)

// rpcAppSetupParams holds the parameters needed to construct an rpcApp.
type rpcAppSetupParams struct {
	customSystemPrompt string
	maxTurns           int
	debugAddr          string
	agentConfigPath    string
}

// newRPCApp constructs a fully initialized rpcApp by performing all setup:
// config loading, model resolution, session loading/creation, tool registration,
// compactor creation, and skill loading.
func newRPCApp(sessionPath string, params rpcAppSetupParams) (*rpcApp, error) {
	// --- Agent config (optional) ---
	var agentCfg *agentconfig.AgentConfig
	if params.agentConfigPath != "" {
		var err error
		agentCfg, err = agentconfig.Load(params.agentConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load agent config: %w", err)
		}
		slog.Info("Loaded agent config", "path", params.agentConfigPath)
	}

	// --- Config + Logger ---
	cfg, configPath, err := loadConfigWithLogger()
	if err != nil {
		return nil, err
	}

	// --- Model + API Key ---
	model, apiKey, activeSpec, err := resolveModelAndKey(cfg)
	if err != nil {
		return nil, err
	}

	currentModelInfo := modelInfoFromSpec(activeSpec)
	currentModelInfo.MaxTokens = model.MaxTokens
	currentModelInfo.ContextWindow = model.ContextWindow
	currentContextWindow := activeSpec.ContextWindow

	// --- Working directory ---
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get working directory: %w", err)
	}
	// --- Session ---
	sessionPath, err = normalizeSessionPath(sessionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize session path: %w", err)
	}

	sess, sessionID, sessionName, sessionMgr, err := loadOrCreateSession(sessionPath, cwd)
	if err != nil {
		return nil, err
	}

	// --- Workspace & Tools ---
	ws, registry, err := createWorkspaceAndRegistry(cwd, cfg)
	if err != nil {
		return nil, err
	}

	// --- Compactors ---
		compactor, ctxManager, compactorConfig := createCompactors(cfg, model, apiKey, currentContextWindow, agentCfg)

	slog.Info("Registered tools: read, bash, write, grep, edit", "count", len(registry.All()))

	// --- Trace + Skills ---
	traceHandler, traceOutputPath, err := initTraceFileHandler(sessionID)
	_ = traceHandler
	if err != nil {
		slog.Warn("Failed to create trace handler", "outputDir", traceOutputPath, "error", err)
	} else {
		slog.Info("Trace handler initialized", "outputDir", traceOutputPath)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}
	agentDir := filepath.Join(homeDir, ".ai")

	skillResult, skillStats := loadSkills(agentDir, cwd, registry)

	// --- Build rpcApp ---
	app := &rpcApp{
		customSystemPrompt:    params.customSystemPrompt,
		maxTurns:              params.maxTurns,
		debugAddr:             params.debugAddr,
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
		agentConfig:           agentCfg,
		steeringMode:          "all",
		followUpMode:          "one-at-a-time",
		currentThinkingLevel:  "high",
		showThinking:          true,
		showTools:             true,
		showPrefix:            true,
		busyMode:              "steer",
	}

	app.initHelpers()
	return app, nil
}

// loadConfigWithLogger loads the config file and initializes the slog logger.
func loadConfigWithLogger() (*config.Config, string, error) {
	configPath, err := config.GetDefaultConfigPath()
	if err != nil {
		return nil, "", fmt.Errorf("failed to get config path: %w", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		slog.Warn("Failed to load config", "path", configPath, "error", err)
		cfg, _ = config.LoadConfig(configPath)
	}

	log, err := cfg.Log.CreateLogger()
	if err != nil {
		return nil, "", fmt.Errorf("failed to create logger: %w", err)
	}
	slog.SetDefault(log)

	return cfg, configPath, nil
}

// resolveModelAndKey resolves the LLM model and API key from config.
func resolveModelAndKey(cfg *config.Config) (llm.Model, string, config.ModelSpec, error) {
	model := cfg.GetLLMModel()

	apiKey, err := config.ResolveAPIKey(model.Provider)
	if err != nil {
		return llm.Model{}, "", config.ModelSpec{}, fmt.Errorf("missing API key: %w", err)
	}

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

	return model, apiKey, activeSpec, nil
}

// loadOrCreateSession loads an existing session or creates a new one.
func loadOrCreateSession(sessionPath string, cwd string) (*session.Session, string, string, *session.SessionManager, error) {
	sessionsDir, err := session.GetDefaultSessionsDir(cwd)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("failed to get sessions path: %w", err)
	}

	if sessionPath != "" {
		sessionsDir = filepath.Dir(sessionPath)
	}
	sessionMgr := session.NewSessionManager(sessionsDir)

	var sess *session.Session
	var sessionID string
	var sessionName string

	if sessionPath != "" {
		opts := session.DefaultLoadOptions()
		sess, err = session.LoadSessionLazy(sessionPath, opts)
		if err != nil {
			return nil, "", "", nil, fmt.Errorf("failed to load session from %s: %w", sessionPath, err)
		}
		sessionID = sess.GetID()
		sessionName = resolveSessionName(sessionMgr, sessionID)
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
				return nil, "", "", nil, fmt.Errorf("failed to create new session: %w", err)
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
			sessionName = resolveSessionName(sessionMgr, sessionID)
			slog.Info("Restored previous session", "id", sessionID, "name", sessionName, "count", len(sess.GetMessages()))
		}
	}

	return sess, sessionID, sessionName, sessionMgr, nil
}

// createWorkspaceAndRegistry creates the workspace and tool registry.
func createWorkspaceAndRegistry(cwd string, cfg *config.Config) (*tools.Workspace, *tools.Registry, error) {
	ws, err := tools.NewWorkspace(cwd)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	registry := tools.NewRegistry()
	readTool := tools.NewReadTool(ws)
	editTool := tools.NewEditTool(ws)

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

	return ws, registry, nil
}

// createCompactors creates the main compactor and context manager.
func createCompactors(cfg *config.Config, model llm.Model, apiKey string, contextWindow int, agentCfg *agentconfig.AgentConfig) (*compact.Compactor, *compact.ContextManager, *compact.Config) {
	compactorConfig := cfg.Compactor
	if compactorConfig == nil {
		compactorConfig = compact.DefaultConfig()
	}

	compactor := compact.NewCompactor(
		compactorConfig,
		model,
		apiKey,
		prompt.CompactorBasePrompt(),
		contextWindow,
	)

	ctxMgmtConfig := compact.DefaultContextManagerConfig()

	// Apply context_management config from agent.yaml if present
	if agentCfg != nil {
		if cmCfg := agentCfg.ResolveContextManagementConfig(); cmCfg != nil {
			ctxMgmtConfig.StaleAnnotation = cmCfg.StaleAnnotation
			if cmCfg.StaleAgeInvestigative > 0 {
				ctxMgmtConfig.StaleAgeInvestigative = cmCfg.StaleAgeInvestigative
			}
			if cmCfg.StaleAgeModification > 0 {
				ctxMgmtConfig.StaleAgeModification = cmCfg.StaleAgeModification
			}
			if customPrompt := agentCfg.LoadContextManagementPrompt(); customPrompt != "" {
				ctxMgmtConfig.ContextMgmtPrompt = customPrompt
			}
		}
	}

	ctxManager := compact.NewContextManager(
		ctxMgmtConfig,
		model,
		apiKey,
		contextWindow,
		prompt.ContextManagementSystemPrompt(),
		compactor,
	)

	return compactor, ctxManager, compactorConfig
}

// loadSkills loads skills from the agent directory and registers find_skill tool.
func loadSkills(agentDir string, cwd string, registry *tools.Registry) (*skill.LoadResult, *skill.SkillStatsFile) {
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

	skillStats := skill.LoadStats(filepath.Join(agentDir, "skill-stats.json"))
	registry.Register(tools.NewFindSkillTool(skillResult.Skills, skillStats))

	return skillResult, skillStats
}